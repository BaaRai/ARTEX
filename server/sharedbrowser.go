package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The shared browser.
//
// Before this, every agent run spawned its own playwright-mcp, which launched a
// private headless Chromium that nobody could see. A CAPTCHA, an email OTP page or
// a 2FA prompt stopped the run: the agent could only describe the page in
// ask_operator and hope the answer was typeable.
//
// Now ARTEX launches one persistent Chromium itself and points every browser MCP
// at it over CDP (--cdp-endpoint). The operator watches the same browser live in
// the web UI (Function → Browser), can take over to solve a CAPTCHA or sign in,
// and hands it back; the agent continues in the same tab with the same cookies.
//
// Security & policy:
//   - Opt-in only (-shared-browser / ARTEX_SHARED_BROWSER=true; default off).
//   - Chromium runs as the executor identity (never root) and uses
//     --remote-debugging-pipe on Unix so it binds no unauthenticated DevTools
//     TCP port and writes no DevToolsActivePort file for agent Bash to read.
//     Playwright MCP connects only through ARTEX's per-run token-authenticated
//     CDP proxy (sharedbrowser_cdp_proxy.go).
//   - Browser profiles are isolated per task or per chat session under
//     <data>/browser-profiles/<scope> so cookies and storage never leak across
//     tasks or sessions.
//   - With traffic capture on, Chromium trusts only the recording proxy CA SPKI
//     pin (--ignore-certificate-errors-spki-list) and never passes
//     --ignore-certificate-errors.
//   - Policy: ARTEX does not apply anti-bot stealth flags
//     (--disable-blink-features=AutomationControlled or HeadlessChrome UA
//     rewriting). Human verification challenges are detected and handed off to
//     the operator via ask_operator.

const (
	sharedBrowserWidth  = 1280
	sharedBrowserHeight = 800
	// sharedBrowserLeaseIdle is how long an agent run keeps the browser after its
	// last browser call. Another run that calls a browser tool within this window
	// is told the browser is busy; afterwards it takes the lease over.
	sharedBrowserLeaseIdle = 90 * time.Second
	// sharedBrowserProfileDir is the root directory under data holding isolated
	// per-task and per-session browser profiles (<data>/browser-profiles/<scope>).
	sharedBrowserProfileDir = "browser-profiles"
)

// sharedBrowserConfig is how the shared browser is launched.
type sharedBrowserConfig struct {
	// Executable is the Chromium binary. Empty: discovered at first launch.
	Executable string
	// ProfileDir is the root directory under which per-task / per-session
	// profiles are isolated.
	ProfileDir string
	Priv       ExecPrivilege
	// OperatorLockTimeout bounds how long an idle operator take-over or an
	// ask_operator hold keeps the browser before expiring automatically.
	// Zero defaults to DefaultSharedBrowserLockTimeout (15m).
	OperatorLockTimeout time.Duration
	// Now overrides the clock in tests; nil uses time.Now.
	Now func() time.Time
}

// sbPage is one open tab as the server tracks it.
type sbPage struct {
	ID      string    `json:"id"`
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	created time.Time // tab order
	changed time.Time // last navigation; the live view follows the newest change
}

// sbCaptcha is a human check detected on a tab.
type sbCaptcha struct {
	Kind  string    `json:"kind"`
	URL   string    `json:"url"`
	Since time.Time `json:"since"`
}

// sbLease is the agent run that currently drives the browser.
type sbLease struct {
	holder string
	label  string
	last   time.Time
	// hold keeps the lease for a run that asked the operator and is paused
	// waiting for the reply, so no other run navigates away from the page.
	hold time.Time
}

// activeLocked reports whether the lease still binds other runs.
func (l sbLease) activeLocked(now time.Time) bool {
	return l.holder != "" && (now.Sub(l.last) < sharedBrowserLeaseIdle || now.Before(l.hold))
}

// SharedBrowserStatus is the state the live view shows.
type SharedBrowserStatus struct {
	Enabled      bool       `json:"enabled"`
	Running      bool       `json:"running"`
	Error        string     `json:"error,omitempty"`
	ProfileScope string     `json:"profile_scope,omitempty"`
	Tabs         []sbPage   `json:"tabs"`
	Viewed       string     `json:"viewed"`
	Operator     bool       `json:"operator"` // the operator holds control
	Agent        string     `json:"agent"`    // label of the agent run holding the lease, when active
	Captcha      *sbCaptcha `json:"captcha,omitempty"`
	Width        int        `json:"width"`
	Height       int        `json:"height"`
}

// sharedBrowser owns the Chromium process, the DevTools connection, the
// authenticated CDP proxy, the live-view subscribers and arbitration.
type sharedBrowser struct {
	parent context.Context
	cfg    sharedBrowserConfig

	launchMu sync.Mutex // serializes Ensure/stop
	castMu   sync.Mutex // serializes refreshScreencast

	mu           sync.Mutex
	cmd          *exec.Cmd
	conn         *cdpConn
	cdpProxy     *cdpProxy
	profileScope string // isolated per-task / per-session scope
	proxy        string
	caSPKI       string // SHA-256 base64 SPKI pin of ARTEX's recording CA
	lastErr      string

	pages    map[string]*sbPage
	sessions map[string]string // target id → flatten session id
	viewed   string            // target id of the page the live view shows
	pinned   bool              // the operator picked the viewed tab; do not follow the agent
	castSess string            // session id the screencast runs on
	frameW   float64           // CSS size of the last frame (for input mapping)
	frameH   float64
	last     []byte // last frame event, sent to a new subscriber
	subs     map[chan []byte]struct{}

	operator      bool
	operatorSince time.Time
	lease         sbLease
	acks          map[string]time.Time // holder → time of its last ask_operator call
	captcha       map[string]sbCaptcha // target id → detected human check
}

