package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Autumn-27/artex/config"
)

const (
	jwtKeyFilename = "jwt.key"
	authPassKey    = "auth.password_hash"
	jwtTTL         = 7 * 24 * time.Hour
	keyChars       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// The lower bound matches the setup page's frontend validation — validation that
	// lives only in the frontend is as good as none: hitting the API directly bypasses
	// it. The upper bound is bcrypt's hard limit: past 72 bytes GenerateFromPassword returns ErrPasswordTooLong; rejecting early beats a cryptic "password encryption failed".
	// Both apply to new passwords only (init, change-password); login never checks
	// length, so passwords set under the old 8-character minimum still log in.
	minPasswordRunes = 12
	maxPasswordBytes = 72
)

// errDataSourceUnavailable is the uniform reply when a password-related read fails.
// These handlers must never mistake a failed read for "not set": authInit once let an
// unauthenticated request through on a database error, overwriting the admin password.
const errDataSourceUnavailable = "Data source temporarily unavailable, please retry later"

// validatePassword returns an empty string when passing, otherwise a reason ready to show the user.
func validatePassword(pw string) string {
	if utf8.RuneCountInString(pw) < minPasswordRunes {
		return fmt.Sprintf("Password must be at least %d characters", minPasswordRunes)
	}
	if len(pw) > maxPasswordBytes {
		return fmt.Sprintf("Password must not exceed %d bytes", maxPasswordBytes)
	}
	return ""
}

// jwtLegacyBaseDir is the second legacy key location (the executable's directory,
// where releases before ARTEX_JWT_KEY_DIR wrote the key). A var so tests can point it
// at a temp dir.
var jwtLegacyBaseDir = config.BaseDir

// loadOrCreateJWTKey reads the 32-byte signing key from keyDir/jwt.key. keyDir is
// ARTEX_JWT_KEY_DIR (the /app/keys volume in Docker) or the project base dir, NOT
// the browsable workspace root (dataDir) — the signing key must never be
// listable/downloadable via the file manager. When keyDir/jwt.key is missing, a key
// at dataDir/jwt.key, then at BaseDir()/jwt.key, is migrated (key preserved, so
// sessions stay valid) and the old file removed. Otherwise a random key is
// generated. An existing key file is never overwritten.
func loadOrCreateJWTKey(keyDir, dataDir string) ([]byte, error) {
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("create jwt key dir: %w", err)
	}
	path := filepath.Join(keyDir, jwtKeyFilename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := migrateJWTKey(path, dataDir); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(data))
		if len(key) < 32 {
			return nil, fmt.Errorf("jwt key %s is shorter than 32 bytes; fix or remove it (it is never overwritten)", path)
		}
		return []byte(key), nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read jwt key: %w", err)
	}
	buf := make([]byte, 32)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(keyChars))))
		if err != nil {
			return nil, fmt.Errorf("generate jwt key: %w", err)
		}
		buf[i] = keyChars[n.Int64()]
	}
	if err := writeNewJWTKey(path, buf); err != nil {
		return nil, fmt.Errorf("write jwt key: %w", err)
	}
	log.Printf("[auth] new JWT key written to %s", path)
	return buf, nil
}

// migrateJWTKey copies the first legacy key found (dataDir, then BaseDir) to dst
// and removes the old file. Candidates equal to dst are skipped.
func migrateJWTKey(dst, dataDir string) error {
	for _, src := range []string{
		filepath.Join(dataDir, jwtKeyFilename),
		filepath.Join(jwtLegacyBaseDir(), jwtKeyFilename),
	} {
		if samePath(src, dst) {
			continue
		}
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			log.Printf("[auth] cannot read legacy JWT key %s: %v", src, err)
			continue
		}
		if err := writeNewJWTKey(dst, data); err != nil {
			return fmt.Errorf("migrate jwt key from %s: %w", src, err)
		}
		if err := os.Remove(src); err != nil {
			log.Printf("[auth] JWT key copied to %s but the old file %s could not be removed: %v", dst, src, err)
		}
		log.Printf("[auth] JWT key migrated from %s to %s", src, dst)
		return nil
	}
	return nil
}

// writeNewJWTKey creates path with mode 0600, failing if it already exists.
func writeNewJWTKey(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func samePath(a, b string) bool {
	if absA, err := filepath.Abs(a); err == nil {
		a = absA
	}
	if absB, err := filepath.Abs(b); err == nil {
		b = absB
	}
	return a == b
}

// signJWT issues a 7-day HS256 token for user ARTEX.
func signJWT(key []byte) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "ARTEX",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}).SignedString(key)
}

// verifyJWT returns true when tokenStr is a valid, non-expired HS256 token.
func verifyJWT(tokenStr string, key []byte) bool {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	return err == nil && t.Valid
}

// jwtSubject returns the subject of tokenStr when it is a valid, non-expired
// HS256 token, and "" otherwise.
func jwtSubject(tokenStr string, key []byte) string {
	var claims jwt.RegisteredClaims
	t, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	if err != nil || !t.Valid {
		return ""
	}
	return claims.Subject
}

