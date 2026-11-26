package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