func newSharedBrowser(parent context.Context, cfg sharedBrowserConfig) *sharedBrowser {
	return &sharedBrowser{
		parent:   parent,
		cfg:      cfg,
		pages:    map[string]*sbPage{},
		sessions: map[string]string{},
		subs:     map[chan []byte]struct{}{},
		acks:     map[string]time.Time{},
		captcha:  map[string]sbCaptcha{},
	}
}

func (b *sharedBrowser) now() time.Time {
	if b.cfg.Now != nil {
		return b.cfg.Now()
	}
	return time.Now()
}

func (b *sharedBrowser) operatorLockTimeout() time.Duration {
	if b.cfg.OperatorLockTimeout > 0 {
		return b.cfg.OperatorLockTimeout
	}
	return DefaultSharedBrowserLockTimeout
}

// operatorActiveLocked reports whether the operator currently holds control,
// automatically expiring an idle operator lock after operatorLockTimeout().
func (b *sharedBrowser) operatorActiveLocked(now time.Time) bool {
	if !b.operator {
		return false
	}
	if !b.operatorSince.IsZero() && now.Sub(b.operatorSince) >= b.operatorLockTimeout() {
		b.operator = false
		b.operatorSince = time.Time{}
		b.pinned = false
		return false
	}
	return true
}

// profileDirFor returns the isolated profile directory for one task or session.
func (b *sharedBrowser) profileDirFor(scope string) string {
	return filepath.Join(b.cfg.ProfileDir, sanitizeProfileScope(scope))
}

// certSPKIHash computes the base64 SHA-256 SubjectPublicKeyInfo hash of a PEM
// certificate for Chromium's --ignore-certificate-errors-spki-list flag.
func certSPKIHash(pemPath string) (string, error) {
	raw, err := os.ReadFile(pemPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", pemPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// ---------- launch ----------

// findChromium returns a Chromium binary: ARTEX_BROWSER_EXECUTABLE, else the newest
// Playwright-installed Chromium under PLAYWRIGHT_BROWSERS_PATH (the image installs
// it there), else a chromium/chrome on PATH.
func findChromium() (string, error) {
	if p := strings.TrimSpace(os.Getenv("ARTEX_BROWSER_EXECUTABLE")); p != "" {
		return p, nil
	}
	roots := []string{}
	if p := strings.TrimSpace(os.Getenv("PLAYWRIGHT_BROWSERS_PATH")); p != "" && p != "0" {
		roots = append(roots, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "windows":
			if la := os.Getenv("LOCALAPPDATA"); la != "" {
				roots = append(roots, filepath.Join(la, "ms-playwright"))
			}
		case "darwin":
			roots = append(roots, filepath.Join(home, "Library", "Caches", "ms-playwright"))
		default:
			roots = append(roots, filepath.Join(home, ".cache", "ms-playwright"))
		}
	}
	var rel []string
	switch runtime.GOOS {
	case "windows":
		rel = []string{filepath.Join("chrome-win64", "chrome.exe"), filepath.Join("chrome-win", "chrome.exe")}
	case "darwin":
		rel = []string{"chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
			"chrome-mac/Chromium.app/Contents/MacOS/Chromium"}
	default:
		rel = []string{filepath.Join("chrome-linux64", "chrome"), filepath.Join("chrome-linux", "chrome")}
	}
	type cand struct {
		rev  int
		path string
	}
	var cands []cand
	for _, root := range roots {
		dirs, _ := filepath.Glob(filepath.Join(root, "chromium-*"))
		for _, d := range dirs {
			rev, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(d), "chromium-"))
			if err != nil {
				continue // chromium_headless_shell-* and friends
			}
			for _, r := range rel {
				p := filepath.Join(d, r)
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
					cands = append(cands, cand{rev, p})
					break
				}
			}
		}
	}
	if len(cands) > 0 {
		sort.Slice(cands, func(i, j int) bool { return cands[i].rev > cands[j].rev })
		return cands[0].path, nil
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chromium found: set ARTEX_BROWSER_EXECUTABLE or install Playwright's chromium")
}

// sharedBrowserArgs builds the hardened Chromium command line:
//   - On Unix (linux/darwin/etc.), uses --remote-debugging-pipe (zero listening
//     TCP port so agent Bash cannot connect directly to Chromium).
//   - Never passes --ignore-certificate-errors; trusts only ARTEX's capture CA
//     SPKI pin (--ignore-certificate-errors-spki-list) when caSPKI is non-empty.
//   - Never passes anti-bot stealth flags (--disable-blink-features=AutomationControlled
//     or --user-agent HeadlessChrome rewriting).
func sharedBrowserArgs(profileDir, proxy, caSPKI, goos string) []string {
	args := []string{
		"--headless=new", // a container has no display; the live view uses screencasts
	}
	if goos == "windows" {
		args = append(args,
			"--remote-debugging-address=127.0.0.1",
			"--remote-debugging-port=0",
		)
	} else {
		args = append(args, "--remote-debugging-pipe")
	}
	args = append(args,
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic",
		"--disable-dev-shm-usage",
		fmt.Sprintf("--window-size=%d,%d", sharedBrowserWidth, sharedBrowserHeight),
	)
	if goos == "linux" {
		// The image has no setuid sandbox helper and no user namespaces for the
		// executor; Playwright's own launches pass the same flag.
		args = append(args, "--no-sandbox")
	}
	if proxy != "" {
		args = append(args, "--proxy-server="+proxy)
	}
	if caSPKI != "" {
		// Pin only ARTEX's recording-proxy CA public key; never disable TLS verification globally.
		args = append(args, "--ignore-certificate-errors-spki-list="+caSPKI)
	}
	return append(args, "about:blank")
}