// approver is who a human decision is recorded for: the subject of the
// request's session token.
func (s *Server) approver(r *http.Request) string {
	return jwtSubject(extractToken(r), s.jwtKey)
}

// extractToken reads the JWT from Authorization: Bearer header,
// artex_token cookie, or ?token= query param (for SSE connections).
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("artex_token"); err == nil && c.Value != "" {
		return c.Value
	}
	return r.URL.Query().Get("token")
}

// requireAuth wraps h with JWT validation.
// /api/auth/* and /api/health are exempt.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/auth/") || p == "/api/health" {
			h.ServeHTTP(w, r)
			return
		}
		tok := extractToken(r)
		if tok == "" {
			writeErr(w, 401, "Unauthorized")
			return
		}
		if !verifyJWT(tok, s.jwtKey) {
			writeErr(w, 401, "token is invalid or expired")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// GET /api/auth/status — reports whether the admin password has been initialised.
// A read failure must answer 503, not initialized:false: on initialized:false the
// frontend ships the user to /setup to set a password (login/page.tsx); wrapping a
// database outage in a 200 pushes the user toward overwriting the existing password.
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	hash, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	writeJSON(w, 200, map[string]any{"initialized": hash != ""})
}

// initSetupToken generates the one-time setup token when no admin password is set
// and writes it to the server log. Without it, anyone who can reach the port
// before the operator could claim the instance through POST /api/auth/init.
func (s *Server) initSetupToken() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	if hash, _, err := s.m.pg.GetSetting(authPassKey); err == nil && hash != "" {
		return
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("[auth] generate setup token: %v", err)
	}
	s.setupToken = hex.EncodeToString(buf)
	log.Printf("[auth] no admin password is set. Setup token for the /setup page: %s", s.setupToken)
}

// checkSetupToken compares got with the active setup token in constant time.
// It fails when no token is active.
func (s *Server) checkSetupToken(got string) bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.setupToken)) == 1
}

// POST /api/auth/init — sets the password for the first time; rejected if already set.
// Requires the setup token from the server log.
func (s *Server) authInit(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	existing, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if existing != "" {
		writeErr(w, 403, "Password is already set")
		return
	}
	var req struct {
		Password   string `json:"password"`
		SetupToken string `json:"setup_token"`
	}
	if err := decode(r, &req); err != nil || req.Password == "" {
		writeErr(w, 400, "Password cannot be empty")
		return
	}
	// 403, not 401: the frontend treats any 401 as an expired session and redirects to /login.
	if !s.checkSetupToken(req.SetupToken) {
		writeErr(w, 403, "Invalid setup token. The server log prints it at startup while no password is set")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "Password encryption failed")
		return
	}
	// INSERT ... ON CONFLICT DO NOTHING instead of an upsert: the GetSetting above is
	// only a fast-fail path; the real "first-set-only" guarantee rests on the primary
	// key. bcrypt runs tens of milliseconds, another request may set the password meanwhile, and the read check itself can fail under an outage.
	inserted, err := pg.InsertSettingIfAbsent(authPassKey, string(hash))
	if err != nil {
		writeErr(w, 500, "Save failed: "+err.Error())
		return
	}
	if !inserted {
		writeErr(w, 403, "Password is already set")
		return
	}
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		writeErr(w, 500, "token generation failed")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok})
}

// POST /api/auth/change-password — changes the admin password. Requires a valid
// token (this route is under /api/auth/* which requireAuth exempts, so the token
// is validated here) AND the current password.
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if !verifyJWT(extractToken(r), s.jwtKey) {
		writeErr(w, 401, "Unauthorized")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "Invalid request format")
		return
	}
	if req.NewPassword == "" {
		writeErr(w, 400, "New password cannot be empty")
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "Password not initialized; set a password first")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)); err != nil {
		writeErr(w, 401, "Current password is wrong")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "Password encryption failed")
		return
	}
	if err := pg.SetSetting(authPassKey, string(newHash)); err != nil {
		writeErr(w, 500, "Save failed: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/login — validates username/password and returns a JWT.
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	s.login(w, r, pg.GetSetting)
}

// login is authLogin past the data-source check; getSetting is pg.GetSetting,
// a parameter so tests can run it without PostgreSQL. Every attempt counts
// against the caller's IP (s.loginLimit, keyed by clientIP) and a successful
// one clears it. There is no per-account lockout and no password length check here.
func (s *Server) login(w http.ResponseWriter, r *http.Request, getSetting func(string) (string, bool, error)) {
	ip := clientIP(r, s.trustedProxies)
	if wait := s.loginLimit.allow(ip); wait > 0 {
		secs := int((wait + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeErr(w, 429, fmt.Sprintf("Too many login attempts, try again in %d seconds", secs))
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "Invalid request format")
		return
	}
	if req.Username != "ARTEX" {
		writeErr(w, 401, "Incorrect username or password")
		return
	}
	hash, ok, err := getSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "Password not initialized; set a password first")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeErr(w, 401, "Incorrect username or password")
		return
	}
	s.loginLimit.reset(ip)
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		writeErr(w, 500, "token generation failed")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok})
}
