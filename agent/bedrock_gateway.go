package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

// BedrockGatewayHeaders are newline-separated HTTP headers, stored encrypted with
// the profile. The bearer token is held separately in the existing API key field.
func ParseBedrockGatewayHeaders(raw string) (http.Header, error) {
	h := make(http.Header)
	if len(raw) > 8192 {
		return nil, errors.New("custom headers exceed 8 KiB")
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(h) >= 20 {
			return nil, errors.New("too many custom headers")
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || !validHeaderName(name) || !validHeaderValue(value) {
			return nil, errors.New("invalid custom header")
		}
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "host", "x-api-key", "content-type", "accept", "content-length", "transfer-encoding", "connection", "proxy-connection", "keep-alive", "te", "trailer", "upgrade", "cookie", "anthropic-version", "x-amzn-bedrock-accept", "x-amzn-bedrock-content-type":
			return nil, fmt.Errorf("custom header %q is reserved", name)
		}
		name = textproto.CanonicalMIMEHeaderKey(name)
		if _, exists := h[name]; exists {
			return nil, fmt.Errorf("duplicate custom header %q", name)
		}
		h.Set(name, value)
	}
	return h, nil
}

func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

func validHeaderValue(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func validateBedrockGateway(baseURL, token, model, headers string) (http.Header, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return nil, errors.New("Bedrock gateway requires an HTTPS base URL (HTTP only for localhost)")
	}
	if !validHeaderValue(strings.TrimSpace(token)) {
		return nil, errors.New("Bedrock gateway requires a bearer token")
	}
	if strings.TrimSpace(model) == "" || strings.ContainsAny(model, "?#\r\n") {
		return nil, errors.New("Bedrock gateway requires a valid model ID")
	}
	return ParseBedrockGatewayHeaders(headers)
}

// bedrockGatewayTransport adapts Norma's Anthropic Messages request/response
// to Bedrock InvokeModel. It never changes the destination host.
type bedrockGatewayTransport struct {
	base    http.RoundTripper
	baseURL string
	token   string
	model   string
	headers http.Header

	mu          sync.Mutex
	chatRequest bool // endpoint rejected the Anthropic body; send chat completions
}