// Ensure starts the browser for the given isolated profile scope (task or
// session), or restarts it when the scope, proxy, or capture CA pin changed.
func (b *sharedBrowser) Ensure(ctx context.Context, scope, proxy, caCertPath, holder string) error {
	scope = sanitizeProfileScope(scope)
	var caSPKI string
	if proxy != "" && caCertPath != "" {
		hash, err := certSPKIHash(caCertPath)
		if err != nil {
			// Fail closed: never fall back to --ignore-certificate-errors.
			log.Printf("[browser] capture CA %q unreadable (%v); refusing to disable TLS verification", caCertPath, err)
		} else {
			caSPKI = hash
		}
	}

	b.launchMu.Lock()
	defer b.launchMu.Unlock()
	b.mu.Lock()
	now := b.now()
	running := b.conn != nil
	same := b.profileScope == scope && b.proxy == proxy && b.caSPKI == caSPKI
	busyOther := running && !same && (b.operatorActiveLocked(now) || (b.lease.holder != "" && b.lease.holder != holder && b.lease.activeLocked(now)))
	otherLabel := b.lease.label
	b.mu.Unlock()
	if running && same {
		return nil
	}
	if busyOther {
		if otherLabel != "" {
			return fmt.Errorf("shared browser is in use by another run (%s) on a different profile", otherLabel)
		}
		return errors.New("shared browser is currently held by the human operator")
	}
	if running {
		log.Printf("[browser] profile/proxy changed (%s -> %s); restarting shared browser", b.profileScope, scope)
		b.stopLocked()
	}
	err := b.launch(ctx, scope, proxy, caSPKI)
	b.mu.Lock()
	if err != nil {
		b.lastErr = err.Error()
	} else {
		b.lastErr = ""
	}
	b.mu.Unlock()
	b.broadcastStatus()
	return err
}

// IssueMCPEndpoint ensures the authenticated server-side CDP proxy is running
// and mints a per-run bearer token endpoint for playwright-mcp.
func (b *sharedBrowser) IssueMCPEndpoint(holder string) (endpoint string, revoke func(), err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil {
		return "", nil, errors.New("shared browser is not running")
	}
	if b.cdpProxy == nil {
		p, err := newCDPProxy(b)
		if err != nil {
			return "", nil, err
		}
		b.cdpProxy = p
	}
	_, ep, rev := b.cdpProxy.IssueToken(holder)
	return ep, rev, nil
}

func (b *sharedBrowser) launch(ctx context.Context, scope, proxy, caSPKI string) error {
	exe := b.cfg.Executable
	if exe == "" {
		var err error
		if exe, err = findChromium(); err != nil {
			return err
		}
	}
	profileDir, err := prepareSharedBrowserProfileDir(b.cfg.ProfileDir, scope, b.cfg.Priv)
	if err != nil {
		return fmt.Errorf("browser profile: %w", err)
	}

	args := sharedBrowserArgs(profileDir, proxy, caSPKI, runtime.GOOS)
	cmd := exec.Command(exe, args...)
	cmd.Env = agentProcessEnv(b.cfg.Priv)
	applyExecCredential(cmd, b.cfg.Priv) // after Env: runs as the executor, HOME in its home
	cmd.Stdout = nil
	stderr, _ := cmd.StderrPipe()
	if stderr != nil {
		go drainBrowserLog(stderr)
	}

	conn, exited, err := b.startSharedBrowserCDP(ctx, cmd, profileDir)
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.cmd, b.conn, b.profileScope, b.proxy, b.caSPKI = cmd, conn, scope, proxy, caSPKI
	b.pages = map[string]*sbPage{}
	b.sessions = map[string]string{}
	b.captcha = map[string]sbCaptcha{}
	b.viewed, b.castSess, b.pinned = "", "", false
	b.mu.Unlock()

	if _, err := conn.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}); err != nil {
		b.stopLocked()
		return fmt.Errorf("watch browser tabs: %w", err)
	}
	go b.watch(cmd, conn, exited)
	go b.captchaLoop(conn)
	log.Printf("[browser] shared browser up (scope %s, profile %s, pipe=%v)", scope, profileDir, sharedBrowserUsesPipe)
	return nil
}

func drainBrowserLog(r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		// Chromium is chatty on stderr; keep the first lines for diagnosis only.
		if n < 20 {
			log.Printf("[browser] %s", sc.Text())
			n++
		}
	}
}

