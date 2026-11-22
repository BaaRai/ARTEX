package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

func TestBedrockGatewayCompleteAndStream(t *testing.T) {
	const token = "synthetic-test-token"
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/bedrock/model/test.model/invoke") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("X-Example-Route"); got != "demo" {
			t.Errorf("custom header = %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("unexpected x-api-key")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["model"]; ok {
			t.Error("model remained in body")
		}
		if _, ok := body["stream"]; ok {
			t.Error("stream remained in body")
		}
		if string(body["anthropic_version"]) != `"bedrock-2023-05-31"` {
			t.Errorf("version = %s", body["anthropic_version"])
		}
		if !strings.Contains(r.URL.Path, "response-stream") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		enc := eventstream.NewEncoder()
		for i, payload := range []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streamed"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			if i == 0 {
				payload = `{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(payload)) + `"}`
			}
			err := enc.Encode(w, eventstream.Message{Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("event")}, {Name: ":event-type", Value: eventstream.StringValue("chunk")}}, Payload: []byte(payload)})
			if err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL+"/bedrock", token, "")
	cfg.CustomHeaders = "X-Example-Route: demo"
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}
	msg, _, _, err := p.Complete(context.Background(), req)
	if err != nil || msg.Text() != "done" {
		t.Fatalf("complete: %q, %v", msg.Text(), err)
	}
	var streamed, toolName, toolInput string
	for ev, err := range p.Stream(context.Background(), req) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case llm.SETextDelta:
			streamed += ev.Text
		case llm.SEToolUseStart:
			toolName = ev.ToolName
		case llm.SEToolInputJSON:
			toolInput += ev.Text
		}
	}
	if streamed != "streamed" || toolName != "lookup" || toolInput != `{"q":"x"}` || calls != 2 {
		t.Fatalf("stream = %q, tool = %q %q, calls = %d", streamed, toolName, toolInput, calls)
	}
}

func TestBedrockGatewayStreamExceptionIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_ = eventstream.NewEncoder().Encode(w, eventstream.Message{Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("exception")}, {Name: ":event-type", Value: eventstream.StringValue("validationException")}}, Payload: []byte(`{"message":"synthetic failure"}`)})
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	var sawError bool
	for _, err := range p.Stream(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}) {
		if err != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("stream exception was treated as success")
	}
}

func TestBedrockGatewayDoesNotFollowRedirectWithToken(t *testing.T) {
	redirected := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", first.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}})
	if err == nil || redirected {
		t.Fatal("redirect followed or treated as success")
	}
}

func TestBedrockGatewayEscapesModelARN(t *testing.T) {
	const model = "arn:aws:bedrock:region:123456789012:foundation-model/test.model"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.EscapedPath(), "foundation-model%2Ftest.model") {
			t.Errorf("model path not escaped: %s", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", model, server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}); err != nil {
		t.Fatal(err)
	}
}

func TestBedrockGatewayRejectsUnsafeConfig(t *testing.T) {
	for _, cfg := range []Config{
		ConfigFrom("bedrock-gateway", "model", "", "token", ""),
		ConfigFrom("bedrock-gateway", "model", "https://example.com", "", ""),
		ConfigFrom("bedrock-gateway", "model", "https://example.com", "token", ""),
	} {
		if cfg.BaseURL != "" && cfg.APIKey != "" {
			cfg.CustomHeaders = "Authorization: stolen"
		}
		if _, err := cfg.NewProvider(); err == nil {
			t.Errorf("accepted unsafe config: %+v", cfg)
		}
	}
	if _, err := ParseBedrockGatewayHeaders("X-Test: ok\nInjected: x\r\nBad: y\x00"); err == nil {
		t.Fatal("accepted control characters")
	}
}