func (t *bedrockGatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != strings.TrimRight(mustURLPath(t.baseURL), "/")+"/v1/messages" {
		return nil, errors.New("unexpected Bedrock gateway request path")
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	var streaming bool
	if err := json.Unmarshal(body["stream"], &streaming); err != nil {
		return nil, errors.New("missing stream mode")
	}
	delete(body, "stream")
	delete(body, "model")
	body["anthropic_version"] = json.RawMessage(`"bedrock-2023-05-31"`)
	raw, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(t.baseURL)
	basePath, escapedBasePath := strings.TrimRight(u.Path, "/"), strings.TrimRight(u.EscapedPath(), "/")
	u.Path = basePath + "/model/" + t.model + "/invoke"
	u.RawPath = escapedBasePath + "/model/" + url.PathEscape(t.model) + "/invoke"
	if streaming {
		u.Path += "-with-response-stream"
		u.RawPath += "-with-response-stream"
	}
	t.mu.Lock()
	preferChat := t.chatRequest
	t.mu.Unlock()
	sentChat := false
	if preferChat {
		if rewritten, ok := anthropicRequestToChatCompletion(raw); ok {
			raw, sentChat = rewritten, true
		}
	}
	resp, err := t.invoke(req, u, raw, streaming)
	if err != nil {
		return resp, err
	}
	// A tool-bearing Anthropic body is rejected by OpenAI-shaped InvokeModel
	// endpoints. Retry once as a chat completion and keep that shape.
	if resp.StatusCode == http.StatusBadRequest && !sentChat {
		if rewritten, ok := anthropicRequestToChatCompletion(raw); ok {
			_ = resp.Body.Close()
			resp, err = t.invoke(req, u, rewritten, streaming)
			if err != nil {
				return resp, err
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				t.mu.Lock()
				t.chatRequest = true
				t.mu.Unlock()
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Gateways may echo authorization material in error bodies or headers.
		// Keep the status for callers and retries without exposing that echo.
		// Preserve only the quota classification used by the outer transport.
		quotaExhausted := false
		if resp.StatusCode == http.StatusTooManyRequests {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			quotaExhausted = IsQuotaExhaustedMessage(string(raw))
		}
		_ = resp.Body.Close()
		message := fmt.Sprintf("Bedrock gateway returned HTTP %d", resp.StatusCode)
		if quotaExhausted {
			message += ": quota_exceeded"
		}
		body := []byte(fmt.Sprintf(`{"message":%q}`, message))
		copy := *resp
		copy.Body = io.NopCloser(bytes.NewReader(body))
		copy.ContentLength = int64(len(body))
		copy.Header = make(http.Header)
		copy.Header.Set("Content-Type", "application/json")
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			copy.Header.Set("Retry-After", retryAfter)
		}
		return &copy, nil
	}
	if !streaming {
		rawBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		adapted, err := rewriteBedrockModelJSON(rawBody)
		if err != nil {
			return nil, err
		}
		return jsonResponse(resp, adapted), nil
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "application/vnd.amazon.eventstream") {
		rawBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		sse, ok, err := chatCompletionToSSE(rawBody)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &bedrockGatewayError{"Bedrock gateway returned a non-eventstream response"}
		}
		return sseResponse(resp, sse), nil
	}
	reader, writer := io.Pipe()
	go func() {
		defer resp.Body.Close()
		defer writer.Close()
		decoder := eventstream.NewDecoder()
		chat := &chatChunkWriter{w: writer}
		sawChat := false
		for {
			frame, err := decoder.Decode(resp.Body, nil)
			if err == io.EOF {
				if sawChat {
					if err := chat.close(); err != nil {
						writer.CloseWithError(err)
					}
				}
				return
			}
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			if kind := fmt.Sprint(frame.Headers.Get(":message-type")); kind == "exception" || kind == "error" {
				writer.CloseWithError(errors.New("Bedrock gateway stream exception"))
				return
			}
			if fmt.Sprint(frame.Headers.Get(":message-type")) != "event" || fmt.Sprint(frame.Headers.Get(":event-type")) != "chunk" {
				writer.CloseWithError(errors.New("unexpected Bedrock gateway event frame"))
				return
			}
			payload := frame.Payload
			var wrapper struct {
				Bytes string `json:"bytes"`
			}
			if json.Unmarshal(payload, &wrapper) == nil && wrapper.Bytes != "" {
				payload, err = base64.StdEncoding.DecodeString(wrapper.Bytes)
				if err != nil {
					writer.CloseWithError(err)
					return
				}
			}
			switch bedrockPayloadKind(payload) {
			case "chat":
				sawChat = true
				if err = chat.write(payload); err != nil {
					writer.CloseWithError(err)
					return
				}
			case "anthropic":
				if sawChat {
					writer.CloseWithError(errors.New("invalid Bedrock gateway event"))
					return
				}
				var compact bytes.Buffer
				if err = json.Compact(&compact, payload); err != nil {
					writer.CloseWithError(err)
					return
				}
				var event struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(payload, &event)
				if _, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, compact.Bytes()); err != nil {
					return
				}
			default:
				writer.CloseWithError(errors.New("invalid Bedrock gateway event"))
				return
			}
		}
	}()
	copy := *resp
	copy.Body = &bedrockStreamBody{PipeReader: reader, upstream: resp.Body}
	copy.Header = cloneHeader(resp.Header)
	copy.Header.Set("Content-Type", "text/event-stream")
	copy.Header.Del("Content-Length")
	copy.ContentLength = -1
	return &copy, nil
}

func jsonResponse(resp *http.Response, body []byte) *http.Response {
	copy := *resp
	copy.Body = io.NopCloser(bytes.NewReader(body))
	copy.ContentLength = int64(len(body))
	copy.Header = cloneHeader(resp.Header)
	copy.Header.Set("Content-Type", "application/json")
	return &copy
}

