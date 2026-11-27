package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"

	"github.com/Autumn-27/artex/chatgptplan"
	"github.com/Autumn-27/norma/llm"
)

const chatGPTPlanAPI = "https://api.openai.com/v1"

// NewChatGPTPlanProvider runs ARTEX's normal Norma Responses adapter using an
// ARTEX-owned Sign in with ChatGPT connection. The OAuth bearer never enters a
// saved LLM profile or Norma config, and requests are confined to the public
// OpenAI Responses endpoint.
func NewChatGPTPlanProvider(c Config, source chatgptplan.TokenSource) (llm.Provider, error) {
	if strings.TrimSpace(c.BaseURL) != "" && strings.TrimRight(c.BaseURL, "/") != chatGPTPlanAPI {
		return nil, errors.New("ChatGPT plan usage only supports https://api.openai.com/v1")
	}
	return newChatGPTPlanProvider(c, source, chatGPTPlanAPI)
}

// newChatGPTPlanProvider accepts a loopback endpoint for deterministic tests.
func newChatGPTPlanProvider(c Config, source chatgptplan.TokenSource, baseURL string) (llm.Provider, error) {
	if source == nil {
		return nil, errors.New("ChatGPT plan token source is required")
	}
	client, err := quotaAwareHTTPClient(c.Proxy, c.SessionHeaderKey, c.UserAgent)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/responses")
	if err != nil {
		return nil, err
	}
	client.Transport = &chatGPTPlanTransport{base: client.Transport, source: source, endpoint: endpoint.String()}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	lc := llm.Config{
		Format: llm.FormatOpenAIResponses, BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey: "oauth-token-supplied-by-transport", Model: c.Model, HTTPClient: client,
		ThinkingType: c.ThinkingType, ReasoningEffort: c.ReasoningEffort,
		MaxRetries: c.Retry.ConnectAttempts, RetryInterval: c.Retry.ConnectInterval,
	}
	if c.RatePerSecond > 0 || c.RatePerMinute > 0 {
		lc.RateLimit = &llm.RateLimit{PerSecond: c.RatePerSecond, PerMinute: c.RatePerMinute}
	}
	inner, err := llm.NewProvider(lc)
	if err != nil {
		return nil, err
	}
	return &chatGPTPlanProvider{inner: inner}, nil
}

type chatGPTPlanProvider struct{ inner llm.Provider }

func (p *chatGPTPlanProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		completed := false
		for ev, err := range p.inner.Stream(ctx, req) {
			if err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			if ev.Type == llm.SEMessageDelta && ev.StopReason != "" {
				completed = true
			}
			if !yield(ev, nil) {
				return
			}
		}
		if !completed {
			yield(llm.StreamEvent{}, errors.New("ChatGPT stream ended without response.completed"))
		}
	}
}

