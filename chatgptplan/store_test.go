package chatgptplan

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testStore(t *testing.T) (*Store, *httptest.Server, *atomic.Int32, func(string, string) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	refreshes := &atomic.Int32{}
	var nonce string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/jwks", "revocation_endpoint": srv.URL + "/revoke"})
		case "/jwks":
			n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": "one", "kty": "RSA", "n": n, "e": e}}})
		case "/api/accounts/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			if r.Form.Get("resource") != resource || r.Form.Get("client_id") != "oaiapp_test" {
				t.Errorf("bad token exchange: %v", r.Form)
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				if r.Form.Get("refresh_token") != "refresh-one" {
					t.Errorf("refresh token not rotated correctly: %v", r.Form)
				}
				json.NewEncoder(w).Encode(map[string]any{"access_token": "access-two", "refresh_token": "refresh-two", "expires_in": 3600, "scope": requestedScopes})
				return
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" || r.Form.Get("redirect_uri") != "http://127.0.0.1:1455/auth/callback" {
				t.Errorf("bad code exchange: %v", r.Form)
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": srv.URL, "aud": "oaiapp_test", "sub": "person-1", "email": "test@example.com", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix()})
			token.Header["kid"] = "one"
			signed, _ := token.SignedString(key)
			json.NewEncoder(w).Encode(map[string]any{"id_token": signed, "access_token": "access-one", "refresh_token": "refresh-one", "token_type": "Bearer", "expires_in": 3600, "scope": requestedScopes})
		case "/revoke":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse revoke: %v", err)
			}
			if r.Form.Get("token") != "refresh-two" || r.Form.Get("client_id") != "oaiapp_test" {
				t.Errorf("bad revoke: %v", r.Form)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s := NewStore(filepath.Join(t.TempDir(), "auth", "chatgpt-plan.json"))
	s.authority, s.client = srv.URL, srv.Client()
	setNonce := func(n, _ string) string { nonce = n; return nonce }
	return s, srv, refreshes, setNonce
}

func authorize(t *testing.T, s *Store, setNonce func(string, string) string) url.Values {
	t.Helper()
	authURL, err := s.BeginLogin("http://127.0.0.1:1455/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "dynamic_agent_client" || q.Get("agent_name_hint") != "ARTEX" || q.Get("resource") != resource || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || !strings.HasPrefix(q.Get("ext_agent_host_id"), "urn:uuid:") {
		t.Fatalf("invalid authorize params: %v", q)
	}
	setNonce(q.Get("nonce"), "")
	return url.Values{"state": {q.Get("state")}, "code": {"once"}, "client_id": {"oaiapp_test"}}
}

func TestLoginValidatesIdentityAndStoresPrivateCredentials(t *testing.T) {
	s, _, _, setNonce := testStore(t)
	callback := authorize(t, s, setNonce)
	status, err := s.CompleteLogin(context.Background(), callback)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Connected || status.Email != "test@example.com" || status.ClientID != "oaiapp_test" {
		t.Fatalf("status: %+v", status)
	}
	if strings.Contains(status.Email, "access-one") {
		t.Fatal("status leaked token")
	}
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("credentials file mode %v is not private", info.Mode())
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && strings.Contains(string(b), "access-one") {
		t.Fatal("Windows credential file contains a plaintext token")
	}
	if runtime.GOOS != "windows" && !strings.Contains(string(b), "access-one") {
		t.Fatal("token was not saved")
	}
	got, err := s.AccessToken(context.Background())
	if err != nil || got != "access-one" {
		t.Fatalf("token: %q, %v", got, err)
	}
}

func TestLoginRejectsStateAndDoesNotExchangeCode(t *testing.T) {
	s, _, _, setNonce := testStore(t)
	callback := authorize(t, s, setNonce)
	wrong := url.Values{"state": {"wrong"}, "code": {"attacker"}}
	if _, err := s.CompleteLogin(context.Background(), wrong); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("wanted state mismatch, got %v", err)
	}
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatalf("credentials written after bad callback: %v", err)
	}
	if status, err := s.CompleteLogin(context.Background(), callback); err != nil || !status.Connected {
		t.Fatalf("legitimate callback canceled by wrong-state request: %+v, %v", status, err)
	}
}

