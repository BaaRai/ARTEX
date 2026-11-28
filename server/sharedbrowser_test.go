package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/gorilla/websocket"
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
	got := sharedBrowserMCPArgs(in, "ws://127.0.0.1:9222/cdp/tok/devtools/browser")
	want := []string{"--browser", "chromium", "--caps", "vision", "--cdp-endpoint", "ws://127.0.0.1:9222/cdp/tok/devtools/browser"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if len(in) != 13 {
		t.Fatalf("input slice was modified")
	}
}

func TestBrowserMCPProxy(t *testing.T) {
	p, ca := browserMCPProxy([]string{"--headless", "--proxy-server", "http://127.0.0.1:8788"}, map[string]string{"NODE_EXTRA_CA_CERTS": "/app/data/ca.pem"})
	if p != "http://127.0.0.1:8788" || ca != "/app/data/ca.pem" {
		t.Fatalf("capture proxy: got %q %q", p, ca)
	}
	p, ca = browserMCPProxy([]string{"--proxy-server=socks5://10.0.0.1:1080"}, map[string]string{})
	if p != "socks5://10.0.0.1:1080" || ca != "" {
		t.Fatalf("global proxy: got %q %q", p, ca)
	}
	if p, ca = browserMCPProxy([]string{"--headless"}, nil); p != "" || ca != "" {
		t.Fatalf("no proxy: got %q %q", p, ca)
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

// TestSharedBrowserOptInDefaultOff verifies that the shared browser is off by
// default unless explicitly enabled.
func TestSharedBrowserOptInDefaultOff(t *testing.T) {
	s := &Server{}
	if s.sharedBrowserOn || s.browser != nil {
		t.Fatalf("shared browser must default to off")
	}
	w := httptest.NewRecorder()
	s.browserStatus(w, httptest.NewRequest(http.MethodGet, "/api/browser/status", nil))
	if w.Code != 200 {
		t.Fatalf("status HTTP %d", w.Code)
	}
	var st SharedBrowserStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Enabled || st.Running {
		t.Fatalf("unexpected default status: %+v err=%v", st, err)
	}
	w2 := httptest.NewRecorder()
	s.browserStart(w2, httptest.NewRequest(http.MethodPost, "/api/browser/start", nil))
	if w2.Code != 409 || !strings.Contains(w2.Body.String(), "opt-in") {
		t.Fatalf("disabled start: code=%d body=%s", w2.Code, w2.Body.String())
	}
}

// TestSharedBrowserArgsHardening verifies:
//   - Linux uses --remote-debugging-pipe (never --remote-debugging-port)
//   - --ignore-certificate-errors is never emitted
//   - --disable-blink-features=AutomationControlled and --user-agent are never emitted
//   - Capture CA uses --ignore-certificate-errors-spki-list pinned to the CA PEM
func TestSharedBrowserArgsHardening(t *testing.T) {
	// Generate a real test CA certificate PEM and compute its SPKI pin.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ARTEX Test Capture CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	spki, err := certSPKIHash(caPath)
	if err != nil || spki == "" {
		t.Fatalf("certSPKIHash: %q err=%v", spki, err)
	}
	if _, err := certSPKIHash(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing CA file must return an error")
	}

	args := sharedBrowserArgs("/data/browser-profiles/task-7", "http://127.0.0.1:8788", spki, "linux")
	hasPipe := false
	hasSPKI := false
	for _, a := range args {
		if a == "--remote-debugging-pipe" {
			hasPipe = true
		}
		if a == "--ignore-certificate-errors-spki-list="+spki {
			hasSPKI = true
		}
		if a == "--ignore-certificate-errors" {
			t.Fatalf("forbidden --ignore-certificate-errors present in args: %v", args)
		}
		if strings.HasPrefix(a, "--remote-debugging-port") || strings.HasPrefix(a, "--remote-debugging-address") {
			t.Fatalf("Linux must not open a DevTools TCP port: %v", args)
		}
		if strings.Contains(a, "AutomationControlled") || strings.HasPrefix(a, "--user-agent") {
			t.Fatalf("anti-bot stealth flag must not be emitted: %v", args)
		}
	}
	if !hasPipe {
		t.Fatalf("expected --remote-debugging-pipe on linux, got %v", args)
	}
	if !hasSPKI {
		t.Fatalf("expected --ignore-certificate-errors-spki-list=%s, got %v", spki, args)
	}
}

// TestSharedBrowserProfileIsolation verifies per-task and per-session profile
// directories are isolated and path traversal is neutralized.
func TestSharedBrowserProfileIsolation(t *testing.T) {
	root := t.TempDir()
	b := newSharedBrowser(context.Background(), sharedBrowserConfig{ProfileDir: root})
	sTask1 := browserProfileScope(agent.RunInfo{TaskID: 1, IntentID: 10})
	sTask2 := browserProfileScope(agent.RunInfo{TaskID: 2, IntentID: 10})
	sSessA := browserProfileScope(agent.RunInfo{SessionID: "sess-alpha"})
	sSessB := browserProfileScope(agent.RunInfo{SessionID: "sess-beta"})
	sEvil := browserProfileScope(agent.RunInfo{SessionID: "../../etc/passwd"})

	if sTask1 == sTask2 || sSessA == sSessB || sTask1 == sSessA {
		t.Fatalf("scopes must be distinct: %q %q %q %q", sTask1, sTask2, sSessA, sSessB)
	}
	dir1 := b.profileDirFor(sTask1)
	dir2 := b.profileDirFor(sTask2)
	dirEvil := b.profileDirFor(sEvil)
	if dir1 == dir2 {
		t.Fatalf("distinct tasks must not share a profile dir: %q", dir1)
	}
	rel, err := filepath.Rel(root, dirEvil)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		t.Fatalf("traversal scope escaped root %q -> %q (rel=%q)", root, dirEvil, rel)
	}
}

// fakeRunning marks a sharedBrowser as running without a process, for the
// arbitration tests (no tabs, so no DevTools call is made).
func fakeRunning() *sharedBrowser {
	return fakeRunningWithConfig(sharedBrowserConfig{})
}

func fakeRunningWithConfig(cfg sharedBrowserConfig) *sharedBrowser {
	b := newSharedBrowser(context.Background(), cfg)
	b.conn = &cdpConn{
		pending:   map[int64]chan cdpReply{},
		rawWaiter: map[int64]chan json.RawMessage{},
		evSubs:    map[chan json.RawMessage]struct{}{},
		done:      make(chan struct{}),
	}
	return b
}

// TestSharedBrowserCDPIsolationAndAuth verifies that the server-owned CDP proxy
// blocks unauthenticated requests (e.g. agent Bash probing localhost), allows
// authenticated per-run WebSocket RPC over pipe transport, blocks while the
// operator holds control, and rejects tokens immediately upon revocation.
func TestSharedBrowserCDPIsolationAndAuth(t *testing.T) {
	// Wire a real in-memory NUL-delimited --remote-debugging-pipe echo peer.
	c2pR, c2pW := io.Pipe() // browser writes -> cdpConn reads
	p2cR, p2cW := io.Pipe() // cdpConn writes -> fake browser reads
	go func() {
		defer c2pW.Close()
		tr := newPipeCDPTransport(c2pW, p2cR)
		for {
			raw, err := tr.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				continue
			}
			resp, _ := json.Marshal(map[string]any{
				"id":     req.ID,
				"result": map[string]any{"echo": req.Method},
			})
			if err := tr.WriteMessage(resp); err != nil {
				return
			}
		}
	}()

	b := newSharedBrowser(context.Background(), sharedBrowserConfig{})
	b.conn = newPipeCDP(p2cW, c2pR, nil)
	defer b.conn.Close()

	ep, revoke, err := b.IssueMCPEndpoint("t1/i1")
	if err != nil {
		t.Fatalf("IssueMCPEndpoint: %v", err)
	}
	defer b.cdpProxy.Close()

	baseHTTP := "http://" + b.cdpProxy.addr

	// 1. Unauthenticated Bash-style probes to /json/version, /json/list, /cdp/wrong -> 401.
	for _, path := range []string{"/json/version", "/json/list", "/cdp/badtoken/json/version"} {
		resp, err := http.Get(baseHTTP + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d, want 401", path, resp.StatusCode)
		}
	}

	// 2. Unauthenticated WebSocket dial -> 401.
	if _, resp, err := websocket.DefaultDialer.Dial("ws://"+b.cdpProxy.addr+"/cdp/badtoken/devtools/browser", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WS dial: err=%v resp=%v, want 401", err, resp)
	}

	// 3. Valid per-run token WebSocket RPC works and restores the client's original JSON-RPC ID.
	ws, _, err := websocket.DefaultDialer.Dial(ep, nil)
	if err != nil {
		t.Fatalf("authenticated WS dial %s: %v", ep, err)
	}
	if err := ws.WriteJSON(map[string]any{"id": 42, "method": "Browser.getVersion"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID     int64 `json:"id"`
		Result struct {
			Echo string `json:"echo"`
		} `json:"result"`
	}
	if err := ws.ReadJSON(&got); err != nil {
		t.Fatalf("read CDP reply: %v", err)
	}
	if got.ID != 42 || got.Result.Echo != "Browser.getVersion" {
		t.Fatalf("unexpected CDP reply: %+v", got)
	}
	_ = ws.Close()

	// 4. Operator take-over blocks even a valid token with 403 Forbidden.
	b.SetOperator(true)
	if _, resp, err := websocket.DefaultDialer.Dial(ep, nil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator-locked WS dial: err=%v resp=%v, want 403", err, resp)
	}
	b.SetOperator(false)

	// 5. Revoking the token immediately rejects subsequent connects with 401.
	revoke()
	if _, resp, err := websocket.DefaultDialer.Dial(ep, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked-token WS dial: err=%v resp=%v, want 401", err, resp)
	}
}

// TestSharedBrowserConfigurableOperatorLockTimeout verifies that custom
// OperatorLockTimeout governs both idle operator take-over expiry and
// ask_operator hold duration.
func TestSharedBrowserConfigurableOperatorLockTimeout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	b := fakeRunningWithConfig(sharedBrowserConfig{
		OperatorLockTimeout: 5 * time.Minute,
		Now:                 func() time.Time { return now },
	})

	// Operator takes control; blocked for 4m59s, automatically released at 5m.
	b.SetOperator(true)
	now = now.Add(4*time.Minute + 59*time.Second)
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_snapshot"); !strings.Contains(r, "taken over") {
		t.Fatalf("operator lock should still hold before timeout, got %q", r)
	}
	now = now.Add(2 * time.Second)
	if b.Status().Operator {
		t.Fatalf("operator lock should expire after configured 5m timeout")
	}
	if r := b.AgentGate(ctx, "t1/i1", "x", "browser_click"); r != "" {
		t.Fatalf("agent should resume once operator lock expires, got %q", r)
	}

	// ask_operator hold also uses configured 5m timeout (not hardcoded 15m).
	b.NoteAsk("t1/i1")
	b.AgentDone("t1/i1")
	now = now.Add(4 * time.Minute)
	if r := b.AgentGate(ctx, "t1/i2", "y", "browser_click"); !strings.Contains(r, "in use") {
		t.Fatalf("ask_operator hold should still protect the browser at 4m, got %q", r)
	}
	now = now.Add(2 * time.Minute) // total 6m > 5m configured timeout
	if r := b.AgentGate(ctx, "t1/i2", "y", "browser_click"); r != "" {
		t.Fatalf("ask_operator hold should expire after configured 5m timeout, got %q", r)
	}
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

