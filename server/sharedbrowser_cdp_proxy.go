package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// cdpProxy is the server-owned authenticated CDP endpoint exposed to
// playwright-mcp. Every agent run receives an ephemeral 256-bit random token
// that is revoked when the run finishes. Unauthenticated callers (such as an
// agent Bash process scanning loopback ports) receive 401 Unauthorized, and
// requests while the operator holds control or another holder owns the lease
// receive 403 Forbidden.
//
// Residual risk: because playwright-mcp runs under the same executor UID as
// agent Bash, its --cdp-endpoint argv token is readable via /proc/<pid>/cmdline
// by same-UID agent processes while that run is active. Mitigations: Chromium
// itself exposes no TCP port on Unix (--remote-debugging-pipe), tokens are
// revoked immediately when the run ends, and this proxy re-checks token
// validity, operator hold (operatorActiveLocked), and active lease on every
// HTTP request and every WebSocket message so operator take-over cannot be
// bypassed even if the token is read.
type cdpProxy struct {
	b        *sharedBrowser
	ln       net.Listener
	srv      *http.Server
	addr     string
	upgrader websocket.Upgrader

	mu     sync.Mutex
	tokens map[string]string // token -> holder
}

func newCDPProxy(b *sharedBrowser) (*cdpProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cdp proxy listen: %w", err)
	}
	p := &cdpProxy{
		b:      b,
		ln:     ln,
		addr:   ln.Addr().String(),
		tokens: map[string]string{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1 << 16,
			WriteBufferSize: 1 << 16,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
	p.srv = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

func (p *cdpProxy) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.tokens = map[string]string{}
	p.mu.Unlock()
	_ = p.srv.Close()
}

func (p *cdpProxy) IssueToken(holder string) (token string, endpoint string, revoke func()) {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	token = hex.EncodeToString(raw[:])
	p.mu.Lock()
	p.tokens[token] = holder
	p.mu.Unlock()
	endpoint = fmt.Sprintf("ws://%s/cdp/%s/devtools/browser", p.addr, token)
	return token, endpoint, func() { p.RevokeToken(token) }
}

func (p *cdpProxy) RevokeToken(token string) {
	if p == nil || token == "" {
		return
	}
	p.mu.Lock()
	delete(p.tokens, token)
	p.mu.Unlock()
}

// authenticate extracts the bearer token from Authorization header or
// /cdp/<token>/... path prefix and matches it in constant time.
func (p *cdpProxy) authenticate(r *http.Request) (token, holder, restPath string, ok bool) {
	path := r.URL.Path
	if trimmed, hasPrefix := strings.CutPrefix(path, "/cdp/"); hasPrefix {
		if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
			token = trimmed[:idx]
			restPath = trimmed[idx:]
		} else {
			token = trimmed
			restPath = "/"
		}
	} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		restPath = path
	}
	if token == "" {
		return "", "", "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var matchedHolder string
	matched := false
	for k, h := range p.tokens {
		if len(k) == len(token) && subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			matchedHolder = h
			matched = true
		}
	}
	if !matched {
		return "", "", "", false
	}
	return token, matchedHolder, restPath, true
}

func (p *cdpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, holder, rest, ok := p.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized: valid per-run CDP token required", http.StatusUnauthorized)
		return
	}
	// Enforce operator lock and active lease at the CDP proxy layer too so
	// even a leaked per-run token cannot bypass operator control.
	p.b.mu.Lock()
	now := p.b.now()
	conn := p.b.conn
	opActive := p.b.operatorActiveLocked(now)
	leaseBlocked := p.b.lease.holder != "" && p.b.lease.holder != holder && p.b.lease.activeLocked(now)
	p.b.mu.Unlock()
	if opActive {
		http.Error(w, "forbidden: human operator holds control of the shared browser", http.StatusForbidden)
		return
	}
	if leaseBlocked {
		http.Error(w, "forbidden: shared browser lease held by another run", http.StatusForbidden)
		return
	}
	if conn == nil {
		http.Error(w, "shared browser is not running", http.StatusServiceUnavailable)
		return
	}

	if websocket.IsWebSocketUpgrade(r) {
		p.handleWS(w, r, token, holder, conn)
		return
	}

	switch rest {
	case "/json/version":
		wsURL := fmt.Sprintf("ws://%s/cdp/%s/devtools/browser", p.addr, token)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Browser":              "ARTEX-Shared-Browser",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": wsURL,
		})
	case "/json", "/json/list":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	default:
		http.NotFound(w, r)
	}
}

func (p *cdpProxy) handleWS(w http.ResponseWriter, r *http.Request, token, holder string, conn *cdpConn) {
	ws, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(32 << 20)

	evCh, unsub := conn.SubscribeRawEvents()
	defer unsub()

	var writeMu sync.Mutex
	writeText := func(b []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteMessage(websocket.TextMessage, b)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-conn.Done():
				_ = ws.Close()
				return
			case ev, ok := <-evCh:
				if !ok {
					return
				}
				if writeText(ev) != nil {
					return
				}
			}
		}
	}()

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		// Re-check token validity and operator lock on every message.
		p.mu.Lock()
		_, stillValid := p.tokens[token]
		p.mu.Unlock()
		if !stillValid {
			return
		}
		p.b.mu.Lock()
		opActive := p.b.operatorActiveLocked(p.b.now())
		leaseBlocked := p.b.lease.holder != "" && p.b.lease.holder != holder && p.b.lease.activeLocked(p.b.now())
		p.b.mu.Unlock()
		if opActive || leaseBlocked {
			return
		}
		reply, err := conn.ForwardRaw(r.Context(), msg)
		if err != nil {
			return
		}
		if len(reply) > 0 {
			if writeText(reply) != nil {
				return
			}
		}
	}
}