func sseResponse(resp *http.Response, sse string) *http.Response {
	copy := *resp
	copy.Body = io.NopCloser(strings.NewReader(sse))
	copy.Header = cloneHeader(resp.Header)
	copy.Header.Set("Content-Type", "text/event-stream")
	copy.Header.Del("Content-Length")
	copy.ContentLength = -1
	return &copy
}

func cloneHeader(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		out = make(http.Header)
	}
	return out
}

func (t *bedrockGatewayTransport) invoke(req *http.Request, u *url.URL, raw []byte, streaming bool) (*http.Response, error) {
	q := req.Clone(req.Context())
	q.URL = u
	q.Host = u.Host
	q.Body = io.NopCloser(bytes.NewReader(raw))
	q.ContentLength = int64(len(raw))
	q.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	q.Header = req.Header.Clone()
	q.Header.Del("x-api-key")
	q.Header.Del("anthropic-version")
	q.Header.Set("Authorization", "Bearer "+t.token)
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("X-Amzn-Bedrock-Accept", "application/json")
	q.Header.Set("Accept", "application/json")
	if streaming {
		q.Header.Set("Accept", "application/vnd.amazon.eventstream")
	}
	for name, values := range t.headers {
		q.Header[name] = append([]string(nil), values...)
	}
	return t.base.RoundTrip(q)
}

type anthInMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthInBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type oaOutMessage struct {
	Role       string      `json:"role"`
	Content    string      `json:"content,omitempty"`
	Reasoning  string      `json:"reasoning,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []oaOutCall `json:"tool_calls,omitempty"`
}

type oaOutCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaOutTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type chatInvokeRequest struct {
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	Stop        []string       `json:"stop,omitempty"`
	Messages    []oaOutMessage `json:"messages"`
	Tools       []oaOutTool    `json:"tools,omitempty"`
}

// anthropicRequestToChatCompletion rebuilds an Anthropic Messages body as an
// OpenAI chat completion.
func anthropicRequestToChatCompletion(raw []byte) ([]byte, bool) {
	var doc struct {
		MaxTokens     int             `json:"max_tokens"`
		Temperature   *float64        `json:"temperature"`
		StopSequences []string        `json:"stop_sequences"`
		System        json.RawMessage `json:"system"`
		Messages      []anthInMessage `json:"messages"`
		Tools         []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, false
	}
	var msgs []oaOutMessage
	if system := jsonText(doc.System); system != "" {
		msgs = append(msgs, oaOutMessage{Role: "system", Content: system})
	}
	for _, m := range doc.Messages {
		msgs = append(msgs, openAIMessages(m)...)
	}
	if len(msgs) == 0 {
		return nil, false
	}
	out := chatInvokeRequest{
		MaxTokens:   doc.MaxTokens,
		Temperature: doc.Temperature,
		Stop:        doc.StopSequences,
		Messages:    msgs,
	}
	for _, tool := range doc.Tools {
		if tool.Name == "" {
			continue
		}
		params := tool.InputSchema
		if len(params) == 0 || string(params) == "null" {
			params = json.RawMessage(`{"type":"object"}`)
		}
		var ot oaOutTool
		ot.Type = "function"
		ot.Function.Name = tool.Name
		ot.Function.Description = tool.Description
		ot.Function.Parameters = params
		out.Tools = append(out.Tools, ot)
	}
	rewritten, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}

func jsonStarts(raw json.RawMessage, c byte) bool {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	return len(raw) > 0 && raw[0] == c
}

func openAIMessages(m anthInMessage) []oaOutMessage {
	if !jsonStarts(m.Content, '[') {
		text := jsonText(m.Content)
		if text == "" && m.Role == "" {
			return nil
		}
		if m.Role == "assistant" && text == "" {
			text = "…"
		}
		if text == "" {
			return nil
		}
		return []oaOutMessage{{Role: m.Role, Content: text}}
	}
	var blocks []anthInBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return nil
	}
	if m.Role == "assistant" {
		msg := oaOutMessage{Role: "assistant"}
		var text strings.Builder
		var reasoning strings.Builder
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "thinking":
				reasoning.WriteString(b.Thinking)
			case "tool_use":
				call := oaOutCall{ID: b.ID, Type: "function"}
				if call.ID == "" {
					call.ID = fmt.Sprintf("tool_%d", len(msg.ToolCalls))
				}
				call.Function.Name = b.Name
				call.Function.Arguments = toolArguments(b.Input)
				msg.ToolCalls = append(msg.ToolCalls, call)
			}
		}
		msg.Content = text.String()
		msg.Reasoning = reasoning.String()
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			msg.Content = "…"
		}
		return []oaOutMessage{msg}
	}
	var text strings.Builder
	var toolMsgs []oaOutMessage
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_result":
			toolMsgs = append(toolMsgs, oaOutMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: jsonText(b.Content)})
		}
	}
	if s := text.String(); s != "" {
		toolMsgs = append(toolMsgs, oaOutMessage{Role: "user", Content: s})
	}
	return toolMsgs
}

func toolArguments(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	if json.Valid(raw) && (raw[0] == '{' || raw[0] == '[') {
		return string(raw)
	}
	return "{}"
}

// rewriteBedrockModelJSON passes an Anthropic Messages body through and rewrites
// an OpenAI chat.completion into that shape. The raw body is never returned in
// an error: gateways can echo the bearer token there.
func rewriteBedrockModelJSON(raw []byte) ([]byte, error) {
	if isAnthropicModelJSON(raw) {
		return raw, nil
	}
	rewritten, ok := anthropicFromChatCompletion(raw)
	if !ok {
		return nil, bedrockShapeError(raw)
	}
	return rewritten, nil
}

func isAnthropicModelJSON(raw []byte) bool {
	var probe struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	if probe.Type == "message" || probe.Type == "error" {
		return true
	}
	return len(probe.Content) > 0 && probe.Content[0] == '['
}

// bedrockGatewayError is raised by the adapter itself. Its text never includes
// gateway-supplied values, so it is safe to show to the operator.
type bedrockGatewayError struct{ msg string }

func (e *bedrockGatewayError) Error() string { return e.msg }

func bedrockShapeError(raw []byte) error {
	if !json.Valid(raw) {
		return &bedrockGatewayError{"Bedrock gateway response was not JSON"}
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return &bedrockGatewayError{"Bedrock gateway response was not a JSON object"}
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = append(keys[:8], "…")
	}
	finish := ""
	if c, ok := top["choices"]; ok {
		var choices []struct {
			FinishReason string `json:"finish_reason"`
		}
		_ = json.Unmarshal(c, &choices)
		if len(choices) > 0 {
			finish = choices[0].FinishReason
		}
	}
	return &bedrockGatewayError{fmt.Sprintf("Bedrock gateway response was not an Anthropic message or a chat completion (object=%s, finish_reason=%s, keys=%s)", jsonStringField(top["object"]), finish, strings.Join(keys, ","))}
}

func jsonStringField(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

type chatDoc struct {
	Object  string         `json:"object"`
	Choices []chatChoice   `json:"choices"`
	Usage   chatUsageBlock `json:"usage"`
}

type chatChoice struct {
	FinishReason string      `json:"finish_reason"`
	Message      chatMessage `json:"message"`
	Delta        chatMessage `json:"delta"`
}

type chatMessage struct {
	Content          json.RawMessage `json:"content"`
	Reasoning        string          `json:"reasoning"`
	ReasoningContent string          `json:"reasoning_content"`
	ReasoningText    string          `json:"reasoning_text"`
	ToolCalls        []chatToolCall  `json:"tool_calls"`
}

type chatToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatUsageBlock struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type anthBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type anthMessage struct {
	Type       string      `json:"type"`
	Role       string      `json:"role"`
	Content    []anthBlock `json:"content"`
	StopReason string      `json:"stop_reason,omitempty"`
	Usage      *anthUsage  `json:"usage,omitempty"`
}

type anthUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func anthropicFromChatCompletion(raw []byte) ([]byte, bool) {
	var doc chatDoc
	if json.Unmarshal(raw, &doc) != nil || doc.Object != "chat.completion" {
		return nil, false
	}
	msg := anthMessage{Type: "message", Role: "assistant", Content: []anthBlock{}}
	if len(doc.Choices) > 0 {
		ch := doc.Choices[0]
		msg.Content = blocksFromMessage(ch.Message)
		msg.StopReason = mapFinishReason(ch.FinishReason)
	}
	if doc.Usage.PromptTokens != 0 || doc.Usage.CompletionTokens != 0 {
		msg.Usage = &anthUsage{InputTokens: doc.Usage.PromptTokens, OutputTokens: doc.Usage.CompletionTokens}
	}
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, false
	}
	return out, true
}

func blocksFromMessage(m chatMessage) []anthBlock {
	var blocks []anthBlock
	if think := pickReasoningText(m.Reasoning, m.ReasoningContent, m.ReasoningText); think != "" {
		blocks = append(blocks, anthBlock{Type: "thinking", Thinking: think})
	}
	if text := jsonText(m.Content); text != "" {
		blocks = append(blocks, anthBlock{Type: "text", Text: text})
	}
	for i, tc := range m.ToolCalls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("tool_%d", i)
		}
		name := tc.Function.Name
		if name == "" {
			name = "tool"
		}
		blocks = append(blocks, anthBlock{Type: "tool_use", ID: id, Name: name, Input: toolInput(tc.Function.Arguments)})
	}
	if blocks == nil {
		blocks = []anthBlock{}
	}
	return blocks
}

func pickReasoningText(fields ...string) string {
	for _, f := range fields {
		if f != "" {
			return f
		}
	}
	return ""
}

func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "" || p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func toolInput(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments != "" && json.Valid([]byte(arguments)) && (arguments[0] == '{' || arguments[0] == '[') {
		return json.RawMessage(arguments)
	}
	return json.RawMessage(`{}`)
}

func mapFinishReason(s string) string {
	switch s {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return s
	}
}

// chatCompletionToSSE replays a non-streamed chat.completion as Anthropic SSE
// for a gateway that ignored the streaming request.
func chatCompletionToSSE(raw []byte) (string, bool, error) {
	var doc chatDoc
	if json.Unmarshal(raw, &doc) != nil || doc.Object != "chat.completion" {
		return "", false, nil
	}
	var b strings.Builder
	c := &chatChunkWriter{w: &b}
	if err := c.write(raw); err != nil {
		return "", true, err
	}
	if err := c.finish(""); err != nil {
		return "", true, err
	}
	return b.String(), true, nil
}

func writeSSE(w io.Writer, event string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

func bedrockPayloadKind(payload []byte) string {
	var probe struct {
		Object string `json:"object"`
		Type   string `json:"type"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		return "invalid"
	}
	switch probe.Object {
	case "chat.completion", "chat.completion.chunk":
		return "chat"
	}
	if validEventType(probe.Type) {
		return "anthropic"
	}
	return "invalid"
}

