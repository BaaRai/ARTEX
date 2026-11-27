package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

type planTokenSource string

func (s planTokenSource) AccessToken(context.Context) (string, error) { return string(s), nil }

type failingPlanTokenSource struct{}

func (failingPlanTokenSource) AccessToken(context.Context) (string, error) {
	return "", errors.New("not connected")
}

func TestChatGPTPlanProviderShapesStreamingRequestAndComplete(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("endpoint: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer oauth-test" {
			t.Errorf("authorization: %q", r.Header.Get("Authorization"))
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if string(body["stream"]) != "true" || string(body["store"]) != "false" {
			t.Errorf("required plan flags: %v", body)
		}
		for _, forbidden := range []string{"max_output_tokens", "temperature", "tools", "previous_response_id"} {
			if _, ok := body[forbidden]; ok {
				t.Errorf("unsupported %q field sent", forbidden)
			}
		}
		var input []map[string]any
		if err := json.Unmarshal(body["input"], &input); err != nil {
			t.Error(err)
			return
		}
		if len(input) != 3 || input[0]["type"] != "additional_tools" || input[0]["role"] != "developer" || input[1]["role"] != "user" || input[2]["role"] != "assistant" {
			t.Errorf("input history/tools: %+v", input)
		}
		tools, ok := input[0]["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("additional tools: %+v", input[0])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n")
	}))
	defer srv.Close()
	cfg := Config{Model: "gpt-6.1-sol", Stream: false}
	p, err := newChatGPTPlanProvider(cfg, planTokenSource("oauth-test"), srv.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.5
	req := llm.CompletionRequest{System: []string{"Follow directions"}, Messages: []llm.Message{llm.UserText("hello"), {Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("previous")}}}, Tools: []llm.ToolSchema{{Name: "Read", Description: "Read file", InputSchema: map[string]any{"type": "object"}}}, MaxTokens: 512, Temperature: &temp}
	msg, reason, usage, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text() != "done" || reason != "end_turn" || usage.InputTokens != 12 || usage.OutputTokens != 3 {
		t.Fatalf("response: %q %q %+v", msg.Text(), reason, usage)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestChatGPTPlanProviderRejectsOtherBaseURLAndMissingLogin(t *testing.T) {
	if _, err := NewChatGPTPlanProvider(Config{BaseURL: "https://example.com/v1"}, planTokenSource("token")); err == nil {
		t.Fatal("accepted alternate endpoint for OAuth bearer")
	}
	p, err := newChatGPTPlanProvider(Config{Model: "gpt-6.1-sol", Retry: RetryConfig{ConnectAttempts: -1}}, failingPlanTokenSource{}, "http://127.0.0.1:1/v1")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hi")}})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("missing login error: %v", err)
	}
}

func TestChatGPTPlanProviderRejectsIncompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
	}))
	defer srv.Close()
	p, err := newChatGPTPlanProvider(Config{Model: "gpt-6.1-sol", Retry: RetryConfig{ConnectAttempts: -1}}, planTokenSource("oauth-test"), srv.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hi")}})
	if err == nil || !strings.Contains(err.Error(), "without response.completed") {
		t.Fatalf("incomplete stream error: %v", err)
	}
}

func TestChatGPTPlanProviderSendsEmptyInputArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if string(body["input"]) != "[]" {
			t.Errorf("input must be an array, got %s", body["input"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()
	p, err := newChatGPTPlanProvider(Config{Model: "gpt-6.1-sol"}, planTokenSource("oauth-test"), srv.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range p.Stream(t.Context(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestChatGPTPlanProviderSurfacesNestedUsageLimitBeforeAndAfterDeltas(t *testing.T) {
	for _, withDelta := range []bool{false, true} {
		t.Run(fmt.Sprint("delta=", withDelta), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if withDelta {
					fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
				}
				fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\",\"message\":\"usage limit reached\"}}}\n\n")
			}))
			defer srv.Close()
			p, err := newChatGPTPlanProvider(Config{Model: "gpt-6.1-sol", Retry: RetryConfig{ConnectAttempts: -1}}, planTokenSource("oauth-test"), srv.URL+"/v1")
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hi")}})
			if err == nil || !strings.Contains(err.Error(), "subscription_sharing_usage_limit_exceeded") {
				t.Fatalf("quota code lost: %v", err)
			}
		})
	}
}

func TestChatGPTPlanProviderTreatsResponseIncompleteAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n")
	}))
	defer srv.Close()
	p, err := newChatGPTPlanProvider(Config{Model: "gpt-6.1-sol", Retry: RetryConfig{ConnectAttempts: -1}}, planTokenSource("oauth-test"), srv.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = p.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hi")}})
	if err == nil || !strings.Contains(err.Error(), "response incomplete: max_output_tokens") {
		t.Fatalf("incomplete stream accepted: %v", err)
	}
}