// TestPrepareSharedBrowserProfileDirRefusesSymlink verifies that preparing an
// isolated profile refuses symlinks at root or scope (preventing symlink/TOCTOU
// chown or unlink redirection into sensitive directories like /app/keys) and
// never walks into child directories or symlinks inside the scope.
func TestPrepareSharedBrowserProfileDirRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	sensitive := filepath.Join(base, "fake-keys")
	if err := os.MkdirAll(sensitive, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sensitive, "SingletonLock")
	if err := os.WriteFile(sentinel, []byte("secret-key-material"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Symlinked profile root must be refused; target directory untouched.
	linkRoot := filepath.Join(base, "link-root")
	if err := os.Symlink(sensitive, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSharedBrowserProfileDir(linkRoot, "task-1", ExecPrivilege{}); err == nil {
		t.Fatal("symlinked profile root must be refused")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("symlinked root touched sensitive target: %v", err)
	}

	// 2. Symlinked scope directory (<root>/task-2 -> fake-keys) must be refused;
	//    SingletonLock inside fake-keys must NOT be unlinked.
	realRoot := filepath.Join(base, "browser-profiles")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sensitive, filepath.Join(realRoot, "task-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSharedBrowserProfileDir(realRoot, "task-2", ExecPrivilege{}); err == nil {
		t.Fatal("symlinked profile scope must be refused")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("symlinked scope unlinked or modified file in sensitive target: %v", err)
	}

	// 3. Normal scope with a planted child symlink (sub -> fake-keys) and a real
	//    stale lock file: succeeds, unlinks only the scope's own SingletonLock,
	//    and never walks into sub or touches fake-keys.
	scopeDir, err := prepareSharedBrowserProfileDir(realRoot, "task-3", ExecPrivilege{})
	if err != nil {
		t.Fatalf("prepare valid scope: %v", err)
	}
	stale := filepath.Join(scopeDir, "SingletonLock")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sensitive, filepath.Join(scopeDir, "Default")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSharedBrowserProfileDir(realRoot, "task-3", ExecPrivilege{}); err != nil {
		t.Fatalf("re-prepare scope with child symlink: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale SingletonLock inside scope to be removed, stat err=%v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "secret-key-material" {
		t.Fatalf("sensitive target was modified through child symlink: data=%q err=%v", data, err)
	}
}
