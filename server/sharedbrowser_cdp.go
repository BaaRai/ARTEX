package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// cdpTransport abstracts the underlying byte-frame link to Chromium: either
// NUL-delimited JSON over --remote-debugging-pipe (Unix, zero listening port)
// or a browser-level DevTools WebSocket (fallback on non-Unix).
type cdpTransport interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	Close() error
}

type wsCDPTransport struct {
	ws *websocket.Conn
}

func (t *wsCDPTransport) ReadMessage() ([]byte, error) {
	_, b, err := t.ws.ReadMessage()
	return b, err
}

func (t *wsCDPTransport) WriteMessage(b []byte) error {
	return t.ws.WriteMessage(websocket.TextMessage, b)
}

func (t *wsCDPTransport) Close() error { return t.ws.Close() }

// pipeCDPTransport speaks Chrome DevTools Protocol over --remote-debugging-pipe:
// JSON messages delimited by a single 0x00 byte on FD 3 (write) / FD 4 (read).
type pipeCDPTransport struct {
	w  io.WriteCloser
	r  io.ReadCloser
	br *bufio.Reader
}

func newPipeCDPTransport(w io.WriteCloser, r io.ReadCloser) *pipeCDPTransport {
	return &pipeCDPTransport{w: w, r: r, br: bufio.NewReaderSize(r, 64<<10)}
}

func (t *pipeCDPTransport) ReadMessage() ([]byte, error) {
	b, err := t.br.ReadBytes(0)
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b, nil
}

func (t *pipeCDPTransport) WriteMessage(b []byte) error {
	buf := make([]byte, len(b)+1)
	copy(buf, b)
	buf[len(b)] = 0
	_, err := t.w.Write(buf)
	return err
}

func (t *pipeCDPTransport) Close() error {
	errW := t.w.Close()
	errR := t.r.Close()
	if errW != nil {
		return errW
	}
	return errR
}

// cdpConn is a minimal Chrome DevTools Protocol client over a browser-level
// transport (pipe or websocket). It multiplexes page sessions with flatten mode
// and also supports proxying authenticated MCP WebSocket clients over the same
// server-owned link via ForwardRaw / SubscribeEvents.
type cdpConn struct {
	tr      cdpTransport
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu        sync.Mutex
	pending   map[int64]chan cdpReply
	rawWaiter map[int64]chan json.RawMessage
	evSubs    map[chan json.RawMessage]struct{}
	closed    bool
	done      chan struct{}

	// onEvent receives every event (a message without an id). It runs on the
	// read loop, so it must not block on a Call.
	onEvent func(method, sessionID string, params json.RawMessage)
}

type cdpReply struct {
	result json.RawMessage
	err    error
}

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// errCDPClosed is returned by Call once the connection is gone.
var errCDPClosed = errors.New("devtools connection closed")

func newCDPConn(tr cdpTransport, onEvent func(method, sessionID string, params json.RawMessage)) *cdpConn {
	c := &cdpConn{
		tr:        tr,
		pending:   map[int64]chan cdpReply{},
		rawWaiter: map[int64]chan json.RawMessage{},
		evSubs:    map[chan json.RawMessage]struct{}{},
		done:      make(chan struct{}),
		onEvent:   onEvent,
	}
	go c.readLoop()
	return c
}

// dialCDP connects to a browser websocket endpoint (ws://127.0.0.1:<port>/devtools/browser/<id>).
func dialCDP(ctx context.Context, wsURL string, onEvent func(method, sessionID string, params json.RawMessage)) (*cdpConn, error) {
	d := websocket.Dialer{ReadBufferSize: 1 << 16, WriteBufferSize: 1 << 16}
	ws, _, err := d.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	// Screencast frames are JPEG in base64; a large viewport frame can pass 1 MiB.
	ws.SetReadLimit(32 << 20)
	return newCDPConn(&wsCDPTransport{ws: ws}, onEvent), nil
}

// newPipeCDP attaches a cdpConn directly to Chromium's --remote-debugging-pipe.
func newPipeCDP(w io.WriteCloser, r io.ReadCloser, onEvent func(method, sessionID string, params json.RawMessage)) *cdpConn {
	return newCDPConn(newPipeCDPTransport(w, r), onEvent)
}

