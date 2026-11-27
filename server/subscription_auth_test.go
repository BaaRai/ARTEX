package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/chatgptplan"
	"github.com/Autumn-27/artex/db"
)

func TestChatGPTCallbackURLUsesFixedLoopbackHost(t *testing.T) {
	for _, tc := range []struct {
		listen string
		want   string
	}{
		{"127.0.0.1:8787", "http://127.0.0.1:8787/auth/callback"},
		{"0.0.0.0:8999", "http://127.0.0.1:8999/auth/callback"},
		{":9000", "http://127.0.0.1:9000/auth/callback"},
	} {
		t.Run(tc.listen, func(t *testing.T) {
			s := &Server{listenAddr: tc.listen}
			got, err := s.chatgptCallbackURL()
			if err != nil || got != tc.want {
				t.Fatalf("callback URL = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestChatGPTCallbackURLRejectsNonLocalOrUnfixedListeners(t *testing.T) {
	for _, listen := range []string{"", "192.0.2.1:8787", "127.0.0.1:0", "127.0.0.1:not-a-port"} {
		t.Run(listen, func(t *testing.T) {
			s := &Server{listenAddr: listen}
			if got, err := s.chatgptCallbackURL(); err == nil {
				t.Fatalf("callback URL = %q; expected error for %q", got, listen)
			}
		})
	}
}

func TestSubscriptionLoginAPIRoutesRequireARTEXSession(t *testing.T) {
	s := &Server{chatgptStore: chatgptplan.NewStore(filepath.Join(t.TempDir(), "chatgpt-plan.json"))}
	handler := s.Handler()
	for _, path := range []string{"/api/llm/chatgpt/status", "/api/llm/chatgpt/login", "/api/llm/chatgpt/logout", "/api/llm/claude/status"} {
		method := http.MethodGet
		if path == "/api/llm/chatgpt/login" || path == "/api/llm/chatgpt/logout" {
			method = http.MethodPost
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d, want 401", method, path, response.Code)
		}
	}
	callback := httptest.NewRecorder()
	handler.ServeHTTP(callback, httptest.NewRequest(http.MethodGet, "/auth/callback", nil))
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("public callback returned %d, want redirect", callback.Code)
	}
}

func TestSubscriptionProfileSaveRejectsEndpointCredentialsAndClearsOldKey(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	name := fmt.Sprintf("t-subscription-save-%d", time.Now().UnixNano())
	id, err := m.pg.SaveProfile(&db.LLMProfile{Name: name, Format: "openai", Model: "test-model", APIKey: "synthetic-old-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
	save := func(format, key, baseURL, proxy, effort string) int {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"id": id, "name": name, "format": format, "model": "test-model",
			"api_key": key, "base_url": baseURL, "proxy": proxy, "reasoning_effort": effort,
		})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.pgSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/llm/profiles", bytes.NewReader(body)))
		return rec.Code
	}
	if got := save("chatgpt-plan", "synthetic-new-key", "", "", ""); got != http.StatusBadRequest {
		t.Fatalf("ChatGPT profile accepted API key: HTTP %d", got)
	}
	if got := save("chatgpt-plan", "", "https://example.invalid", "", ""); got != http.StatusBadRequest {
		t.Fatalf("ChatGPT profile accepted base URL: HTTP %d", got)
	}
	if got := save("claude-code", "", "", "http://proxy.example.invalid", ""); got != http.StatusBadRequest {
		t.Fatalf("Claude Code profile accepted proxy: HTTP %d", got)
	}
	profile, err := m.pg.ProfileByID(id)
	if err != nil || profile.APIKey != "synthetic-old-key" || profile.Format != "openai" {
		t.Fatalf("rejected saves changed original profile: %v, %+v", err, profile)
	}
	if got := save("chatgpt-plan", "", "", "", ""); got != http.StatusOK {
		t.Fatalf("keyless ChatGPT profile save returned HTTP %d", got)
	}
	profile, err = m.pg.ProfileByID(id)
	if err != nil || profile.APIKey != "" || profile.Format != "chatgpt-plan" {
		t.Fatalf("subscription save retained an API key: %v, %+v", err, profile)
	}
	if got := save("claude-code", "", "", "", "high"); got != http.StatusOK {
		t.Fatalf("Claude Code effort save returned HTTP %d", got)
	}
	profile, err = m.pg.ProfileByID(id)
	if err != nil || profile.Format != "claude-code" || profile.ReasoningEffort != "high" {
		t.Fatalf("Claude Code effort was not saved: %v, %+v", err, profile)
	}
	if got := save("claude-code", "", "", "", "invalid"); got != http.StatusBadRequest {
		t.Fatalf("unsupported Claude Code effort returned HTTP %d", got)
	}
}
