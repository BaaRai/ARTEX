package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveUA spins up an httptest server that records the User-Agent of the request
// it receives, drives one request through a quotaAwareHTTPClient built with the
// given userAgent, and returns what actually reached the wire.
func serveUA(t *testing.T, userAgent string) string {
	t.Helper()
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := quotaAwareHTTPClient("", "", userAgent)
	if err != nil {
		t.Fatalf("quotaAwareHTTPClient: %v", err)
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	return <-got
}

func TestUserAgentHeaderSetWhenConfigured(t *testing.T) {
	const want = "artex-test-agent/1.2.3"
	if got := serveUA(t, want); got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestUserAgentHeaderDefaultWhenEmpty(t *testing.T) {
	// Empty = no override: the Go client's default UA must reach the server unchanged.
	got := serveUA(t, "")
	if !strings.HasPrefix(got, "Go-http-client/") {
		t.Fatalf("User-Agent = %q, want the default Go-http-client UA when unset", got)
	}
}

func TestValidateUserAgentRejectsNewline(t *testing.T) {
	if _, err := ValidateUserAgent("good/1.0\r\nX-Injected: 1"); err == nil {
		t.Fatalf("expected a newline-containing user agent to be rejected")
	}
}

func TestValidateUserAgentRejectsTooLong(t *testing.T) {
	if _, err := ValidateUserAgent(strings.Repeat("a", MaxUserAgentLen+1)); err == nil {
		t.Fatalf("expected a user agent longer than %d to be rejected", MaxUserAgentLen)
	}
}

func TestValidateUserAgentTrimsAndAllowsEmpty(t *testing.T) {
	if ua, err := ValidateUserAgent("   "); err != nil || ua != "" {
		t.Fatalf("blank user agent: got (%q, %v), want (\"\", nil)", ua, err)
	}
	if ua, err := ValidateUserAgent("  my-app/1.0  "); err != nil || ua != "my-app/1.0" {
		t.Fatalf("trim: got (%q, %v), want (\"my-app/1.0\", nil)", ua, err)
	}
}