func (c *cdpConn) readLoop() {
	defer c.shutdown()
	for {
		raw, err := c.tr.ReadMessage()
		if err != nil {
			return
		}
		if len(raw) == 0 {
			continue
		}
		var msg cdpMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			rawCh := c.rawWaiter[msg.ID]
			delete(c.rawWaiter, msg.ID)
			c.mu.Unlock()
			if rawCh != nil {
				cp := make(json.RawMessage, len(raw))
				copy(cp, raw)
				rawCh <- cp
				continue
			}
			if ch == nil {
				continue
			}
			if msg.Error != nil {
				ch <- cdpReply{err: fmt.Errorf("%s (code %d)", msg.Error.Message, msg.Error.Code)}
			} else {
				ch <- cdpReply{result: msg.Result}
			}
			continue
		}
		if msg.Method != "" {
			if c.onEvent != nil {
				c.onEvent(msg.Method, msg.SessionID, msg.Params)
			}
			c.broadcastRawEvent(raw)
		}
	}
}

func (c *cdpConn) broadcastRawEvent(raw []byte) {
	c.mu.Lock()
	if len(c.evSubs) == 0 {
		c.mu.Unlock()
		return
	}
	cp := make(json.RawMessage, len(raw))
	copy(cp, raw)
	for ch := range c.evSubs {
		select {
		case ch <- cp:
		default:
		}
	}
	c.mu.Unlock()
}

// SubscribeRawEvents delivers raw CDP event JSON frames to a proxied MCP client.
func (c *cdpConn) SubscribeRawEvents() (<-chan json.RawMessage, func()) {
	ch := make(chan json.RawMessage, 128)
	c.mu.Lock()
	if !c.closed {
		c.evSubs[ch] = struct{}{}
	}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.evSubs, ch)
		c.mu.Unlock()
	}
}

// ForwardRaw sends one client CDP JSON-RPC command over the shared connection
// with a remapped internal ID, returning the raw reply JSON with the client's
// original ID restored.
func (c *cdpConn) ForwardRaw(ctx context.Context, clientFrame []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(clientFrame, &envelope); err != nil {
		return nil, fmt.Errorf("invalid CDP JSON: %w", err)
	}
	origID, hasID := envelope["id"]
	if !hasID {
		// Notification without id: forward directly.
		c.writeMu.Lock()
		err := c.tr.WriteMessage(clientFrame)
		c.writeMu.Unlock()
		return nil, err
	}
	internalID := c.nextID.Add(1)
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errCDPClosed
	}
	c.rawWaiter[internalID] = ch
	c.mu.Unlock()

	envelope["id"], _ = json.Marshal(internalID)
	out, err := json.Marshal(envelope)
	if err != nil {
		c.mu.Lock()
		delete(c.rawWaiter, internalID)
		c.mu.Unlock()
		return nil, err
	}
	c.writeMu.Lock()
	err = c.tr.WriteMessage(out)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.rawWaiter, internalID)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case reply, ok := <-ch:
		if !ok || len(reply) == 0 {
			return nil, errCDPClosed
		}
		var repEnv map[string]json.RawMessage
		if err := json.Unmarshal(reply, &repEnv); err != nil {
			return reply, nil
		}
		repEnv["id"] = origID
		return json.Marshal(repEnv)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.rawWaiter, internalID)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *cdpConn) shutdown() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = map[int64]chan cdpReply{}
	rawWaiter := c.rawWaiter
	c.rawWaiter = map[int64]chan json.RawMessage{}
	evSubs := c.evSubs
	c.evSubs = map[chan json.RawMessage]struct{}{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- cdpReply{err: errCDPClosed}
	}
	for _, ch := range rawWaiter {
		close(ch)
	}
	for ch := range evSubs {
		close(ch)
	}
	if c.tr != nil {
		_ = c.tr.Close()
	}
	close(c.done)
}

// Close ends the connection; pending calls fail with errCDPClosed.
func (c *cdpConn) Close() { c.shutdown() }

// Done is closed when the connection ends (the browser exited or Close was called).
func (c *cdpConn) Done() <-chan struct{} { return c.done }

// Call sends one command and waits for its reply. sessionID is empty for
// browser-level commands (Target.*, Browser.*).
func (c *cdpConn) Call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errCDPClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	c.writeMu.Lock()
	err = c.tr.WriteMessage(raw)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Send issues a command without waiting for the reply (screencast frame acks).
func (c *cdpConn) Send(sessionID, method string, params any) {
	id := c.nextID.Add(1)
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	_ = c.tr.WriteMessage(raw)
	c.writeMu.Unlock()
}