// waitDevToolsPort waits for Chromium to write DevToolsActivePort: the port on the
// first line, the browser websocket path on the second.
func waitDevToolsPort(ctx context.Context, profileDir string, exited <-chan struct{}) (string, string, error) {
	file := filepath.Join(profileDir, "DevToolsActivePort")
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if raw, err := os.ReadFile(file); err == nil {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) >= 2 {
				return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
			}
		}
		select {
		case <-tick.C:
		case <-exited:
			return "", "", errors.New("the browser exited during startup (see the [browser] log lines)")
		case <-deadline.C:
			return "", "", errors.New("the browser did not open its DevTools port within 20s")
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
}

// watch clears the state when the browser exits or the connection drops; the next
// agent browser call (Ensure) starts it again.
func (b *sharedBrowser) watch(cmd *exec.Cmd, conn *cdpConn, exited <-chan struct{}) {
	select {
	case <-exited:
	case <-conn.Done():
	case <-b.parent.Done():
	}
	conn.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	b.mu.Lock()
	var proxyToClose *cdpProxy
	if b.conn == conn {
		b.conn, b.cmd = nil, nil
		proxyToClose, b.cdpProxy = b.cdpProxy, nil
		b.pages = map[string]*sbPage{}
		b.sessions = map[string]string{}
		b.captcha = map[string]sbCaptcha{}
		b.viewed, b.castSess = "", ""
		b.operator = false
		b.operatorSince = time.Time{}
		if b.parent.Err() == nil {
			log.Printf("[browser] shared browser stopped")
		}
	}
	b.mu.Unlock()
	if proxyToClose != nil {
		proxyToClose.Close()
	}
	b.broadcastStatus()
}

