package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

func TestClassifyHumanCheck(t *testing.T) {
	long := strings.Repeat("search result text ", 120) // > 1500 chars
	cases := []struct {
		name, url    string
		frames       []string
		widget, text string
		want         string
	}{
		{"recaptcha frame", "https://shop.example/login", []string{"https://shop.example/login", "https://www.google.com/recaptcha/api2/anchor?k=x"}, "", "Sign in", "reCAPTCHA"},
		{"hcaptcha frame", "https://a.example/", []string{"https://newassets.hcaptcha.com/captcha/v1/x"}, "", "", "hCaptcha"},
		{"turnstile frame", "https://a.example/", []string{"https://challenges.cloudflare.com/cdn-cgi/challenge-platform/x"}, "", "", "Cloudflare Turnstile"},
		{"google sorry page", "https://www.google.com/sorry/index?continue=x", nil, "", "", "Google unusual-traffic check"},
		{"dom widget", "https://a.example/", nil, "PerimeterX", "", "PerimeterX"},
		{"short challenge wording", "https://a.example/", nil, "", "Checking your browser before accessing a.example", "human verification page"},
		{"robot wording", "https://a.example/", nil, "", "Are you a robot? Please confirm.", "human verification page"},
		{"long page quoting wording is not a check", "https://search.example/?q=captcha", nil, "", long + " verify you are human", ""},
		{"plain page", "https://a.example/", []string{"https://a.example/", "https://ads.example/frame"}, "", "Welcome back", ""},
	}
	for _, c := range cases {
		if got := classifyHumanCheck(c.url, c.frames, c.widget, c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSharedBrowserMCPArgs(t *testing.T) {
	in := []string{"--headless", "--browser", "chromium", "--proxy-server", "http://127.0.0.1:8788",
		"--isolated", "--user-data-dir=/tmp/x", "--caps", "vision", "--cdp-endpoint", "http://old", "--storage-state", "s.json"}
	got := sharedBrowserMCPArgs(in, "http://127.0.0.1:9222")
	want := []string{"--browser", "chromium", "--caps", "vision", "--cdp-endpoint", "http://127.0.0.1:9222"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if len(in) != 13 {
		t.Fatalf("input slice was modified")
	}
}

func TestBrowserMCPProxy(t *testing.T) {
	p, insecure := browserMCPProxy([]string{"--headless", "--proxy-server", "http://127.0.0.1:8788"}, map[string]string{"NODE_EXTRA_CA_CERTS": "/app/data/ca.pem"})
	if p != "http://127.0.0.1:8788" || !insecure {
		t.Fatalf("capture proxy: got %q %v", p, insecure)
	}
	p, insecure = browserMCPProxy([]string{"--proxy-server=socks5://10.0.0.1:1080"}, map[string]string{})
	if p != "socks5://10.0.0.1:1080" || insecure {
		t.Fatalf("global proxy: got %q %v", p, insecure)
	}
	if p, insecure = browserMCPProxy([]string{"--headless"}, nil); p != "" || insecure {
		t.Fatalf("no proxy: got %q %v", p, insecure)
	}
}

func TestIsSharedBrowserMCP(t *testing.T) {
	row := func(name, transport, cmd string, args ...string) *db.MCPServer {
		raw, _ := json.Marshal(args)
		return &db.MCPServer{Name: name, Transport: transport, Command: cmd, Args: raw}
	}
	if !isSharedBrowserMCP(row("browser", "stdio", "playwright-mcp", "--headless")) {
		t.Error("seeded row not recognized")
	}
	if !isSharedBrowserMCP(row("browser", "stdio", "npx", "-y", "@playwright/mcp@latest")) {
		t.Error("npx row not recognized")
	}
	if isSharedBrowserMCP(row("browser", "stdio", "my-own-browser-mcp")) {
		t.Error("a custom browser MCP must be left alone")
	}
	if isSharedBrowserMCP(row("other", "stdio", "playwright-mcp")) {
		t.Error("only the seeded name is taken over")
	}
	if isSharedBrowserMCP(row("browser", "http", "playwright-mcp")) {
		t.Error("only stdio rows are taken over")
	}
}

func TestBrowserToolIsReadOnly(t *testing.T) {
	for _, n := range []string{"mcp__browser__browser_snapshot", "browser_take_screenshot", "mcp__browser__browser_network_requests"} {
		if !browserToolIsReadOnly(n) {
			t.Errorf("%s should be read-only", n)
		}
	}
	for _, n := range []string{"mcp__browser__browser_click", "browser_type", "browser_navigate", "browser_evaluate", "browser_run_code", "browser_file_upload"} {
		if browserToolIsReadOnly(n) {
			t.Errorf("%s acts on the page", n)
		}
	}
}

func TestNormalizeBrowserURL(t *testing.T) {
	cases := map[string]string{
		"example.com/login":      "https://example.com/login",
		"localhost:3000":         "http://localhost:3000",
		"127.0.0.1:8080/x":       "http://127.0.0.1:8080/x",
		"http://plain.example":   "http://plain.example",
		"about:blank":            "about:blank",
		"  ":                     "",
		"https://a.example/?q=1": "https://a.example/?q=1",
	}
	for in, want := range cases {
		if got := normalizeBrowserURL(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestBrowserRunHolder(t *testing.T) {
	k, _ := browserRunHolder(agent.RunInfo{TaskID: 7, IntentID: 42}, "worker")
	k2, _ := browserRunHolder(agent.RunInfo{TaskID: 7, IntentID: 42}, "pentest")
	if k != "t7/i42" || k != k2 {
		t.Fatalf("intent holder must not depend on the agent key (ask_operator uses it): %q %q", k, k2)
	}
	a, _ := browserRunHolder(agent.RunInfo{}, "chat")
	b, _ := browserRunHolder(agent.RunInfo{}, "chat")
	if a == b {
		t.Fatal("runs without an identity must get distinct holders")
	}
}

// fakeRunning marks a sharedBrowser as running without a process, for the
// arbitration tests (no tabs, so no DevTools call is made).
func fakeRunning() *sharedBrowser {
	b := newSharedBrowser(context.Background(), sharedBrowserConfig{})
	b.conn = &cdpConn{pending: map[int64]chan cdpReply{}, done: make(chan struct{})}
	return b
}

func TestAgentGateLeaseAndOperator(t *testing.T) {
	ctx := context.Background()
	b := fakeRunning()
	if r := b.AgentGate(ctx, "t1/i1", "task 1 · intent 1", "browser_click"); r != "" {
		t.Fatalf("first run should get the browser: %s", r)
	}
	if r := b.AgentGate(ctx, "t1/i2", "task 1 · intent 2", "browser_snapshot"); !strings.Contains(r, "in use by another agent run") {
		t.Fatalf("second run must be told the browser is busy, got %q", r)
	}
	// The lease expires after the idle window.
	b.mu.Lock()
	b.lease.last = time.Now().Add(-2 * sharedBrowserLeaseIdle)
	b.mu.Unlock()
	if r := b.AgentGate(ctx, "t1/i2", "task 1 · intent 2", "browser_click"); r != "" {
		t.Fatalf("idle lease should pass to the next run: %s", r)
	}
	// A finished run releases the lease at once.
	b.AgentDone("t1/i2")
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_click"); r != "" {
		t.Fatalf("released lease: %s", r)
	}
	// Operator control refuses every agent call, reading ones included.
	b.SetOperator(true)
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_snapshot"); !strings.Contains(r, "taken over") {
		t.Fatalf("operator control must refuse agents, got %q", r)
	}
	b.SetOperator(false)
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_click"); r != "" {
		t.Fatalf("hand back should let the agent continue: %s", r)
	}
}

func TestAskOperatorHoldsTheBrowser(t *testing.T) {
	ctx := context.Background()
	b := fakeRunning()
	_ = b.AgentGate(ctx, "t1/i1", "x", "browser_click")
	b.NoteAsk("t1/i1")
	b.AgentDone("t1/i1") // ask_operator pauses the intent: the run ends
	b.mu.Lock()
	b.lease.last = time.Now().Add(-2 * sharedBrowserLeaseIdle)
	b.mu.Unlock()
	if r := b.AgentGate(ctx, "t1/i9", "y", "browser_navigate"); !strings.Contains(r, "in use") {
		t.Fatalf("another run must not take the page while the asker waits, got %q", r)
	}
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_snapshot"); r != "" {
		t.Fatalf("the resumed asker keeps the browser: %s", r)
	}
}

func TestCaptchaBlocksActingToolsUntilAsked(t *testing.T) {
	ctx := context.Background()
	b := fakeRunning()
	since := time.Now()
	// A detected check on a tab; scanCaptchas skips it (no tabs listed), so the
	// cached entry stands, as between two scans.
	b.captcha["tab1"] = sbCaptcha{Kind: "hCaptcha", URL: "https://a.example/login", Since: since}
	r := b.AgentGate(ctx, "t1/i1", "x", "browser_click")
	if !strings.Contains(r, "hCaptcha") || !strings.Contains(r, "ask_operator") {
		t.Fatalf("acting tool must be refused with a handoff instruction, got %q", r)
	}
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_take_screenshot"); r != "" {
		t.Fatalf("reading tools stay allowed for the screenshot ask_operator needs: %s", r)
	}
	time.Sleep(2 * time.Millisecond)
	b.NoteAsk("t1/i1")
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_click"); r != "" {
		t.Fatalf("after asking the operator the run may continue: %s", r)
	}
}

// TestSharedBrowserLive drives a real Chromium. It runs only when a browser is
// configured: ARTEX_BROWSER_EXECUTABLE, or ARTEX_TEST_SHARED_BROWSER=1 with a
// Playwright Chromium installed.
func TestSharedBrowserLive(t *testing.T) {
	if os.Getenv("ARTEX_BROWSER_EXECUTABLE") == "" && os.Getenv("ARTEX_TEST_SHARED_BROWSER") != "1" {
		t.Skip("set ARTEX_BROWSER_EXECUTABLE (or ARTEX_TEST_SHARED_BROWSER=1) to run against a real browser")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	b := newSharedBrowser(ctx, sharedBrowserConfig{ProfileDir: filepath.Join(t.TempDir(), "profile")})
	defer b.Stop()
	ep, err := b.Ensure(ctx, "", false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !strings.HasPrefix(ep, "http://127.0.0.1:") {
		t.Fatalf("endpoint %q", ep)
	}
	b.mu.Lock()
	ua := b.userAgent
	b.mu.Unlock()
	if strings.Contains(ua, "HeadlessChrome") {
		t.Fatalf("user agent still carries the headless token: %q", ua)
	}

	events, unsub := b.Subscribe()
	defer unsub()

	// A page with a text field, and a short "verify you are human" interstitial.
	page := "data:text/html," + strings.ReplaceAll(`<html><body style="margin:0"><input id="f" style="position:absolute;left:0;top:0;width:400px;height:60px"><p>Checking your browser before accessing the site</p></body></html>`, " ", "%20")
	if err := b.Navigate(ctx, "new_tab", page); err != nil {
		t.Fatalf("new tab: %v", err)
	}
	b.SetOperator(false) // Navigate takes control; hand it back for the agent checks below

	// A frame reaches the live view.
	deadline := time.After(20 * time.Second)
	gotFrame := false
	for !gotFrame {
		select {
		case ev := <-events:
			var m struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if json.Unmarshal(ev, &m) == nil && m.Type == "frame" && len(m.Data) > 100 {
				gotFrame = true
			}
		case <-deadline:
			t.Fatal("no screencast frame within 20s")
		}
	}

	// navigator.webdriver is false: the automation tell is gone.
	sid, conn, _, _, err := b.viewedSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := conn.Call(ctx, sid, "Runtime.evaluate", map[string]any{"expression": "String(navigator.webdriver)", "returnByValue": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"false"`) {
		t.Fatalf("navigator.webdriver: %s", raw)
	}

	// The human check is detected and acting tools are refused.
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_click"); !strings.Contains(r, "human verification page") {
		t.Fatalf("challenge not detected: %q", r)
	}

	// Operator input reaches the page: click the field, type, read it back.
	in := []sbInput{
		{Kind: "mouse", Type: "mousePressed", X: 0.05, Y: 0.03, Button: "left", ClickCount: 1},
		{Kind: "mouse", Type: "mouseReleased", X: 0.05, Y: 0.03, Button: "left", ClickCount: 1},
		{Kind: "text", Text: "otp-123456"},
	}
	if err := b.Input(ctx, in); err != nil {
		t.Fatalf("input: %v", err)
	}
	if !b.Status().Operator {
		t.Fatal("input must give the operator control")
	}
	raw, err = conn.Call(ctx, sid, "Runtime.evaluate", map[string]any{"expression": "document.getElementById('f').value", "returnByValue": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "otp-123456") {
		t.Fatalf("typed text did not arrive: %s", raw)
	}
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_snapshot"); !strings.Contains(r, "taken over") {
		t.Fatalf("agent must wait while the operator types: %q", r)
	}

	// The profile persists: a restart reuses it.
	b.Stop()
	if _, err := b.Ensure(ctx, "", false); err != nil {
		t.Fatalf("restart: %v", err)
	}
}