// Complete still uses streaming on the wire, as required for plan usage, then
// assembles a complete Norma message for ARTEX callers using non-streaming mode.
func (p *chatGPTPlanProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for ev, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(ev)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

type chatGPTPlanTransport struct {
	base     http.RoundTripper
	source   chatgptplan.TokenSource
	endpoint string
}

func (t *chatGPTPlanTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.URL.String() != t.endpoint {
		return nil, errors.New("ChatGPT plan request escaped the public Responses endpoint")
	}
	token, err := t.source.AccessToken(req.Context())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("ChatGPT plan access token is empty")
	}
	if req.GetBody == nil {
		return nil, errors.New("ChatGPT plan request body is not replayable")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(body, 16<<20))
	body.Close()
	if err != nil {
		return nil, err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode ChatGPT plan request: %w", err)
	}
	var input []json.RawMessage
	if err := json.Unmarshal(payload["input"], &input); err != nil {
		return nil, fmt.Errorf("decode ChatGPT plan input: %w", err)
	}
	if input == nil {
		input = []json.RawMessage{}
	}
	if tools := payload["tools"]; len(tools) > 0 {
		var toolList []json.RawMessage
		if err := json.Unmarshal(tools, &toolList); err != nil {
			return nil, fmt.Errorf("decode ChatGPT plan tools: %w", err)
		}
		if len(toolList) > 0 {
			item, err := json.Marshal(map[string]any{"type": "additional_tools", "role": "developer", "tools": toolList})
			if err != nil {
				return nil, err
			}
			input = append([]json.RawMessage{item}, input...)
		}
	}
	delete(payload, "tools")
	// The preview route rejects these fields even though Norma sends them for
	// ordinary API-key Responses requests. The same history remains in input.
	for _, key := range []string{"max_output_tokens", "temperature", "background", "conversation", "max_tool_calls", "metadata", "moderation", "multi_agent", "prompt", "prompt_cache_retention", "safety_identifier", "top_logprobs", "top_p", "truncation", "user", "previous_response_id"} {
		delete(payload, key)
	}
	payload["input"], err = json.Marshal(input)
	if err != nil {
		return nil, err
	}
	payload["stream"] = json.RawMessage("true")
	payload["store"] = json.RawMessage("false")
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+token)
	clone.Header.Set("Content-Type", "application/json")
	clone.Header.Set("Accept", "text/event-stream")
	clone.Body = io.NopCloser(bytes.NewReader(encoded))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
	clone.ContentLength = int64(len(encoded))
	resp, err := t.base.RoundTrip(clone)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode == http.StatusOK {
		resp.Body = normalizeChatGPTPlanSSE(resp.Body)
	}
	return resp, nil
}

type planSSEBody struct {
	*io.PipeReader
	source io.ReadCloser
}

func (b *planSSEBody) Close() error {
	err := b.source.Close()
	_ = b.PipeReader.Close()
	return err
}

// Norma's Responses parser only reads a top-level error.message and treats
// response.incomplete as a normal turn. Plan usage reports quota in the nested
// response.error.code, so normalize these terminal frames before Norma parses
// them. Frames that carry text/tools pass through without buffering the stream.
func normalizeChatGPTPlanSSE(source io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		r := bufio.NewReader(source)
		var frame []byte
		for {
			line, err := r.ReadBytes('\n')
			frame = append(frame, line...)
			if len(frame) > 16<<20 {
				writer.CloseWithError(errors.New("ChatGPT SSE frame exceeds 16 MiB"))
				return
			}
			if bytes.Equal(bytes.TrimSpace(line), nil) || err == io.EOF {
				if len(frame) > 0 {
					out := normalizePlanFrame(frame)
					if _, writeErr := writer.Write(out); writeErr != nil {
						return
					}
					frame = nil
				}
			}
			if err != nil {
				if err != io.EOF {
					writer.CloseWithError(err)
				}
				return
			}
		}
	}()
	return &planSSEBody{PipeReader: reader, source: source}
}

func normalizePlanFrame(frame []byte) []byte {
	var eventName string
	var data []byte
	for _, line := range bytes.Split(frame, []byte{'\n'}) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			eventName = strings.TrimSpace(string(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimSpace(line[len("data:"):])...)
		}
	}
	var payload struct {
		Type     string `json:"type"`
		Response struct {
			Error             *struct{ Code, Message string } `json:"error"`
			IncompleteDetails *struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return frame
	}
	if eventName == "" {
		eventName = payload.Type
	}
	var message string
	switch eventName {
	case "response.failed":
		if payload.Response.Error == nil {
			return frame
		}
		message = strings.TrimSpace(payload.Response.Error.Code + ": " + payload.Response.Error.Message)
	case "response.incomplete":
		message = "response incomplete"
		if payload.Response.IncompleteDetails != nil && payload.Response.IncompleteDetails.Reason != "" {
			message += ": " + payload.Response.IncompleteDetails.Reason
		}
	default:
		return frame
	}
	rewritten, _ := json.Marshal(map[string]any{"type": "response.failed", "error": map[string]string{"message": message}})
	return append(append([]byte("event: response.failed\ndata: "), rewritten...), []byte("\n\n")...)
}
