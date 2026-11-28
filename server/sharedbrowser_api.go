package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Shared browser API (Function → Browser in the web UI).
//
//	GET  /api/browser/status   state: running, tabs, who drives it, CAPTCHA seen
//	GET  /api/browser/stream   SSE: status and JPEG frame events of the viewed tab
//	POST /api/browser/start    start the browser (opt-in; optional {task_id|session_id} profile scope)
//	POST /api/browser/control  {action: take|release|select_tab|goto|back|forward|reload|new_tab|close_tab, target}
//	POST /api/browser/input    {events: [...]} mouse, wheel, key and text input (takes control)
//
// All of them sit behind requireAuth like every other /api route.

func (s *Server) registerSharedBrowserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/browser/status", s.browserStatus)
	mux.HandleFunc("GET /api/browser/stream", s.browserStream)
	mux.HandleFunc("POST /api/browser/start", s.browserStart)
	mux.HandleFunc("POST /api/browser/control", s.browserControl)
	mux.HandleFunc("POST /api/browser/input", s.browserInput)
}

func (s *Server) browserStatus(w http.ResponseWriter, _ *http.Request) {
	if s.browser == nil {
		writeJSON(w, 200, SharedBrowserStatus{Tabs: []sbPage{}})
		return
	}
	writeJSON(w, 200, s.browser.Status())
}

// browserStart starts the browser with the proxy the browser MCP row carries, so
// the operator can sign in before any task runs. Optional JSON body {task_id,
// session_id} selects an isolated profile; empty body uses the "operator" scope.
func (s *Server) browserStart(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeErr(w, 409, "the shared browser is turned off (opt-in via -shared-browser or ARTEX_SHARED_BROWSER=true)")
		return
	}
	scope := "operator"
	if r.Body != nil {
		var req struct {
			TaskID    int64  `json:"task_id"`
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err == nil {
			switch {
			case req.TaskID > 0:
				scope = fmt.Sprintf("task-%d", req.TaskID)
			case strings.TrimSpace(req.SessionID) != "":
				scope = "session-" + sanitizeProfileScope(req.SessionID)
			}
		}
	}
	proxy, caCertPath := "", ""
	if s.m != nil && s.m.pg != nil {
		if servers, err := s.m.pg.ListMCP(); err == nil {
			for _, m := range servers {
				if isSharedBrowserMCP(m) {
					proxy, caCertPath = browserMCPProxy(jsonStrSlice(m.Args), jsonStrMap(m.Env))
					break
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := s.browser.Ensure(ctx, scope, proxy, caCertPath, ""); err != nil {
		writeErr(w, 500, "start the shared browser: "+err.Error())
		return
	}
	writeJSON(w, 200, s.browser.Status())
}

func (s *Server) browserControl(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeErr(w, 409, "the shared browser is turned off (opt-in via -shared-browser or ARTEX_SHARED_BROWSER=true)")
		return
	}
	var req struct {
		Action string `json:"action"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var err error
	switch req.Action {
	case "take":
		s.browser.SetOperator(true)
	case "release":
		s.browser.SetOperator(false)
	case "select_tab":
		err = s.browser.SelectTab(req.Target)
	case "goto", "back", "forward", "reload", "new_tab", "close_tab":
		err = s.browser.Navigate(ctx, req.Action, req.Target)
	default:
		writeErr(w, 400, fmt.Sprintf("unknown action %q", req.Action))
		return
	}
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, s.browser.Status())
}

func (s *Server) browserInput(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		writeErr(w, 409, "the shared browser is turned off (opt-in via -shared-browser or ARTEX_SHARED_BROWSER=true)")
		return
	}
	var req struct {
		Events []sbInput `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if len(req.Events) > 200 {
		writeErr(w, 400, "too many events in one request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.browser.Input(ctx, req.Events); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	w.WriteHeader(204)
}

// browserStream is the live view: one status event on connect and on every
// change, and a frame event per screencast frame of the viewed tab.
func (s *Server) browserStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	tok := extractToken(r)
	if !s.sessionOK(tok) {
		writeErr(w, 401, "token is invalid or expired")
		return
	}
	if s.browser == nil {
		writeErr(w, 409, "the shared browser is turned off (opt-in via -shared-browser or ARTEX_SHARED_BROWSER=true)")
		return
	}
	// A long-lived stream must not be cut by the server's write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := s.browser.Subscribe()
	defer unsub()
	ctx := r.Context()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", ev)
			flusher.Flush()
		case <-ping.C:
			if !s.sessionOK(tok) {
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
