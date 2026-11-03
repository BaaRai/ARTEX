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

// Reaching the port is not enough to claim a fresh instance: authInit needs the
// setup token that initSetupToken printed to the server log.
func TestAuthInitRequiresSetupToken(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	if hash, _, err := m.pg.GetSetting(authPassKey); err != nil || hash != "" {
		t.Skipf("test database already has an admin password (err=%v); not touching it", err)
	}
	defer func() { _, _ = m.pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey) }()

	s := &Server{m: m, jwtKey: []byte(strings.Repeat("k", 32))}
	s.initSetupToken()
	if s.setupToken == "" {
		t.Fatal("no setup token issued while the password is unset")
	}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", strings.NewReader(body)))
		return w
	}

	for name, body := range map[string]string{
		"missing token": `{"password":"correct horse battery"}`,
		"wrong token":   `{"password":"correct horse battery","setup_token":"nope"}`,
	} {
		if w := post(body); w.Code != 403 || strings.Contains(w.Body.String(), `"token"`) {
			t.Fatalf("%s: status=%d body=%s, want 403 without a token", name, w.Code, w.Body.String())
		}
	}
	if hash, _, _ := m.pg.GetSetting(authPassKey); hash != "" {
		t.Fatal("a rejected init must not set the password")
	}

	w := post(`{"password":"correct horse battery","setup_token":"` + s.setupToken + `"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("with the setup token: status=%d body=%s, want 200 with a token", w.Code, w.Body.String())
	}

	// Once a password exists, a restart issues no token.
	s2 := &Server{m: m}
	s2.initSetupToken()
	if s2.setupToken != "" {
		t.Fatal("setup token issued although a password is set")
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
