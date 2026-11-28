package mcphttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Every other test in this package uses httptest on 127.0.0.1. The guard is a
// default, not an absolute ban, so the suite opts out globally here; the tests
// below turn it back on explicitly to check the default.
func TestMain(m *testing.M) {
	if os.Getenv(AllowLocalTargetsEnv) == "" {
		_ = os.Setenv(AllowLocalTargetsEnv, "1")
	}
	os.Exit(m.Run())
}

func TestLocalMCPTargetBlockedByDefault(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the MCP endpoint was dialled despite the guard")
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := New(ctx, "blocked", srv.URL, nil, false, 1)
	if err == nil || !strings.Contains(err.Error(), "refusing to dial loopback") {
		t.Fatalf("streamable HTTP: want dial refusal, got %v", err)
	}
	_, err = NewSSE(ctx, "blocked", srv.URL+"/sse", nil, false)
	if err == nil || !strings.Contains(err.Error(), "refusing to dial loopback") {
		t.Fatalf("legacy SSE: want dial refusal, got %v", err)
	}
}

func TestLocalMCPTargetAllowedWithOptOut(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":"local","version":"0"}}}`, protocolVersion)
	}))
	defer srv.Close()

	c, err := New(context.Background(), "local", srv.URL, nil, false, 1)
	if err != nil {
		t.Fatalf("connect with opt-out: %v", err)
	}
	_ = c.Close()
}
