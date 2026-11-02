package mcphttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A Streamable HTTP server whose slow_call tool takes 3s, so the client's
// per-request timeout can be observed: 1s cuts the call off, 10s lets it finish.
func TestStreamableClientRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"t","version":"0"}}}`, req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"slow_call","description":"slow","inputSchema":{"type":"object"}}]}}`, req.ID)
		case "tools/call":
			time.Sleep(3 * time.Second)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"done"}]}}`, req.ID)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// timeout_seconds=1: the slow call is cut off well before the server answers.
	c, err := New(ctx, "t", srv.URL, nil, false, 1)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := c.Tools(ctx); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	start := time.Now()
	if _, err := c.Call(ctx, "slow_call", nil); err == nil {
		t.Fatal("expected the 1s-timeout call to fail")
	} else if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("call not cut off by the timeout: %v", elapsed)
	}
	_ = c.Close()

	// timeout_seconds=10: the same slow call completes.
	c2, err := New(ctx, "t", srv.URL, nil, false, 10)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	got, err := c2.Call(ctx, "slow_call", nil)
	if err != nil || got != "done" {
		t.Fatalf("Call() = %q, %v", got, err)
	}
	_ = c2.Close()

	// timeout_seconds=0: falls back to the 120s default (call completes).
	c3, err := New(ctx, "t", srv.URL, nil, false, 0)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got, err := c3.Call(ctx, "slow_call", nil); err != nil || got != "done" {
		t.Fatalf("default-timeout Call() = %q, %v", got, err)
	}
	_ = c3.Close()
}

func TestStreamableClientProtocolVersions(t *testing.T) {
	for _, supported := range []string{"2025-11-25", "2025-06-18"} {
		t.Run(supported, func(t *testing.T) {
			var attempts []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req rpcRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				version := r.Header.Get("MCP-Protocol-Version")
				if req.Method == "initialize" {
					attempts = append(attempts, version)
				}
				if version != supported {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"code":"UNSUPPORTED_PROTOCOL_VERSION"}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch req.Method {
				case "initialize":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":"t","version":"0"}}}`, req.ID, supported)
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
				case "tools/list":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, req.ID)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			c, err := New(context.Background(), "t", srv.URL, nil, false, 0)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer c.Close()
			if _, err := c.Tools(context.Background()); err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			if attempts[0] != protocolVersion {
				t.Fatalf("first version = %q", attempts[0])
			}
			if supported == "2025-06-18" && (len(attempts) != 2 || attempts[1] != supported) {
				t.Fatalf("fallback attempts = %v", attempts)
			}
		})
	}
}

// TestStreamableClientVersionRejectionForms covers the ways a 2025-06-18-only
// server can reject 2025-11-25: the spec's HTTP 400 on the header, the spec's
// JSON-RPC -32602 error, and an unrelated failure that must not trigger a retry.
func TestStreamableClientVersionRejectionForms(t *testing.T) {
	cases := []struct {
		name     string
		reject   func(w http.ResponseWriter, id int)
		fallback bool
	}{
		{"http 400 header", func(w http.ResponseWriter, _ int) {
			http.Error(w, "Bad Request: Unsupported protocol version", http.StatusBadRequest)
		}, true},
		{"json-rpc -32602", func(w http.ResponseWriter, id int) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":"Unsupported protocol version","data":{"supported":["2025-06-18"],"requested":"2025-11-25"}}}`, id)
		}, true},
		{"unrelated 401", func(w http.ResponseWriter, _ int) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attempts []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req rpcRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req.Method == "initialize" {
					attempts = append(attempts, r.Header.Get("MCP-Protocol-Version"))
				}
				if r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
					tc.reject(w, req.ID)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch req.Method {
				case "initialize":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"t","version":"0"}}}`, req.ID)
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			c, err := New(context.Background(), "t", srv.URL, nil, false, 0)
			if !tc.fallback {
				if err == nil {
					_ = c.Close()
					t.Fatal("expected connect error")
				}
				if len(attempts) != 1 {
					t.Fatalf("unrelated error retried: attempts = %v", attempts)
				}
				return
			}
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer c.Close()
			if len(attempts) != 2 || attempts[1] != "2025-06-18" {
				t.Fatalf("fallback attempts = %v", attempts)
			}
			if c.protocol != "2025-06-18" {
				t.Fatalf("negotiated protocol = %q", c.protocol)
			}
		})
	}
}
