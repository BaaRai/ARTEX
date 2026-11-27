package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/agent"
)

// WithListenAddress records the actual listener so OAuth always uses a fixed
// 127.0.0.1 callback URI instead of a caller-controlled Host header.
func WithListenAddress(addr string) Option {
	return func(s *Server) { s.listenAddr = addr }
}

func (s *Server) chatgptCallbackURL() (string, error) {
	host, portText, err := net.SplitHostPort(s.listenAddr)
	if err != nil || (host != "127.0.0.1" && host != "0.0.0.0" && host != "") {
		return "", fmt.Errorf("ChatGPT sign-in requires an ARTEX listener reachable at 127.0.0.1")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("ChatGPT sign-in requires a fixed local listener port")
	}
	return fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port), nil
}

func (s *Server) pgChatGPTStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.chatgptStore.Status()
	if err != nil {
		writeJSON(w, 200, map[string]any{"state": "unknown"})
		return
	}
	state := "disconnected"
	if status.Connected {
		state = "connected"
	}
	writeJSON(w, 200, map[string]any{"state": state})
}

func (s *Server) pgChatGPTLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	callbackURL, err := s.chatgptCallbackURL()
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	loginURL, err := s.chatgptStore.BeginLogin(callbackURL)
	if err != nil {
		writeErr(w, 503, "ChatGPT sign-in could not start")
		return
	}
	writeJSON(w, 200, map[string]any{"authorization_url": loginURL})
}

func (s *Server) pgChatGPTLogout(w http.ResponseWriter, r *http.Request) {
	err := s.chatgptStore.Logout(r.Context())
	status, statusErr := s.chatgptStore.Status()
	if statusErr != nil || status.Connected {
		writeErr(w, 503, "ChatGPT local sign-out could not be confirmed")
		return
	}
	s.invalidateProfileAgents()
	s.reapplyActiveProfile()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": true, "warning": "signed out locally; remote revocation was not confirmed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) chatgptCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if _, err := s.chatgptStore.CompleteLogin(r.Context(), r.URL.Query()); err != nil {
		http.Redirect(w, r, "/system/llm?chatgpt=error", http.StatusSeeOther)
		return
	}
	s.invalidateProfileAgents()
	s.reapplyActiveProfile()
	http.Redirect(w, r, "/system/llm?chatgpt=connected", http.StatusSeeOther)
}

func (s *Server) pgClaudeCodeStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	connected, err := agent.ClaudeCodeLoginStatus(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"state": "unknown"})
		return
	}
	state := "disconnected"
	if connected {
		state = "connected"
	}
	writeJSON(w, 200, map[string]any{"state": state})
}
