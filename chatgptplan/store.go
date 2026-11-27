// Package chatgptplan implements the public Sign in with ChatGPT flow for a
// locally running ARTEX instance. It never reads Codex CLI credentials.
package chatgptplan

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const resource = "https://api.openai.com/v1"
const sharingScope = "chatgpt.tokens.use.direct"
const requestedScopes = "openid profile email offline_access resource.invoke " + sharingScope

// TokenSource supplies an OAuth bearer token to backend inference requests.
// The Store implements it; tests can provide a fake without opening a browser.
type TokenSource interface {
	AccessToken(context.Context) (string, error)
}

// Status contains only account display data. No token or authorization URL is
// included, so it can safely be returned by a local settings endpoint.
type Status struct {
	Connected bool      `json:"connected"`
	Email     string    `json:"email,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	ClientID  string    `json:"client_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type credential struct {
	ClientID     string    `json:"client_id"`
	HostID       string    `json:"ext_agent_host_id"`
	Issuer       string    `json:"issuer,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Email        string    `json:"email,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
}

type loginAttempt struct {
	state       string
	nonce       string
	verifier    string
	redirectURI string
	clientID    string
	subject     string
}

// Store keeps one registered ChatGPT connection per path. Give different ARTEX
// profiles different paths to keep their registrations and tokens separate.
// A Store instance serializes refresh and login for its credential file.
type Store struct {
	path      string
	client    *http.Client
	authority string
	now       func() time.Time
	mu        sync.Mutex
	pending   *loginAttempt
}

func NewStore(path string) *Store {
	return &Store{path: path, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, authority: "https://auth.openai.com", now: time.Now}
}

func (s *Store) read() (credential, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return credential{}, nil
	}
	if err != nil {
		return credential{}, err
	}
	b, err = unsealCredential(b)
	if err != nil {
		return credential{}, fmt.Errorf("unprotect ChatGPT credentials: %w", err)
	}
	var c credential
	if err := json.Unmarshal(b, &c); err != nil {
		return credential{}, fmt.Errorf("read ChatGPT credentials: %w", err)
	}
	return c, nil
}

func (s *Store) write(c credential) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	b, err = sealCredential(b)
	if err != nil {
		return fmt.Errorf("protect ChatGPT credentials: %w", err)
	}
	return atomicPrivateFile(s.path, b)
}

func atomicPrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".chatgpt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Rename replaces the previous record atomically on supported filesystems.
	// Fail closed if the destination cannot be replaced; never delete the only
	// valid credential record before its replacement is ready.
	return os.Rename(f.Name(), path)
}

func statusOf(c credential) Status {
	return Status{Connected: c.ClientID != "" && c.AccessToken != "" && c.RefreshToken != "" && hasScope(c.Scopes, sharingScope), Email: c.Email, Subject: c.Subject, ClientID: c.ClientID, ExpiresAt: c.ExpiresAt}
}

func hasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func (s *Store) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.read()
	if err != nil {
		return Status{}, err
	}
	return statusOf(c), nil
}