// chatChunkWriter turns a sequence of chat.completion.chunk frames into
// Anthropic SSE. Tool-call argument fragments are forwarded as they arrive.
type chatChunkWriter struct {
	w        io.Writer
	started  bool
	finished bool
	input    int
	output   int
	tools    map[int]*chatToolState
}

type chatToolState struct {
	started bool
	id      string
	name    string
	pending strings.Builder
}

func (c *chatChunkWriter) write(payload []byte) error {
	var doc chatDoc
	if json.Unmarshal(payload, &doc) != nil {
		return errors.New("invalid Bedrock gateway event")
	}
	if doc.Usage.PromptTokens != 0 {
		c.input = doc.Usage.PromptTokens
	}
	if doc.Usage.CompletionTokens != 0 {
		c.output = doc.Usage.CompletionTokens
	}
	if len(doc.Choices) == 0 {
		return nil
	}
	ch := doc.Choices[0]
	if err := c.emitMessage(ch.Delta); err != nil {
		return err
	}
	// A full message has no per-call index; number its calls by position.
	for i := range ch.Message.ToolCalls {
		ch.Message.ToolCalls[i].Index = i
	}
	if err := c.emitMessage(ch.Message); err != nil {
		return err
	}
	if ch.FinishReason != "" {
		return c.finish(mapFinishReason(ch.FinishReason))
	}
	return nil
}