func (b *sharedBrowser) stopLocked() {
	b.mu.Lock()
	conn, cmd, prx := b.conn, b.cmd, b.cdpProxy
	b.conn, b.cmd, b.cdpProxy = nil, nil, nil
	b.mu.Unlock()
	if prx != nil {
		prx.Close()
	}
	if conn != nil {
		_, _ = conn.Call(context.Background(), "", "Browser.close", nil)
		conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// Stop closes the browser; the profile stays on disk.
func (b *sharedBrowser) Stop() {
	b.launchMu.Lock()
	defer b.launchMu.Unlock()
	b.stopLocked()
}

// Running reports whether the browser is up.
func (b *sharedBrowser) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn != nil
}

// ---------- tab tracking ----------

type cdpTargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

func (b *sharedBrowser) onEvent(method, sessionID string, params json.RawMessage) {
	switch method {
	case "Target.targetCreated", "Target.targetInfoChanged":
		var p struct {
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(params, &p) != nil || p.TargetInfo.Type != "page" {
			return
		}
		ti := p.TargetInfo
		now := time.Now()
		b.mu.Lock()
		pg := b.pages[ti.TargetID]
		follow := false
		if pg == nil {
			pg = &sbPage{ID: ti.TargetID, created: now, changed: now}
			b.pages[ti.TargetID] = pg
			follow = true
		} else if pg.URL != ti.URL {
			pg.changed = now
			follow = true
			delete(b.captcha, ti.TargetID) // re-checked on the new page
		}
		pg.URL, pg.Title = ti.URL, ti.Title
		// The live view follows where the work happens: the newest or most recently
		// navigated tab, unless the operator picked one or holds control.
		switchView := b.viewed == "" || (follow && !b.pinned && !b.operatorActiveLocked(now) && ti.URL != "about:blank")
		if switchView {
			b.viewed = ti.TargetID
		}
		b.mu.Unlock()
		if switchView {
			go b.refreshScreencast()
		}
		b.broadcastStatus()
	case "Target.targetDestroyed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		b.mu.Lock()
		_, known := b.pages[p.TargetID]
		delete(b.pages, p.TargetID)
		delete(b.sessions, p.TargetID)
		delete(b.captcha, p.TargetID)
		if b.viewed == p.TargetID {
			b.viewed = b.newestPageLocked()
			b.castSess = ""
			b.pinned = false
		}
		b.mu.Unlock()
		if known {
			go b.refreshScreencast()
			b.broadcastStatus()
		}
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		b.mu.Lock()
		for id, s := range b.sessions {
			if s == p.SessionID {
				delete(b.sessions, id)
			}
		}
		if b.castSess == p.SessionID {
			b.castSess = ""
		}
		b.mu.Unlock()
	case "Page.screencastFrame":
		var p struct {
			Data      string `json:"data"`
			SessionID int64  `json:"sessionId"`
			Metadata  struct {
				DeviceWidth  float64 `json:"deviceWidth"`
				DeviceHeight float64 `json:"deviceHeight"`
			} `json:"metadata"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		b.mu.Lock()
		conn := b.conn
		current := sessionID == b.castSess
		if current {
			b.frameW, b.frameH = p.Metadata.DeviceWidth, p.Metadata.DeviceHeight
		}
		b.mu.Unlock()
		if conn != nil {
			conn.Send(sessionID, "Page.screencastFrameAck", map[string]any{"sessionId": p.SessionID})
		}
		if !current {
			return
		}
		ev, _ := json.Marshal(map[string]any{"type": "frame", "data": p.Data,
			"width": p.Metadata.DeviceWidth, "height": p.Metadata.DeviceHeight})
		b.mu.Lock()
		b.last = ev
		b.mu.Unlock()
		b.publish(ev)
	}
}

func (b *sharedBrowser) newestPageLocked() string {
	best := ""
	var bt time.Time
	for id, p := range b.pages {
		if best == "" || p.changed.After(bt) {
			best, bt = id, p.changed
		}
	}
	return best
}

// session attaches to a page target (flatten mode) and returns its session id.
func (b *sharedBrowser) session(ctx context.Context, targetID string) (string, *cdpConn, error) {
	b.mu.Lock()
	conn := b.conn
	sid := b.sessions[targetID]
	b.mu.Unlock()
	if conn == nil {
		return "", nil, errors.New("the shared browser is not running")
	}
	if sid != "" {
		return sid, conn, nil
	}
	raw, err := conn.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return "", nil, err
	}
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.SessionID == "" {
		return "", nil, errors.New("attach to tab failed")
	}
	b.mu.Lock()
	b.sessions[targetID] = r.SessionID
	b.mu.Unlock()
	return r.SessionID, conn, nil
}

// ---------- live view ----------

// Subscribe registers a live-view client. The channel gets status and frame
// events (JSON); cancel unregisters it.
func (b *sharedBrowser) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 8)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	last := b.last
	b.mu.Unlock()
	if st, err := json.Marshal(map[string]any{"type": "status", "status": b.Status()}); err == nil {
		ch <- st
	}
	if last != nil {
		ch <- last
	}
	go b.refreshScreencast()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		empty := len(b.subs) == 0
		b.mu.Unlock()
		if empty {
			go b.refreshScreencast()
		}
	}
}

// publish sends an event to every subscriber; a slow subscriber drops frames
// instead of stalling the browser connection.
func (b *sharedBrowser) publish(ev []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *sharedBrowser) broadcastStatus() {
	ev, err := json.Marshal(map[string]any{"type": "status", "status": b.Status()})
	if err == nil {
		b.publish(ev)
	}
}

// refreshScreencast runs the screencast on the viewed tab while anyone watches,
// and stops it otherwise.
func (b *sharedBrowser) refreshScreencast() {
	b.castMu.Lock()
	defer b.castMu.Unlock()
	ctx, cancel := context.WithTimeout(b.parent, 10*time.Second)
	defer cancel()
	b.mu.Lock()
	conn := b.conn
	want := b.viewed
	watching := len(b.subs) > 0
	prev := b.castSess
	b.mu.Unlock()
	if conn == nil {
		return
	}
	var sid string
	if watching && want != "" {
		var err error
		if sid, _, err = b.session(ctx, want); err != nil {
			return
		}
	}
	if prev == sid && sid != "" {
		return
	}
	if prev != "" && prev != sid {
		_, _ = conn.Call(ctx, prev, "Page.stopScreencast", nil)
	}
	b.mu.Lock()
	b.castSess = sid
	b.last = nil
	b.mu.Unlock()
	if sid == "" {
		return
	}
	_, _ = conn.Call(ctx, sid, "Page.enable", nil)
	_, _ = conn.Call(ctx, sid, "Page.startScreencast", map[string]any{
		"format": "jpeg", "quality": 70,
		"maxWidth": sharedBrowserWidth, "maxHeight": sharedBrowserHeight, "everyNthFrame": 1,
	})
}

// Status is the snapshot the live view renders.
func (b *sharedBrowser) Status() SharedBrowserStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	st := SharedBrowserStatus{Enabled: true, Running: b.conn != nil, Error: b.lastErr, ProfileScope: b.profileScope, Viewed: b.viewed,
		Operator: b.operatorActiveLocked(now), Width: sharedBrowserWidth, Height: sharedBrowserHeight, Tabs: []sbPage{}}
	for _, p := range b.pages {
		st.Tabs = append(st.Tabs, *p)
	}
	sort.Slice(st.Tabs, func(i, j int) bool { return st.Tabs[i].created.Before(st.Tabs[j].created) })
	if b.lease.activeLocked(now) {
		st.Agent = b.lease.label
	}
	if c, ok := b.captcha[b.viewed]; ok {
		cc := c
		st.Captcha = &cc
	} else {
		for _, c := range b.captcha {
			cc := c
			st.Captcha = &cc
			break
		}
	}
	return st
}

// ---------- operator control ----------

// SetOperator gives control to the operator (true) or hands it back (false).
// While the operator holds control the agent's browser tools are refused.
func (b *sharedBrowser) SetOperator(on bool) {
	b.mu.Lock()
	b.operator = on
	if on {
		b.operatorSince = b.now()
	} else {
		b.operatorSince = time.Time{}
		b.pinned = false
	}
	b.mu.Unlock()
	b.broadcastStatus()
}

// SelectTab shows a tab in the live view.
func (b *sharedBrowser) SelectTab(id string) error {
	b.mu.Lock()
	if _, ok := b.pages[id]; !ok {
		b.mu.Unlock()
		return errors.New("no such tab")
	}
	b.viewed, b.pinned = id, true
	b.mu.Unlock()
	b.refreshScreencast()
	b.broadcastStatus()
	return nil
}

// viewedSession returns the session of the viewed tab and its last frame size.
func (b *sharedBrowser) viewedSession(ctx context.Context) (string, *cdpConn, float64, float64, error) {
	b.mu.Lock()
	id := b.viewed
	w, h := b.frameW, b.frameH
	b.mu.Unlock()
	if id == "" {
		return "", nil, 0, 0, errors.New("no tab open")
	}
	sid, conn, err := b.session(ctx, id)
	if w <= 0 || h <= 0 {
		w, h = sharedBrowserWidth, sharedBrowserHeight
	}
	return sid, conn, w, h, err
}

// sbInput is one live-view input event. X and Y are fractions of the frame
// (0..1), so the client does not need to know the page's CSS size.
type sbInput struct {
	Kind       string  `json:"kind"` // mouse | wheel | key | text
	Type       string  `json:"type"` // mousePressed|mouseReleased|mouseMoved ; keyDown|keyUp
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button"`
	ClickCount int     `json:"clickCount"`
	DeltaX     float64 `json:"deltaX"`
	DeltaY     float64 `json:"deltaY"`
	Modifiers  int     `json:"modifiers"`
	Key        string  `json:"key"`
	Code       string  `json:"code"`
	Text       string  `json:"text"`
	KeyCode    int     `json:"keyCode"`
}

// Input forwards operator input to the viewed tab. Any input takes control: the
// agent must not act on a page the operator is in the middle of.
func (b *sharedBrowser) Input(ctx context.Context, events []sbInput) error {
	b.mu.Lock()
	now := b.now()
	wasOperator := b.operatorActiveLocked(now)
	if !wasOperator && onlyMouseMoves(events) {
		// Hovering over the view without control is not input.
		b.mu.Unlock()
		return nil
	}
	b.operator = true
	b.operatorSince = now
	b.mu.Unlock()
	if !wasOperator {
		b.broadcastStatus()
	}
	sid, conn, w, h, err := b.viewedSession(ctx)
	if err != nil {
		return err
	}
	clamp := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	for _, e := range events {
		x, y := clamp(e.X)*w, clamp(e.Y)*h
		switch e.Kind {
		case "mouse":
			switch e.Type {
			case "mousePressed", "mouseReleased", "mouseMoved":
			default:
				continue
			}
			btn := e.Button
			if btn == "" {
				btn = "none"
			}
			p := map[string]any{"type": e.Type, "x": x, "y": y, "button": btn, "modifiers": e.Modifiers}
			if e.Type != "mouseMoved" {
				cc := e.ClickCount
				if cc <= 0 {
					cc = 1
				}
				p["clickCount"] = cc
			}
			_, err = conn.Call(ctx, sid, "Input.dispatchMouseEvent", p)
		case "wheel":
			_, err = conn.Call(ctx, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel",
				"x": x, "y": y, "deltaX": e.DeltaX, "deltaY": e.DeltaY, "modifiers": e.Modifiers})
		case "key":
			if e.Type != "keyDown" && e.Type != "keyUp" {
				continue
			}
			p := map[string]any{"type": e.Type, "key": e.Key, "code": e.Code, "modifiers": e.Modifiers,
				"windowsVirtualKeyCode": e.KeyCode, "nativeVirtualKeyCode": e.KeyCode}
			// A printable key without Ctrl/Alt/Meta types its character.
			if e.Type == "keyDown" && e.Text != "" && e.Modifiers&(1|2|4) == 0 {
				p["text"] = e.Text
			}
			_, err = conn.Call(ctx, sid, "Input.dispatchKeyEvent", p)
		case "text":
			if e.Text != "" {
				_, err = conn.Call(ctx, sid, "Input.insertText", map[string]any{"text": e.Text})
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func onlyMouseMoves(events []sbInput) bool {
	for _, e := range events {
		if e.Kind != "mouse" || e.Type != "mouseMoved" {
			return false
		}
	}
	return true
}

// Navigate runs an address-bar action on the viewed tab: a URL, back, forward,
// reload, a new tab or closing the viewed tab.
func (b *sharedBrowser) Navigate(ctx context.Context, action, target string) error {
	b.mu.Lock()
	conn := b.conn
	viewed := b.viewed
	b.operator = true
	b.operatorSince = b.now()
	b.mu.Unlock()
	b.broadcastStatus()
	if conn == nil {
		return errors.New("the shared browser is not running")
	}
	switch action {
	case "new_tab":
		u := normalizeBrowserURL(target)
		if u == "" {
			u = "about:blank"
		}
		raw, err := conn.Call(ctx, "", "Target.createTarget", map[string]any{"url": u})
		if err != nil {
			return err
		}
		var r struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(raw, &r)
		if r.TargetID != "" {
			b.mu.Lock()
			b.viewed, b.pinned = r.TargetID, true
			b.mu.Unlock()
			go b.refreshScreencast()
		}
		return nil
	case "close_tab":
		if viewed == "" {
			return nil
		}
		_, err := conn.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": viewed})
		return err
	}
	sid, conn, _, _, err := b.viewedSession(ctx)
	if err != nil {
		return err
	}
	switch action {
	case "goto":
		u := normalizeBrowserURL(target)
		if u == "" {
			return errors.New("empty address")
		}
		_, err = conn.Call(ctx, sid, "Page.navigate", map[string]any{"url": u})
	case "back":
		_, err = conn.Call(ctx, sid, "Runtime.evaluate", map[string]any{"expression": "history.back()"})
	case "forward":
		_, err = conn.Call(ctx, sid, "Runtime.evaluate", map[string]any{"expression": "history.forward()"})
	case "reload":
		_, err = conn.Call(ctx, sid, "Page.reload", nil)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return err
}

// normalizeBrowserURL turns address-bar text into a URL: a scheme is kept, a
// loopback host gets http://, anything else https://. Spaces are not searched:
// this is a testing browser, the operator types addresses.
func normalizeBrowserURL(in string) string {
	s := strings.TrimSpace(in)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "about:") || strings.HasPrefix(s, "data:") {
		return s
	}
	if strings.HasPrefix(s, "localhost") || strings.HasPrefix(s, "127.") || strings.HasPrefix(s, "[::1]") {
		return "http://" + s
	}
	return "https://" + s
}

// ---------- agent arbitration ----------

// AgentGate decides whether an agent run (holder) may call a browser tool now.
// It returns "" to allow, or the refusal text the model sees.
func (b *sharedBrowser) AgentGate(ctx context.Context, holder, label, tool string) string {
	b.mu.Lock()
	running := b.conn != nil
	if !running {
		b.mu.Unlock()
		return ""
	}
	now := b.now()
	if b.operatorActiveLocked(now) {
		b.mu.Unlock()
		return "The human operator has taken over the shared browser and is using it now (for example to solve a CAPTCHA or sign in). " +
			"Do not retry browser tools in a loop: continue with work that does not need the browser, or call ask_operator to ask them to hand the browser back when they are done."
	}
	if b.lease.holder != holder && b.lease.activeLocked(now) {
		other := b.lease.label
		b.mu.Unlock()
		return fmt.Sprintf("The shared browser is in use by another agent run (%s). Continue with work that does not need the browser and try again in a minute or two; do not loop on this call.", other)
	}
	b.lease = sbLease{holder: holder, label: label, last: now}
	b.mu.Unlock()

	if browserToolIsReadOnly(tool) {
		return ""
	}
	kind, url, since := b.captchaFor(ctx)
	if kind == "" {
		return ""
	}
	b.mu.Lock()
	acked := b.acks[holder]
	b.mu.Unlock()
	if !acked.IsZero() && acked.After(since) {
		return "" // the operator was asked after the check appeared
	}
	return fmt.Sprintf("A human check (%s) is on the page %s. Do not try to solve, click through or bypass it. "+
		"Call ask_operator now with a short question such as \"Please solve the %s in the shared browser (Function → Browser), then reply 'done'\" "+
		"and attach a browser screenshot. The operator completes it in the same browser you are driving; after their reply, take a new snapshot and continue. "+
		"Reading tools (snapshot, screenshot, console, network) still work.", kind, url, kind)
}

// AgentDone releases the lease of a finished agent run.
func (b *sharedBrowser) AgentDone(holder string) {
	b.mu.Lock()
	changed := b.lease.holder == holder && !b.now().Before(b.lease.hold)
	if changed {
		b.lease = sbLease{}
	}
	// acks survive the run: ask_operator pauses the intent, which ends this run,
	// and the resumed run (same holder) must still see that the operator was asked.
	b.mu.Unlock()
	if changed {
		b.broadcastStatus()
	}
}

// NoteAsk records that a run asked the operator (ask_operator), which releases the
// CAPTCHA block for that run.
func (b *sharedBrowser) NoteAsk(holder string) {
	now := b.now()
	b.mu.Lock()
	for h, t := range b.acks {
		if now.Sub(t) > 6*time.Hour {
			delete(b.acks, h)
		}
	}
	b.acks[holder] = now
	if b.conn != nil && (b.lease.holder == holder || !b.lease.activeLocked(now)) {
		b.lease.holder = holder
		b.lease.hold = now.Add(b.operatorLockTimeout())
	}
	b.mu.Unlock()
}

// UsedRecentlyBy reports whether holder drove the browser in the lease window.
func (b *sharedBrowser) UsedRecentlyBy(holder string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn != nil && b.lease.holder == holder && b.lease.activeLocked(b.now())
}

// browserToolIsReadOnly lists the Playwright MCP tools that only observe. Anything
// else (click, type, navigate, evaluate, run_code, file upload, …) acts on the page.
func browserToolIsReadOnly(tool string) bool {
	name := tool
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	switch name {
	case "browser_snapshot", "browser_take_screenshot", "browser_console_messages",
		"browser_network_requests", "browser_wait_for", "browser_tabs":
		return true
	}
	return false
}

// ---------- human-check detection ----------

// captchaFrameHosts identify CAPTCHA widgets by their frame URL.
var captchaFrameHosts = []struct {
	re   *regexp.Regexp
	kind string
}{
	{regexp.MustCompile(`(?i)google\.com/recaptcha|recaptcha\.net|gstatic\.com/recaptcha`), "reCAPTCHA"},
	{regexp.MustCompile(`(?i)hcaptcha\.com`), "hCaptcha"},
	{regexp.MustCompile(`(?i)challenges\.cloudflare\.com`), "Cloudflare Turnstile"},
	{regexp.MustCompile(`(?i)arkoselabs\.com|funcaptcha\.com`), "Arkose/FunCaptcha"},
	{regexp.MustCompile(`(?i)captcha-delivery\.com`), "DataDome"},
	{regexp.MustCompile(`(?i)geetest\.com`), "GeeTest"},
}

// humanCheckWording is the wording of human-verification interstitials.
var humanCheckWording = regexp.MustCompile(`(?i)verify (that )?you('| a)re (a )?human|i('|’)?m not a robot|are you a robot|complete the security check|press (&|and) hold|checking (if the site connection is secure|your browser)|our systems have detected unusual traffic|please solve the challenge`)

// classifyHumanCheck names the human check a page shows, or "" for none. frames
// are the page's frame URLs; widget is a CAPTCHA kind the DOM probe found; text is
// a prefix of the visible text. The wording only counts on a short page, so a
// results page that quotes it is not mistaken for a challenge.
func classifyHumanCheck(pageURL string, frames []string, widget, text string) string {
	if regexp.MustCompile(`(?i)^https?://(www\.)?google\.[a-z.]+/sorry/`).MatchString(pageURL) {
		return "Google unusual-traffic check"
	}
	for _, f := range frames {
		for _, h := range captchaFrameHosts {
			if h.re.MatchString(f) {
				return h.kind
			}
		}
	}
	if widget != "" {
		return widget
	}
	if len(strings.TrimSpace(text)) < 1500 && humanCheckWording.MatchString(text) {
		return "human verification page"
	}
	return ""
}

// humanCheckProbe runs in the page: a visible CAPTCHA widget, and a prefix of the text.
const humanCheckProbe = `(() => {
  const vis = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el);
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'; };
  const widgets = [
    ['.g-recaptcha, [data-sitekey][class*="recaptcha"]', 'reCAPTCHA'],
    ['.h-captcha, [data-hcaptcha-widget-id]', 'hCaptcha'],
    ['.cf-turnstile, #challenge-form, #cf-challenge-running', 'Cloudflare Turnstile'],
    ['#px-captcha', 'PerimeterX'],
    ['.geetest_holder, .geetest_panel', 'GeeTest'],
  ];
  let widget = '';
  for (const [sel, kind] of widgets) { const el = document.querySelector(sel); if (el && vis(el)) { widget = kind; break; } }
  const text = (document.body && document.body.innerText || '').slice(0, 2000);
  return JSON.stringify({ widget, text });
})()`

// captchaFor checks the open tabs for a human check and returns the first found,
// with when it was first seen. Results are cached per tab until it navigates.
func (b *sharedBrowser) captchaFor(ctx context.Context) (kind, url string, since time.Time) {
	b.scanCaptchas(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.captcha {
		if kind == "" || c.Since.Before(since) {
			kind, url, since = c.Kind, c.URL, c.Since
		}
	}
	return kind, url, since
}

// scanCaptchas inspects every open tab once.
func (b *sharedBrowser) scanCaptchas(ctx context.Context) {
	b.mu.Lock()
	ids := make([]string, 0, len(b.pages))
	urls := map[string]string{}
	for id, p := range b.pages {
		ids = append(ids, id)
		urls[id] = p.URL
	}
	b.mu.Unlock()
	changed := false
	for _, id := range ids {
		if strings.HasPrefix(urls[id], "about:") || strings.HasPrefix(urls[id], "chrome") {
			continue
		}
		kind := b.inspectTab(ctx, id, urls[id])
		b.mu.Lock()
		prev, had := b.captcha[id]
		if kind == "" && had {
			delete(b.captcha, id)
			changed = true
		} else if kind != "" && (!had || prev.Kind != kind) {
			b.captcha[id] = sbCaptcha{Kind: kind, URL: urls[id], Since: time.Now()}
			changed = true
		}
		b.mu.Unlock()
	}
	if changed {
		b.broadcastStatus()
	}
}

func (b *sharedBrowser) inspectTab(ctx context.Context, id, pageURL string) string {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	sid, conn, err := b.session(cctx, id)
	if err != nil {
		return ""
	}
	var frames []string
	if raw, err := conn.Call(cctx, sid, "Page.getFrameTree", nil); err == nil {
		var tree struct {
			FrameTree json.RawMessage `json:"frameTree"`
		}
		if json.Unmarshal(raw, &tree) == nil {
			frames = collectFrameURLs(tree.FrameTree, nil)
		}
	}
	var widget, text string
	if raw, err := conn.Call(cctx, sid, "Runtime.evaluate", map[string]any{"expression": humanCheckProbe, "returnByValue": true}); err == nil {
		var r struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &r) == nil {
			var v struct {
				Widget string `json:"widget"`
				Text   string `json:"text"`
			}
			if json.Unmarshal([]byte(r.Result.Value), &v) == nil {
				widget, text = v.Widget, v.Text
			}
		}
	}
	return classifyHumanCheck(pageURL, frames, widget, text)
}

// collectFrameURLs walks a Page.getFrameTree result.
func collectFrameURLs(raw json.RawMessage, out []string) []string {
	var node struct {
		Frame struct {
			URL string `json:"url"`
		} `json:"frame"`
		ChildFrames []json.RawMessage `json:"childFrames"`
	}
	if json.Unmarshal(raw, &node) != nil {
		return out
	}
	if node.Frame.URL != "" {
		out = append(out, node.Frame.URL)
	}
	for _, c := range node.ChildFrames {
		out = collectFrameURLs(c, out)
	}
	return out
}

// captchaLoop keeps the CAPTCHA banner of the live view current while someone
// watches; agent calls scan on demand.
func (b *sharedBrowser) captchaLoop(conn *cdpConn) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-conn.Done():
			return
		case <-b.parent.Done():
			return
		case <-t.C:
			b.mu.Lock()
			watching := len(b.subs) > 0
			b.mu.Unlock()
			if watching {
				ctx, cancel := context.WithTimeout(b.parent, 5*time.Second)
				b.scanCaptchas(ctx)
				cancel()
			}
		}
	}
}
