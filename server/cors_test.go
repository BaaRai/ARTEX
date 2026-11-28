package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embedded console is served from the same origin as /api, so only that
// origin may receive CORS headers or a preflight answer. The old wildcard was
// not exploitable on its own, but it advertised cross-origin access on an
// authenticated API.
func TestCorsOnlyAllowsSameOrigin(t *testing.T) {
	reached := false
	h := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(200)
	}))

	cases := []struct {
		name      string
		origin    string
		fetchSite string
		wantAllow string
	}{
		{"same origin", "http://example.com", "", "http://example.com"},
		{"same origin with default port", "http://EXAMPLE.com:80", "", "http://EXAMPLE.com:80"},
		{"foreign origin", "https://evil.example", "", ""},
		{"no origin but same-site fetch", "", "same-origin", ""},
		{"no origin", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			r := httptest.NewRequest(http.MethodOptions, "/api/tasks", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.wantAllow {
				t.Fatalf("Allow-Origin = %q, want %q", got, tc.wantAllow)
			}
			if tc.wantAllow != "" && !strings.Contains(w.Header().Get("Vary"), "Origin") {
				t.Errorf("Vary = %q, want Origin", w.Header().Get("Vary"))
			}
			if tc.wantAllow != "" {
				if w.Code != http.StatusNoContent {
					t.Errorf("preflight status = %d, want 204", w.Code)
				}
				if reached {
					t.Error("preflight was forwarded to the API handler")
				}
			} else {
				if w.Code == http.StatusNoContent {
					t.Error("foreign preflight was answered without auth")
				}
				if !reached {
					t.Error("request was not forwarded to the API handler")
				}
			}
		})
	}
}

func TestCorsGetForwardsWithoutWildcard(t *testing.T) {
	h := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want empty", got)
	}
}