// BeginLogin creates an authorization URL for a loopback /auth/callback route.
// The caller must open it in the user's system browser; do not log the URL,
// since reauthorization may include an ID-token hint.
func (s *Store) BeginLogin(redirectURI string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/auth/callback" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("ChatGPT callback must be http://127.0.0.1:<port>/auth/callback")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.read()
	if err != nil {
		return "", err
	}
	if c.HostID == "" {
		c.HostID, err = s.hostID()
		if err != nil {
			return "", err
		}
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	nonce, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomURLSafe(48)
	if err != nil {
		return "", err
	}
	clientID := c.ClientID
	if clientID == "" {
		clientID = "dynamic_agent_client"
	}
	s.pending = &loginAttempt{state: state, nonce: nonce, verifier: verifier, redirectURI: redirectURI, clientID: clientID, subject: c.Subject}
	params := url.Values{
		"client_id": {clientID}, "ext_agent_host_id": {c.HostID},
		"response_type": {"code"}, "redirect_uri": {redirectURI},
		"scope": {requestedScopes}, "resource": {resource}, "state": {state},
		"nonce": {nonce}, "code_challenge_method": {"S256"},
	}
	challenge := sha256.Sum256([]byte(verifier))
	params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	if clientID == "dynamic_agent_client" {
		params.Set("agent_name_hint", "ARTEX")
	} else {
		if c.IDToken != "" {
			params.Set("id_token_hint", c.IDToken)
		}
		if c.Email != "" {
			params.Set("login_hint", c.Email)
		}
	}
	return s.authority + "/api/accounts/authorize?" + params.Encode(), nil
}

func (s *Store) hostID() (string, error) {
	path := s.path + ".host"
	b, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(b))
		if strings.HasPrefix(id, "urn:uuid:") {
			return id, nil
		}
		return "", errors.New("invalid ChatGPT host ID")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	hostID := "urn:uuid:" + id.String()
	return hostID, atomicPrivateFile(path, []byte(hostID))
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CompleteLogin validates the callback, exchanges its code, verifies the OIDC
// ID token and granted sharing scope, then atomically saves the connection.
func (s *Store) CompleteLogin(ctx context.Context, query url.Values) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.pending
	if a == nil {
		return Status{}, errors.New("no ChatGPT sign-in is pending")
	}
	if query.Get("state") != a.state {
		return Status{}, errors.New("ChatGPT sign-in state mismatch")
	}
	s.pending = nil
	if code := query.Get("error"); code != "" {
		return Status{}, fmt.Errorf("ChatGPT sign-in rejected: %s", code)
	}
	if query.Get("code") == "" {
		return Status{}, errors.New("ChatGPT callback has no authorization code")
	}
	c, err := s.read()
	if err != nil {
		return Status{}, err
	}
	clientID := a.clientID
	callbackID := query.Get("client_id")
	if clientID == "dynamic_agent_client" {
		if callbackID == "" || callbackID == clientID {
			return Status{}, errors.New("ChatGPT registration did not return an issued client ID")
		}
		clientID = callbackID
		c.ClientID = clientID
		if c.HostID == "" {
			c.HostID, err = s.hostID()
			if err != nil {
				return Status{}, err
			}
		}
		// Keep the issued registration even if code exchange fails.
		if err := s.write(c); err != nil {
			return Status{}, err
		}
	} else if callbackID != "" && callbackID != clientID {
		return Status{}, errors.New("ChatGPT callback client ID mismatch")
	}
	token, err := s.token(ctx, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID},
		"code": {query.Get("code")}, "code_verifier": {a.verifier},
		"redirect_uri": {a.redirectURI}, "resource": {resource},
	})
	if err != nil {
		return Status{}, err
	}
	if token.IDToken == "" || token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 || !strings.EqualFold(token.TokenType, "Bearer") {
		return Status{}, errors.New("ChatGPT sign-in returned incomplete credentials")
	}
	identity, err := s.verifyIDToken(ctx, token.IDToken, clientID, a.nonce)
	if err != nil {
		return Status{}, err
	}
	if a.subject != "" && identity.Subject != a.subject {
		return Status{}, errors.New("ChatGPT account changed during reauthorization")
	}
	newScopes := strings.Fields(token.Scope)
	if !hasScope(newScopes, sharingScope) {
		return Status{}, errors.New("ChatGPT plan usage permission was not granted")
	}
	c.ClientID, c.Issuer, c.Subject, c.Email = clientID, identity.Issuer, identity.Subject, identity.Email
	c.IDToken, c.AccessToken, c.RefreshToken, c.TokenType = token.IDToken, token.AccessToken, token.RefreshToken, token.TokenType
	c.Scopes = newScopes
	c.ExpiresAt = s.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	if err := s.write(c); err != nil {
		return Status{}, err
	}
	return statusOf(c), nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

type oauthError struct {
	Status int
	Code   string
}

func (e *oauthError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("ChatGPT token exchange: %s (HTTP %d)", e.Code, e.Status)
	}
	return fmt.Sprintf("ChatGPT token exchange: HTTP %d", e.Status)
}

func terminalRefreshError(err error) bool {
	var oauth *oauthError
	if !errors.As(err, &oauth) {
		return false
	}
	switch oauth.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_token", "unauthorized_client":
		return true
	}
	return false
}

func (s *Store) token(ctx context.Context, params url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authority+"/api/accounts/oauth/token", strings.NewReader(params.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("ChatGPT token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&problem)
		return tokenResponse{}, &oauthError{Status: resp.StatusCode, Code: problem.Error}
	}
	var out tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return tokenResponse{}, fmt.Errorf("decode ChatGPT token response: %w", err)
	}
	return out, nil
}

type identity struct{ Issuer, Subject, Email string }

func (s *Store) verifyIDToken(ctx context.Context, raw, clientID, nonce string) (identity, error) {
	var discovery struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := s.getJSON(ctx, s.authority+"/.well-known/openid-configuration", &discovery); err != nil {
		return identity{}, fmt.Errorf("ChatGPT OIDC discovery: %w", err)
	}
	if discovery.JWKSURI == "" {
		return identity{}, errors.New("ChatGPT OIDC discovery omitted JWKS URI")
	}
	var keys struct {
		Keys []struct{ KID, KTY, N, E string } `json:"keys"`
	}
	if err := s.getJSON(ctx, discovery.JWKSURI, &keys); err != nil {
		return identity{}, fmt.Errorf("ChatGPT JWKS: %w", err)
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, errors.New("unexpected ChatGPT ID-token algorithm")
		}
		kid, _ := t.Header["kid"].(string)
		for _, key := range keys.Keys {
			if key.KID == kid && key.KTY == "RSA" {
				n, nerr := base64.RawURLEncoding.DecodeString(key.N)
				e, eerr := base64.RawURLEncoding.DecodeString(key.E)
				if nerr != nil || eerr != nil || len(e) == 0 || len(e) > 4 {
					return nil, errors.New("invalid ChatGPT JWKS key")
				}
				exponent := 0
				for _, b := range e {
					exponent = exponent<<8 | int(b)
				}
				return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}, nil
			}
		}
		return nil, errors.New("ChatGPT ID-token key not found")
	}, jwt.WithIssuer(s.authority), jwt.WithAudience(clientID), jwt.WithExpirationRequired(), jwt.WithValidMethods([]string{"RS256"}), jwt.WithTimeFunc(s.now))
	if err != nil || !parsed.Valid {
		return identity{}, fmt.Errorf("verify ChatGPT ID token: %w", err)
	}
	if claims["nonce"] != nonce || nonce == "" {
		return identity{}, errors.New("ChatGPT ID-token nonce mismatch")
	}
	subject, _ := claims["sub"].(string)
	if subject == "" {
		return identity{}, errors.New("ChatGPT ID token has no subject")
	}
	email, _ := claims["email"].(string)
	return identity{Issuer: s.authority, Subject: subject, Email: email}, nil
}

