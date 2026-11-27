package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The connection test must send a bare chat request. Models given the SDK's
// background-task tools answered the test by calling TaskOutput instead of OK (#526).
func TestConnectionSendsNoTools(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	cfg := ConfigFrom("ollama", "m", server.URL+"/v1", "", "")
	cfg.Stream = false
	_, reply, err := TestConnection(context.Background(), cfg)
	if err != nil || reply != "OK" {
		t.Fatalf("TestConnection = %q, %v; want OK", reply, err)
	}
	var req struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request body: %v\n%s", err, body)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("connection test sent %d tools, want none:\n%s", len(req.Tools), body)
	}
}

// Regression for #526: Ollama returning a tool call (with or without accompanying
// text) or finish_reason="tool_calls", or whitespace-only content, must fail
// TestConnection instead of reporting Connected.
func TestConnectionRejectsToolCallsAndWhitespaceReplies(t *testing.T) {
	cases := []struct {
		name       string
		stream     bool
		statusBody string
		wantErrSub string
	}{
		{
			name:       "ollama_nonstream_tool_call_with_text",
			stream:     false,
			statusBody: `{"id":"x","object":"chat.completion","model":"baronllm","choices":[{"index":0,"message":{"role":"assistant","content":"OK","tool_calls":[{"id":"call_1","type":"function","function":{"name":"TaskOutput","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`,
			wantErrSub: "tool call",
		},
		{
			name:       "ollama_nonstream_tool_call_only",
			stream:     false,
			statusBody: `{"id":"x","object":"chat.completion","model":"baronllm","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"TaskOutput","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`,
			wantErrSub: "tool call",
		},
		{
			name:       "ollama_nonstream_finish_reason_tool_calls_without_array",
			stream:     false,
			statusBody: `{"id":"x","object":"chat.completion","model":"baronllm","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22}}`,
			wantErrSub: "tool call",
		},
		{
			name:   "ollama_stream_tool_call_with_text",
			stream: true,
			statusBody: strings.Join([]string{
				`data: {"id":"x","object":"chat.completion.chunk","model":"baronllm","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"}}]}`,
				`data: {"id":"x","object":"chat.completion.chunk","model":"baronllm","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"TaskOutput","arguments":"{}"}}]}}]}`,
				`data: {"id":"x","object":"chat.completion.chunk","model":"baronllm","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
				``,
			}, "\n\n"),
			wantErrSub: "tool call",
		},
		{
			name:       "ollama_whitespace_only_content",
			stream:     false,
			statusBody: `{"id":"x","object":"chat.completion","model":"baronllm","choices":[{"index":0,"message":{"role":"assistant","content":"   \n\t  "},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}}`,
			wantErrSub: "no content",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = w.Write([]byte(tc.statusBody))
			}))
			defer srv.Close()

			cfg := ConfigFrom("ollama", "baronllm", srv.URL+"/v1", "", "")
			cfg.Stream = tc.stream
			_, reply, err := TestConnection(context.Background(), cfg)
			if err == nil {
				t.Fatalf("expected TestConnection to reject %s, got Connected with reply=%q", tc.name, reply)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantErrSub) {
				t.Fatalf("unexpected error for %s: %v (want substring %q)", tc.name, err, tc.wantErrSub)
			}
		})
	}
}
