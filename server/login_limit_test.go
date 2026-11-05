package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

// oldPassword is 8 characters: valid under the old minimum, too short for a new password now.
const oldPassword = "abcd1234"

// loginServer returns a Server whose login reads a stored hash of oldPassword
// from a stub instead of PostgreSQL.
func loginServer(t *testing.T, clk *fakeClock) (*Server, func(remote, password string, hdr map[string]string) *httptest.ResponseRecorder) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(oldPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	getSetting := func(key string) (string, bool, error) {
		if key != authPassKey {
			return "", false, nil
		}
		return string(hash), true, nil
	}
	s := &Server{jwtKey: []byte("0123456789abcdef0123456789abcdef"), loginLimit: newLoginLimiter(clk.now)}
	post := func(remote, password string, hdr map[string]string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"username":"ARTEX","password":%q}`, password)
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.login(w, r, getSetting)
		return w
	}
	return s, post
}

func failN(t *testing.T, post func(string, string, map[string]string) *httptest.ResponseRecorder, remote string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if w := post(remote, "wrong-password", nil); w.Code != 401 {
			t.Fatalf("attempt %d from %s: status=%d want 401; body=%s", i+1, remote, w.Code, w.Body.String())
		}
	}
}

func TestLoginRateLimitReturns429WithRetryAfter(t *testing.T) {
	clk := newFakeClock()
	_, post := loginServer(t, clk)
	const ip = "192.0.2.10:40000"
	failN(t, post, ip, loginMaxAttempts)

	w := post(ip, oldPassword, nil) // even the right password is refused while blocked
	if w.Code != 429 {
		t.Fatalf("status=%d want 429; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After=%q want 60", got)
	}
	var body struct {
		Error string `json:"error"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error == "" || body.Token != "" {
		t.Fatalf("want a JSON error body without a token, got %s (err=%v)", w.Body.String(), err)
	}

	// Each attempt while blocked doubles the wait, capped at loginMaxBlock.
	for _, want := range []string{"120", "240", "480", "900", "900"} {
		if got := post(ip, "wrong-password", nil).Header().Get("Retry-After"); got != want {
			t.Fatalf("Retry-After=%q want %s", got, want)
		}
	}

	// Once the block runs out the IP gets a fresh window.
	clk.advance(loginMaxBlock)
	if w := post(ip, oldPassword, nil); w.Code != 200 {
		t.Fatalf("after the block: status=%d want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestLoginSuccessResetsCounter(t *testing.T) {
	clk := newFakeClock()
	_, post := loginServer(t, clk)
	const ip = "192.0.2.11:40000"
	failN(t, post, ip, loginMaxAttempts-1)
	if w := post(ip, oldPassword, nil); w.Code != 200 {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	// The counter starts over: a full budget of failures before the next block.
	failN(t, post, ip, loginMaxAttempts)
	if w := post(ip, "wrong-password", nil); w.Code != 429 {
		t.Fatalf("status=%d want 429", w.Code)
	}
}

func TestLoginRateLimitIsPerIP(t *testing.T) {
	clk := newFakeClock()
	_, post := loginServer(t, clk)
	failN(t, post, "192.0.2.12:40000", loginMaxAttempts)
	if w := post("192.0.2.12:40001", "wrong-password", nil); w.Code != 429 {
		t.Fatalf("same IP, other port: status=%d want 429", w.Code)
	}
	if w := post("198.51.100.7:40000", "wrong-password", nil); w.Code != 401 {
		t.Fatalf("other IP: status=%d want 401", w.Code)
	}
	if w := post("[2001:db8::1]:40000", oldPassword, nil); w.Code != 200 {
		t.Fatalf("other IP: status=%d want 200", w.Code)
	}
}

func TestLoginRateLimitIgnoresForwardedFor(t *testing.T) {
	clk := newFakeClock()
	_, post := loginServer(t, clk)
	const blocked = "192.0.2.13"
	failN(t, post, blocked+":40000", loginMaxAttempts)

	// A blocked peer cannot escape by claiming another address.
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if w := post(blocked+":40000", oldPassword, map[string]string{h: "203.0.113.9"}); w.Code != 429 {
			t.Fatalf("%s from blocked peer: status=%d want 429", h, w.Code)
		}
	}
	// And another peer naming the blocked address is not blocked.
	if w := post("198.51.100.8:40000", oldPassword, map[string]string{"X-Forwarded-For": blocked}); w.Code != 200 {
		t.Fatalf("X-Forwarded-For naming the blocked IP: status=%d want 200", w.Code)
	}
}

// The 12-character minimum applies to new passwords only. A hash stored under the
// old 8-character minimum must still log in.
func TestEightCharPasswordLogsInButIsRejectedAsNew(t *testing.T) {
	if n := len([]rune(oldPassword)); n != 8 {
		t.Fatalf("oldPassword has %d characters, want 8", n)
	}
	_, post := loginServer(t, newFakeClock())
	w := post("192.0.2.14:40000", oldPassword, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("login with an existing 8-character password: status=%d body=%s", w.Code, w.Body.String())
	}
	// authInit and authChangePassword both refuse a new password through validatePassword.
	if msg := validatePassword(oldPassword); msg == "" {
		t.Fatal("validatePassword accepted an 8-character new password")
	}
}

func TestLoginLimiterSweepsIdleEntries(t *testing.T) {
	clk := newFakeClock()
	l := newLoginLimiter(clk.now)
	l.allow("192.0.2.20")
	clk.advance(loginMaxBlock - time.Second)
	l.allow("192.0.2.21") // sweeps, but .20 is not idle long enough yet
	if len(l.entries) != 2 {
		t.Fatalf("entries=%d want 2 before either is idle for %v", len(l.entries), loginMaxBlock)
	}
	clk.advance(loginSweepEvery)
	l.allow("192.0.2.22") // next sweep drops .20
	if _, ok := l.entries["192.0.2.20"]; ok || len(l.entries) != 2 {
		t.Fatalf("idle entry not swept: %d entries", len(l.entries))
	}
}

func TestLoginLimiterCapsEntries(t *testing.T) {
	l := newLoginLimiter(newFakeClock().now)
	for i := 0; i <= loginMaxEntries; i++ {
		l.allow(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	if len(l.entries) != loginMaxEntries {
		t.Fatalf("entries=%d want the cap %d", len(l.entries), loginMaxEntries)
	}
}

func TestHealthHidesVersionWithoutToken(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	s := &Server{jwtKey: key}

	w := httptest.NewRecorder()
	s.health(w, httptest.NewRequest("GET", "/api/health", nil))
	if got := strings.TrimSpace(w.Body.String()); w.Code != 200 || got != `{"ok":true}` {
		t.Fatalf("no token: status=%d body=%s want {\"ok\":true}", w.Code, got)
	}

	tok, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	s.health(w, r)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["version"] != BuildVersion || body["service"] != "artex" {
		t.Fatalf("valid token: body=%s want service and version", w.Body.String())
	}
}
