package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

func TestBedrockGatewayCompleteAndStream(t *testing.T) {
	const token = "synthetic-test-token"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
	if streamed != "streamed" || toolName != "lookup" || toolInput != `{"q":"x"}` || calls.Load() != 2 {
		t.Fatalf("stream = %q, tool = %q %q, calls = %d", streamed, toolName, toolInput, calls.Load())
	}
}

func TestBedrockGatewayStreamExceptionIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_ = eventstream.NewEncoder().Encode(w, eventstream.Message{Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("exception")}, {Name: ":event-type", Value: eventstream.StringValue("validationException")}}, Payload: []byte(`{"message":"synthetic-secret"}`)})
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
			if strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("stream exception exposed gateway response body")
			}
		}
	}
	if !sawError {
		t.Fatal("stream exception was treated as success")
	}
}

func TestBedrockGatewayErrorBodyIsRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"echoed synthetic-token and private-route"}`))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL+"/private-route", "synthetic-token", "")
	cfg.Stream = false
	_, _, err := TestConnection(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "synthetic-token") || strings.Contains(err.Error(), "private-route") {
		t.Fatalf("unsafe connection error: %v", err)
	}
}

func TestBedrockGatewayQuotaClassificationAfterRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, upstreamBody string
		wantStatus         int
	}{
		{"exhausted", `{"message":"quota_exceeded synthetic-secret"}`, http.StatusPaymentRequired},
		{"transient", `{"message":"rate limit reached synthetic-secret"}`, http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(tc.upstreamBody))
			}))
			defer server.Close()
			transport := quotaAwareTransport{base: &bedrockGatewayTransport{
				base: http.DefaultTransport, baseURL: server.URL, token: "synthetic-token", model: "test.model",
			}}
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/messages", strings.NewReader(`{"stream":false}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != tc.wantStatus || strings.Contains(string(body), "synthetic-secret") {
				t.Fatalf("status=%d body=%q err=%v", resp.StatusCode, body, err)
			}
		})
	}
}

func TestBedrockGatewayDoesNotFollowRedirectWithToken(t *testing.T) {
	var redirected atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
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
	if err == nil || redirected.Load() {
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

func TestBedrockGatewayChatCompletionCompleteAndStream(t *testing.T) {
	const okBody = `{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"OK","reasoning":"The user asked for OK.","role":"assistant"}}],"object":"chat.completion","usage":{"completion_tokens":33,"prompt_tokens":33}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "response-stream") {
			_, _ = w.Write([]byte(okBody))
			return
		}
		_, _ = w.Write([]byte(okBody))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	msg, stop, usage, err := p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}})
	if err != nil || msg.Text() != "OK" || stop != "end_turn" || usage.InputTokens != 33 || usage.OutputTokens != 33 {
		t.Fatalf("complete text=%q stop=%q usage=%+v err=%v", msg.Text(), stop, usage, err)
	}
	var thought string
	for _, b := range msg.Content {
		if b.Type == llm.BlockThinking {
			thought = b.Thinking
		}
	}
	if thought != "The user asked for OK." {
		t.Fatalf("thinking = %q", thought)
	}
	var streamed, thoughtStream string
	for ev, err := range p.Stream(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case llm.SETextDelta:
			streamed += ev.Text
		case llm.SEThinkingDelta:
			thoughtStream += ev.Text
		}
	}
	if streamed != "OK" || thoughtStream != "The user asked for OK." {
		t.Fatalf("json stream text=%q thinking=%q", streamed, thoughtStream)
	}
}

func TestBedrockGatewayChatCompletionChunkStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		enc := eventstream.NewEncoder()
		for _, payload := range []string{
			`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning":"think"},"finish_reason":null}]}`,
			`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":null}]}`,
			`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":""}}]}}]}`,
			`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		} {
			if err := enc.Encode(w, eventstream.Message{Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("event")}, {Name: ":event-type", Value: eventstream.StringValue("chunk")}}, Payload: []byte(payload)}); err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	var streamed, thought, toolName, toolInput, stop string
	var usage llm.Usage
	for ev, err := range p.Stream(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case llm.SETextDelta:
			streamed += ev.Text
		case llm.SEThinkingDelta:
			thought += ev.Text
		case llm.SEToolUseStart:
			toolName = ev.ToolName
		case llm.SEToolInputJSON:
			toolInput += ev.Text
		case llm.SEMessageDelta:
			stop = ev.StopReason
			usage = ev.Usage
		}
	}
	if streamed != "OK" || thought != "think" || toolName != "lookup" || toolInput != `{"q":"x"}` || stop != "tool_use" || usage.InputTokens != 3 || usage.OutputTokens != 2 {
		t.Fatalf("text=%q thinking=%q tool=%q %q stop=%q usage=%+v", streamed, thought, toolName, toolInput, stop, usage)
	}
}

func TestBedrockGatewayChatCompletionToolCall(t *testing.T) {
	const body = `{"object":"chat.completion","choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}},{"id":"call_2","type":"function","function":{"name":"fetch","arguments":"{\"u\":\"y\"}"}}]}}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	msg, stop, _, err := p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}})
	uses := msg.ToolUses()
	if err != nil || stop != "tool_use" || len(uses) != 2 || uses[0].Name != "lookup" || string(uses[0].Input) != `{"q":"x"}` || uses[1].Name != "fetch" || string(uses[1].Input) != `{"u":"y"}` {
		t.Fatalf("stop=%q uses=%+v err=%v", stop, uses, err)
	}
	// A gateway that answers a streaming request with a plain chat.completion.
	var names, inputs []string
	for ev, err := range p.Stream(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case llm.SEToolUseStart:
			names = append(names, ev.ToolName)
		case llm.SEToolInputJSON:
			inputs = append(inputs, ev.Text)
		case llm.SEMessageDelta:
			stop = ev.StopReason
		}
	}
	if strings.Join(names, ",") != "lookup,fetch" || strings.Join(inputs, ",") != `{"q":"x"},{"u":"y"}` || stop != "tool_use" {
		t.Fatalf("stream tools=%v inputs=%v stop=%q", names, inputs, stop)
	}
}

func TestBedrockGatewayConnectionErrorIsRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := server.URL + "/synthetic-secret-path"
	server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", baseURL, "synthetic-token", "")
	cfg.Stream = false
	cfg.Retry.ConnectAttempts = -1
	_, _, err := TestConnection(context.Background(), cfg)
	if err == nil || err.Error() != "Bedrock gateway connection failed" {
		t.Fatalf("error = %v", err)
	}
}

func TestBedrockGatewayReasoningOnlyConnectionError(t *testing.T) {
	const body = `{"choices":[{"finish_reason":"length","message":{"content":null,"reasoning":"still thinking about the reply"}}],"object":"chat.completion"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	cfg.Stream = false
	_, _, err := TestConnection(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "reasoning") || !strings.Contains(err.Error(), "max_tokens") || strings.Contains(err.Error(), "still thinking") {
		t.Fatalf("error = %v", err)
	}
}

func TestBedrockGatewayUnexpectedShapeIsStructural(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"synthetic-token","object":"weird"}`))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	cfg.Stream = false
	_, _, err := TestConnection(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "chat completion") || !strings.Contains(err.Error(), "object=weird") || strings.Contains(err.Error(), "synthetic-token") {
		t.Fatalf("error = %v", err)
	}
}

func TestBedrockGatewayRetriesChatCompletionRequest(t *testing.T) {
	const okBody = `{"choices":[{"finish_reason":"stop","message":{"content":"OK","role":"assistant"}}],"object":"chat.completion"}`
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), raw...))
		mu.Unlock()
		if bytes.Contains(raw, []byte("anthropic_version")) || bytes.Contains(raw, []byte("input_schema")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"rejected synthetic-secret"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	req := llm.CompletionRequest{
		System:    []string{"sys"},
		Messages:  []llm.Message{llm.UserText("hi")},
		Tools:     []llm.ToolSchema{{Name: "lookup", Description: "d", InputSchema: map[string]any{"type": "object"}}},
		MaxTokens: 100,
	}
	for range 2 {
		msg, _, _, err := p.Complete(context.Background(), req)
		if err != nil || msg.Text() != "OK" {
			t.Fatalf("complete text=%q err=%v", msg.Text(), err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("calls = %d, want anthropic 400, chat retry, then a direct chat request", len(bodies))
	}
	if !bytes.Contains(bodies[0], []byte("input_schema")) {
		t.Fatalf("first request was not an Anthropic tool body: %s", bodies[0])
	}
	for i, body := range bodies[1:] {
		var doc struct {
			AnthropicVersion string `json:"anthropic_version"`
			Messages         []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name       string          `json:"name"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.AnthropicVersion != "" || bytes.Contains(body, []byte("input_schema")) || bytes.Contains(body, []byte("synthetic-secret")) {
			t.Fatalf("call %d was not a chat completion: %s", i+2, body)
		}
		var user string
		sawSystem := false
		for _, m := range doc.Messages {
			if m.Role == "system" && m.Content == "sys" {
				sawSystem = true
			}
			if m.Role == "user" {
				user = m.Content
			}
		}
		if !sawSystem || user != "hi" || len(doc.Tools) != 1 || doc.Tools[0].Function.Name != "lookup" || !bytes.Contains(doc.Tools[0].Function.Parameters, []byte("object")) {
			t.Fatalf("call %d messages=%+v tools=%+v", i+2, doc.Messages, doc.Tools)
		}
	}
}

func TestBedrockGatewayChatRequestRejectionStaysRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"rejected synthetic-secret"}`))
	}))
	defer server.Close()
	cfg := ConfigFrom("bedrock-gateway", "test.model", server.URL, "synthetic-token", "")
	p, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = p.Complete(context.Background(), llm.CompletionRequest{
		Messages:  []llm.Message{llm.UserText("hi")},
		Tools:     []llm.ToolSchema{{Name: "lookup", Description: "d", InputSchema: map[string]any{"type": "object"}}},
		MaxTokens: 32,
	})
	if err == nil || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("error = %v", err)
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
