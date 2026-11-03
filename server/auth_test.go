package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// GetSetting distinguishes "key absent" from "read failed" only through its error:
// both return an empty value. If a password handler mistakes the error for "no
// password set yet", a database blip opens the init door — authInit would let an
// unauthenticated request overwrite the admin password, and authStatus ships the frontend to /setup to do the same.
//
// Both tests close the handlers' connection pool to force a read failure and assert both fail closed.
func TestAuthStatusFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	// Close the pool so subsequent GetSetting returns an error instead of sql.ErrNoRows.
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	s.authStatus(w, httptest.NewRequest("GET", "/api/auth/status", nil))

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (treating a failed read as uninitialized sends the user to /setup to overwrite the password); body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Initialized *bool `json:"initialized"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err == nil && payload.Initialized != nil {
		t.Fatalf("a failed read must not answer initialized, got %v", *payload.Initialized)
	}
}

func TestAuthInitFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"password":"correct horse battery"}`)
	s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", body))

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (allowing through on a failed read lets an unauthenticated request overwrite the existing password); body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatalf("a failed read must not issue a token: %s", w.Body.String())
	}
}

// While no password is set, authInit must reject a missing or wrong setup token
// and accept the one generated at startup, once.
func TestAuthInitRequiresSetupToken(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	clearPassword := func() {
		if _, err := m.pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey); err != nil {
			t.Fatal(err)
		}
	}
	clearPassword()
	defer clearPassword()

	s := &Server{m: m, jwtKey: []byte("0123456789abcdef0123456789abcdef")}
	s.initSetupToken()
	setupToken := s.setupToken
	if setupToken == "" {
		t.Fatal("no setup token generated while no password is set")
	}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", strings.NewReader(body)))
		return w
	}

	for name, body := range map[string]string{
		"missing": `{"password":"correct horse battery"}`,
		"empty":   `{"password":"correct horse battery","setup_token":""}`,
		"wrong":   `{"password":"correct horse battery","setup_token":"0000"}`,
	} {
		if w := post(body); w.Code != 403 || strings.Contains(w.Body.String(), `"token"`) {
			t.Fatalf("%s setup token: status=%d want 403 without a token; body=%s", name, w.Code, w.Body.String())
		}
	}
	if hash, _, err := m.pg.GetSetting(authPassKey); err != nil || hash != "" {
		t.Fatalf("a rejected request changed the password state: hash set=%v err=%v", hash != "", err)
	}

	ok := `{"password":"correct horse battery","setup_token":"` + setupToken + `"}`
	if w := post(ok); w.Code != 200 || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("right setup token: status=%d want 200 with a token; body=%s", w.Code, w.Body.String())
	}
	if hash, _, err := m.pg.GetSetting(authPassKey); err != nil || hash == "" {
		t.Fatalf("password not stored: err=%v", err)
	}

	// The token is spent: even with the password row removed, it is refused.
	clearPassword()
	if w := post(ok); w.Code != 403 {
		t.Fatalf("reused setup token: status=%d want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		name, pw string
		wantErr  bool
	}{
		{"empty", "", true},
		{"seven chars", "1234567", true},
		{"eight chars", "12345678", false},
		{"eight multibyte chars counted as runes not bytes", strings.Repeat("€", 8), false},
		{"three chars reach 9 bytes but only 3 runes", strings.Repeat("€", 3), true},
		{"72 bytes", strings.Repeat("a", 72), false},
		{"73 bytes exceeds the bcrypt cap", strings.Repeat("a", 73), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePassword(tc.pw); (got != "") != tc.wantErr {
				t.Fatalf("validatePassword(%q)=%q, wantErr=%v", tc.pw, got, tc.wantErr)
			}
		})
	}
}
