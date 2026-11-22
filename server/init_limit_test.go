package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// newInitTestServer builds a server with a fresh password key pair and an active
// setup token, so the handler reaches the limiter and the token comparison
// instead of failing on missing state.
func newInitTestServer(t *testing.T) (*Server, *Manager) {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	if m.pg == nil {
		db.SkipNoPG(t)
	}
	// Cleanups run last-in-first-out: m.Close is registered first so it runs
	// last, after the server is stopped and the rows are gone.
	t.Cleanup(func() { _ = m.Close() })
	td := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, td, td, td)
	t.Cleanup(func() { stopTestServer(t, s, cancel) })

	stamp := time.Now().UnixNano()
	s.authHashKey = fmt.Sprintf("test.init_hash.%d", stamp)
	s.authVersionKey = fmt.Sprintf("test.init_version.%d", stamp)
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM settings WHERE key=$1 OR key=$2`, s.authHashKey, s.authVersionKey)
	})
	s.setupMu.Lock()
	s.setupToken = "setup-ok"
	s.setupMu.Unlock()
	return s, m
}

func initRequest(remote, body string) *http.Request {
	r := httptest.NewRequest("POST", "/api/auth/init", strings.NewReader(body))
	r.RemoteAddr = remote
	return r
}

// POST /api/auth/init pays a bcrypt hash on every request while no password is
// set, and it sits outside the login limiter. It now has its own per-IP bucket.
func TestAuthInitIsRateLimited(t *testing.T) {
	s, _ := newInitTestServer(t)

	post := func(remote string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.authInit(w, initRequest(remote, `{"password":"correct-horse-1","setup_token":"wrong"}`))
		return w
	}

	// Wrong token: each request hashes and returns 403, filling the bucket.
	const remote = "192.0.2.77:40000"
	for i := 0; i < loginMaxAttempts; i++ {
		if w := post(remote); w.Code != 403 {
			t.Fatalf("attempt %d: status=%d want 403", i+1, w.Code)
		}
	}
	w := post(remote)
	if w.Code != 429 {
		t.Fatalf("after %d attempts: status=%d want 429", loginMaxAttempts, w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
	// Another address still gets through.
	if w := post("192.0.2.78:40000"); w.Code != 403 {
		t.Fatalf("other IP: status=%d want 403", w.Code)
	}
}

// A successful setup clears the bucket, so the attempt that finally worked does
// not leave the operator throttled.
func TestAuthInitSuccessResetsLimit(t *testing.T) {
	s, _ := newInitTestServer(t)

	const remote = "192.0.2.79:40000"
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.authInit(w, initRequest(remote, body))
		return w
	}
	for i := 0; i < loginMaxAttempts-1; i++ {
		if w := post(`{"password":"correct-horse-1","setup_token":"wrong"}`); w.Code != 403 {
			t.Fatalf("filler %d: status=%d want 403", i+1, w.Code)
		}
	}
	w := post(`{"password":"correct-horse-1","setup_token":"setup-ok"}`)
	if w.Code != 200 {
		t.Fatalf("setup: status=%d body=%s want 200", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Token == "" {
		t.Fatalf("no token in %s", w.Body.String())
	}
	// The bucket is cleared; the next request is refused because the password is
	// now set, not because it is throttled.
	if w := post(`{"password":"correct-horse-1","setup_token":"setup-ok"}`); w.Code != 403 {
		t.Fatalf("after success: status=%d want 403", w.Code)
	}
}
