package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// frontProxy spins the proxy pointed at the given upstream and returns a test
// server fronting it, plus a cleanup.
func frontProxy(t *testing.T, upstream *httptest.Server, token string) *httptest.Server {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	// Front the production handler chain (logRequests wraps the proxy in run),
	// not the bare proxy, so these tests exercise statusRecorder too — e.g. its
	// Flush delegation, which unbuffered SSE streaming depends on in production.
	front := httptest.NewServer(logRequests(newProxy(u, token)))
	t.Cleanup(front.Close)
	return front
}

func TestProxy_AuthRewritten(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Auth", r.Header.Get("Authorization"))
		w.Header().Set("X-Seen-ApiKey", r.Header.Get("X-Api-Key"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	front := frontProxy(t, upstream, "tok")

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("X-Api-Key", "sk-client")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Seen-Auth"); got != "Bearer tok" {
		t.Errorf("upstream Authorization = %q, want %q", got, "Bearer tok")
	}
	if got := resp.Header.Get("X-Seen-ApiKey"); got != "" {
		t.Errorf("upstream X-Api-Key = %q, want absent", got)
	}
}

func TestProxy_BetaMerged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Beta", r.Header.Get("Anthropic-Beta"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	front := frontProxy(t, upstream, "tok")

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Anthropic-Beta", "prompt-caching-2024-07-31")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	got := resp.Header.Get("X-Seen-Beta")
	if !strings.Contains(got, "prompt-caching-2024-07-31") {
		t.Errorf("upstream Anthropic-Beta = %q, missing client beta", got)
	}
	if !strings.Contains(got, oauthBeta) {
		t.Errorf("upstream Anthropic-Beta = %q, missing oauth beta %q", got, oauthBeta)
	}
}

func TestProxy_IdentityInjected(t *testing.T) {
	// upstream echoes the request body it received back to the client.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body)
	}))
	defer upstream.Close()

	front := frontProxy(t, upstream, "tok")

	resp, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader(`{"system":"hi"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	seen, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(seen, &m); err != nil {
		t.Fatalf("upstream body not JSON: %v (body=%q)", err, seen)
	}
	sys, ok := m["system"].(string)
	if !ok {
		t.Fatalf("system is not a string: %T", m["system"])
	}
	if !strings.HasPrefix(sys, ClaudeCodeIdentity) {
		t.Errorf("system = %q, want prefix %q", sys, ClaudeCodeIdentity)
	}
	if !strings.Contains(sys, "hi") {
		t.Errorf("system = %q, want original %q preserved", sys, "hi")
	}
}

func TestProxy_NonJSONBodyForwardedIntact(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body)
	}))
	defer upstream.Close()

	front := frontProxy(t, upstream, "tok")

	const raw = "not json"
	resp, err := http.Post(front.URL+"/v1/messages", "text/plain", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	seen, _ := io.ReadAll(resp.Body)
	if string(seen) != raw {
		t.Errorf("upstream body = %q, want byte-for-byte %q", seen, raw)
	}
}

func TestProxy_StreamingPassthrough(t *testing.T) {
	chunks := []string{"data: a\n\n", "data: b\n\n", "data: c\n\n"}
	// proceed gates the upstream: it sends a chunk, flushes, then blocks until
	// the client confirms receipt. If the proxy buffered the whole response the
	// first client read would deadlock, so passing proves incremental delivery.
	proceed := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream ResponseWriter is not a Flusher")
			return
		}
		for _, c := range chunks {
			io.WriteString(w, c)
			fl.Flush()
			<-proceed
		}
	}))
	defer upstream.Close()

	front := frontProxy(t, upstream, "tok")

	resp, err := http.Get(front.URL + "/v1/messages")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	for i, want := range chunks {
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			t.Fatalf("read chunk %d: %v", i, err)
		}
		if string(buf) != want {
			t.Fatalf("chunk %d = %q, want %q", i, buf, want)
		}
		proceed <- struct{}{}
	}
}

func TestProxy_429PassedThrough(t *testing.T) {
	const body = `{"type":"error","error":{"type":"rate_limit_error"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, body)
	}))
	defer upstream.Close()

	front := frontProxy(t, upstream, "tok")

	resp, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != body {
		t.Errorf("body = %q, want %q passed through unmodified", got, body)
	}
}

func TestProxy_502OnUpstreamDown(t *testing.T) {
	// Build a server to grab a real address, then close it so connections refuse.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	u, err := url.Parse(dead.URL)
	if err != nil {
		t.Fatalf("parse dead URL: %v", err)
	}
	dead.Close()

	front := httptest.NewServer(newProxy(u, "supersecret-tok"))
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	got, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(got), "supersecret-tok") {
		t.Errorf("502 body leaked token: %q", got)
	}
	if !strings.Contains(string(got), "upstream unreachable") {
		t.Errorf("502 body = %q, want upstream-unreachable message", got)
	}
}

func TestProxy_TokenNotLogged(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	// Exercise the request logger too, since that is the main log path in run.
	front := httptest.NewServer(logRequests(newProxy(u, "supersecret-tok")))
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader(`{"system":"hi"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	// Close blocks until outstanding handlers return, so logRequests' trailing
	// log.Printf has completed before buf is read (no concurrent access).
	front.Close()

	if strings.Contains(buf.String(), "supersecret-tok") {
		t.Errorf("log leaked token: %q", buf.String())
	}
}

func TestLoadToken_Missing(t *testing.T) {
	t.Setenv("ARTEX_SHIM_TOKEN", "")

	if _, err := loadToken(""); err == nil {
		t.Fatalf("loadToken(\"\") = nil error, want error")
	}
}

func TestLoadToken_FileWins(t *testing.T) {
	t.Setenv("ARTEX_SHIM_TOKEN", "envtok")

	dir := t.TempDir()
	path := dir + "/token"
	if err := os.WriteFile(path, []byte("  filetok\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	got, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken(%q): %v", path, err)
	}
	if got != "filetok" {
		t.Errorf("loadToken = %q, want trimmed file contents %q", got, "filetok")
	}
}