func TestLoginRejectsWrongIDTokenNonce(t *testing.T) {
	s, _, _, setNonce := testStore(t)
	callback := authorize(t, s, setNonce)
	setNonce("wrong", "")
	if _, err := s.CompleteLogin(context.Background(), callback); err == nil || !strings.Contains(err.Error(), "nonce mismatch") {
		t.Fatalf("expected nonce mismatch, got %v", err)
	}
	status, err := s.Status()
	if err != nil || status.Connected || status.ClientID != "oaiapp_test" {
		t.Fatalf("registration/status: %+v, %v", status, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTerminalRefreshRequiresNewSignIn(t *testing.T) {
	for _, code := range []string{"invalid_grant", "refresh_token_reused"} {
		t.Run(code, func(t *testing.T) {
			s, srv, _, setNonce := testStore(t)
			callback := authorize(t, s, setNonce)
			if _, err := s.CompleteLogin(context.Background(), callback); err != nil {
				t.Fatal(err)
			}
			c, err := s.read()
			if err != nil {
				t.Fatal(err)
			}
			c.ExpiresAt = time.Now().Add(-time.Minute)
			if err := s.write(c); err != nil {
				t.Fatal(err)
			}
			s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/accounts/oauth/token" {
					return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"` + code + `"}`)), Header: make(http.Header), Request: r}, nil
				}
				return srv.Client().Transport.RoundTrip(r)
			})}
			if _, err := s.AccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "sign-in required") {
				t.Fatalf("refresh error: %v", err)
			}
			status, err := s.Status()
			if err != nil || status.Connected || status.ClientID != "oaiapp_test" {
				t.Fatalf("status after %s: %+v, %v", code, status, err)
			}
		})
	}
}

func TestLogoutNeverSendsRefreshTokenToForeignDiscoveryURL(t *testing.T) {
	s, srv, _, setNonce := testStore(t)
	callback := authorize(t, s, setNonce)
	if _, err := s.CompleteLogin(context.Background(), callback); err != nil {
		t.Fatal(err)
	}
	var attackerRequests atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attackerRequests.Add(1) }))
	defer attacker.Close()
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			payload, _ := json.Marshal(map[string]string{"revocation_endpoint": attacker.URL + "/steal"})
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(payload))), Header: make(http.Header), Request: r}, nil
		}
		return srv.Client().Transport.RoundTrip(r)
	})}
	if err := s.Logout(context.Background()); err == nil || !strings.Contains(err.Error(), "remote ChatGPT revocation was not confirmed") {
		t.Fatalf("logout: %v", err)
	}
	if attackerRequests.Load() != 0 {
		t.Fatal("refresh token sent to foreign revocation endpoint")
	}
	status, err := s.Status()
	if err != nil || status.Connected {
		t.Fatalf("local logout failed: %+v %v", status, err)
	}
}

func TestRefreshRotatesTokenOnceForConcurrentCalls(t *testing.T) {
	s, _, refreshes, setNonce := testStore(t)
	callback := authorize(t, s, setNonce)
	if _, err := s.CompleteLogin(context.Background(), callback); err != nil {
		t.Fatal(err)
	}
	c, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	c.ExpiresAt = time.Now().Add(-time.Minute)
	if err := s.write(c); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.AccessToken(context.Background())
			if err != nil || got != "access-two" {
				t.Errorf("refresh: %q, %v", got, err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refresh requests=%d, want one", refreshes.Load())
	}
	status, err := s.Status()
	if err != nil || !status.Connected {
		t.Fatalf("status after refresh: %+v, %v", status, err)
	}
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status()
	if err != nil || status.Connected {
		t.Fatalf("status after logout: %+v, %v", status, err)
	}
}
