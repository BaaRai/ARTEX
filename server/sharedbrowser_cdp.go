package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// cdpConn is a minimal Chrome DevTools Protocol client over the browser-level
// websocket. It multiplexes page sessions with flatten mode (Target.attachToTarget
// with flatten=true): every command and event carries the sessionId of the page it
// belongs to, so one connection serves the screencast, input and page inspection.
type cdpConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan cdpReply
	closed  bool
	done    chan struct{}

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

// dialCDP connects to a browser websocket endpoint (ws://127.0.0.1:<port>/devtools/browser/<id>).
func dialCDP(ctx context.Context, wsURL string, onEvent func(method, sessionID string, params json.RawMessage)) (*cdpConn, error) {
	d := websocket.Dialer{ReadBufferSize: 1 << 16, WriteBufferSize: 1 << 16}
	ws, _, err := d.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	// Screencast frames are JPEG in base64; a large viewport frame can pass 1 MiB.
	ws.SetReadLimit(32 << 20)
	c := &cdpConn{ws: ws, pending: map[int64]chan cdpReply{}, done: make(chan struct{}), onEvent: onEvent}
	go c.readLoop()
	return c, nil
}

func (c *cdpConn) readLoop() {
	defer c.shutdown()
	for {
		var msg cdpMessage
		if err := c.ws.ReadJSON(&msg); err != nil {
			return
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
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
		if msg.Method != "" && c.onEvent != nil {
			c.onEvent(msg.Method, msg.SessionID, msg.Params)
		}
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
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- cdpReply{err: errCDPClosed}
	}
	_ = c.ws.Close()
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
	c.writeMu.Lock()
	err := c.ws.WriteJSON(msg)
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
	c.writeMu.Lock()
	_ = c.ws.WriteJSON(msg)
	c.writeMu.Unlock()
}