func (c *chatChunkWriter) emitMessage(m chatMessage) error {
	think := pickReasoningText(m.Reasoning, m.ReasoningContent, m.ReasoningText)
	text := jsonText(m.Content)
	if think == "" && text == "" && len(m.ToolCalls) == 0 {
		return nil
	}
	if err := c.ensureStart(); err != nil {
		return err
	}
	if think != "" {
		if err := writeSSE(c.w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"delta": map[string]string{"type": "thinking_delta", "thinking": think},
		}); err != nil {
			return err
		}
	}
	if text != "" {
		if err := writeSSE(c.w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"delta": map[string]string{"type": "text_delta", "text": text},
		}); err != nil {
			return err
		}
	}
	for _, tc := range m.ToolCalls {
		if err := c.emitTool(tc); err != nil {
			return err
		}
	}
	return nil
}

func (c *chatChunkWriter) emitTool(tc chatToolCall) error {
	if c.tools == nil {
		c.tools = map[int]*chatToolState{}
	}
	st := c.tools[tc.Index]
	if st == nil {
		st = &chatToolState{}
		c.tools[tc.Index] = st
	}
	if tc.ID != "" {
		st.id = tc.ID
	}
	if tc.Function.Name != "" {
		st.name = tc.Function.Name
	}
	if !st.started {
		st.pending.WriteString(tc.Function.Arguments)
		if st.name == "" {
			return nil
		}
		if st.id == "" {
			st.id = fmt.Sprintf("tool_%d", tc.Index)
		}
		st.started = true
		if err := writeSSE(c.w, "content_block_start", map[string]any{
			"type": "content_block_start",
			"content_block": map[string]string{
				"type": "tool_use", "id": st.id, "name": st.name,
			},
		}); err != nil {
			return err
		}
		if st.pending.Len() == 0 {
			return nil
		}
		frag := st.pending.String()
		st.pending.Reset()
		return writeSSE(c.w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"delta": map[string]string{"type": "input_json_delta", "partial_json": frag},
		})
	}
	if tc.Function.Arguments == "" {
		return nil
	}
	return writeSSE(c.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"delta": map[string]string{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
	})
}

func (c *chatChunkWriter) ensureStart() error {
	if c.started {
		return nil
	}
	c.started = true
	return writeSSE(c.w, "message_start", map[string]any{
		"type":    "message_start",
		"message": map[string]any{"usage": map[string]int{"input_tokens": c.input, "output_tokens": 0}},
	})
}

func (c *chatChunkWriter) finish(stop string) error {
	if c.finished {
		return nil
	}
	if err := c.ensureStart(); err != nil {
		return err
	}
	c.finished = true
	if stop == "" {
		stop = "end_turn"
	}
	if err := writeSSE(c.w, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]string{"stop_reason": stop},
		"usage": map[string]int{"input_tokens": c.input, "output_tokens": c.output},
	}); err != nil {
		return err
	}
	return writeSSE(c.w, "message_stop", map[string]string{"type": "message_stop"})
}

func (c *chatChunkWriter) close() error {
	if c.finished || !c.started {
		return nil
	}
	return c.finish("end_turn")
}

func mustURLPath(raw string) string { u, _ := url.Parse(raw); return u.Path }

func validEventType(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

type bedrockStreamBody struct {
	*io.PipeReader
	upstream io.ReadCloser
}

func (b *bedrockStreamBody) Close() error {
	_ = b.upstream.Close()
	return b.PipeReader.Close()
}