func (s *Store) getJSON(ctx context.Context, endpoint string, dst any) error {
	if err := s.validateAuthEndpoint(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(dst)
}

func (s *Store) validateAuthEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	authority, parseErr := url.Parse(s.authority)
	if err != nil || parseErr != nil || u.Scheme != authority.Scheme || u.Host != authority.Host || u.User != nil || u.Fragment != "" {
		return errors.New("ChatGPT OIDC endpoint is outside the authorization authority")
	}
	return nil
}

// AccessToken returns a current bearer token, refreshing it near expiry. It is
// only for backend inference code and must never be serialized to the UI.
func (s *Store) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.read()
	if err != nil {
		return "", err
	}
	if !statusOf(c).Connected {
		return "", errors.New("ChatGPT account is not connected for plan usage")
	}
	if c.ExpiresAt.After(s.now().Add(2 * time.Minute)) {
		return c.AccessToken, nil
	}
	tok, err := s.token(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {c.RefreshToken}, "resource": {resource}})
	if err != nil {
		if terminalRefreshError(err) {
			c.AccessToken, c.RefreshToken, c.IDToken = "", "", ""
			c.Scopes = nil
			if writeErr := s.write(c); writeErr != nil {
				return "", writeErr
			}
			return "", fmt.Errorf("ChatGPT sign-in required: %w", err)
		}
		return "", err
	}
	if tok.AccessToken == "" || tok.ExpiresIn <= 0 || tok.TokenType != "" && !strings.EqualFold(tok.TokenType, "Bearer") {
		return "", errors.New("ChatGPT refresh returned incomplete credentials")
	}
	if tok.Scope != "" {
		c.Scopes = strings.Fields(tok.Scope)
		if !hasScope(c.Scopes, sharingScope) {
			c.AccessToken, c.RefreshToken, c.IDToken = "", "", ""
			if writeErr := s.write(c); writeErr != nil {
				return "", writeErr
			}
			return "", errors.New("ChatGPT plan usage permission was revoked")
		}
	}
	c.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.RefreshToken = tok.RefreshToken
	}
	// Retain the previously verified ID token for a future login hint. A refresh
	// response may contain a new ID token, but no login nonce is available here
	// to validate it as a replacement for the registered identity.
	if tok.TokenType != "" {
		c.TokenType = tok.TokenType
	}
	c.ExpiresAt = s.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if err := s.write(c); err != nil {
		return "", err
	}
	return c.AccessToken, nil
}

// Logout revokes the renewable session and clears its local tokens. It retains
// the client registration and host ID for a later reauthorization.
func (s *Store) Logout(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.read()
	if err != nil {
		return err
	}
	if c.RefreshToken != "" {
		var discovery struct {
			RevocationEndpoint string `json:"revocation_endpoint"`
		}
		if err = s.getJSON(ctx, s.authority+"/.well-known/openid-configuration", &discovery); err == nil {
			if discovery.RevocationEndpoint == "" {
				err = errors.New("ChatGPT OIDC discovery omitted revocation endpoint")
			} else if err = s.validateAuthEndpoint(discovery.RevocationEndpoint); err != nil {
				// Never send a refresh token to a discovery-provided foreign URL.
			} else {
				err = s.revoke(ctx, discovery.RevocationEndpoint, c)
			}
		}
	}
	c.AccessToken, c.RefreshToken, c.IDToken = "", "", ""
	c.Scopes = nil
	if writeErr := s.write(c); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("signed out locally; remote ChatGPT revocation was not confirmed: %w", err)
	}
	return nil
}

func (s *Store) revoke(ctx context.Context, endpoint string, c credential) error {
	params := url.Values{"token": {c.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {c.ClientID}}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := s.client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			if resp.StatusCode < 500 {
				return fmt.Errorf("ChatGPT revocation: HTTP %d", resp.StatusCode)
			}
			err = fmt.Errorf("ChatGPT revocation: HTTP %d", resp.StatusCode)
		}
		if attempt == 2 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * 100 * time.Millisecond):
		}
	}
	return nil
}
