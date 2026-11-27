package agent

// Claude Code CLI provider ("claude-code" format).
//
// The profile drives agents with the local `claude` binary in headless print
// mode instead of an HTTP endpoint, so a Claude subscription logged in through
// Claude Code (or a `claude setup-token` OAuth token) can power ARTEX.
//
// Norma still speaks Anthropic Messages: NewProvider points it at a placeholder
// base URL and swaps the HTTP transport for claudeCodeTransport, which answers
// each /v1/messages request by running one `claude -p` process. ARTEX keeps its
// own agent loop, tools, permissions and transcripts; the CLI is only the model:
//
//   - every built-in Claude Code tool, MCP server, setting source and slash
//     command is disabled, so the process can do nothing but produce text;
//   - ARTEX's tool definitions go into the system prompt with a small JSON reply
//     protocol, and the reply's tool calls are turned back into tool_use blocks;
//   - the conversation (text, tool calls, tool results) is rendered into the
//     prompt on stdin, because each process is a fresh, stateless session.
//
// The process gets a minimal environment, never the server's: no database
// password, secret key or LLM key of another profile reaches it.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// FormatClaudeCode marks a profile served by the local Claude Code CLI.
const FormatClaudeCode llm.Format = "claude-code"

// claudeCodeBaseURL is the placeholder endpoint Norma is pointed at. It is never
// dialed: claudeCodeTransport answers every request itself.
const claudeCodeBaseURL = "http://claude-code.invalid"

// DefaultClaudeCodeModel is the model alias used when a profile names none.
const DefaultClaudeCodeModel = "sonnet"

// ClaudeCodeModels is the model list offered by the UI's model picker: the CLI's
// aliases, which always resolve to the current model of each family.
var ClaudeCodeModels = []string{"opus", "sonnet", "haiku"}

// Environment knobs, read when a provider is built.
const (
	envClaudeCodeBin       = "ARTEX_CLAUDE_CODE_BIN"        // binary path; default "claude" on PATH
	envClaudeCodeConfigDir = "ARTEX_CLAUDE_CODE_CONFIG_DIR" // CLAUDE_CONFIG_DIR for the CLI; empty = the server user's own
	envClaudeCodeMaxProcs  = "ARTEX_CLAUDE_CODE_MAX_PROCS"  // concurrent CLI processes; default 4
	envClaudeCodeTimeout   = "ARTEX_CLAUDE_CODE_TIMEOUT"    // per-call cap (Go duration); default 10m
)

const (
	defaultClaudeCodeMaxProcs = 4
	defaultClaudeCodeTimeout  = 10 * time.Minute
	// claudeCodeMaxOutput bounds what is read from one CLI process.
	claudeCodeMaxOutput = 16 << 20
)

// claudeCodeSlots caps concurrent CLI processes across every claude-code
// provider in the process: each one is a full Node runtime.
var (
	claudeCodeSlotsOnce sync.Once
	claudeCodeSlots     chan struct{}
)

func claudeCodeSemaphore() chan struct{} {
	claudeCodeSlotsOnce.Do(func() {
		n := defaultClaudeCodeMaxProcs
		if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envClaudeCodeMaxProcs))); err == nil && v > 0 {
			n = v
		}
		claudeCodeSlots = make(chan struct{}, n)
	})
	return claudeCodeSlots
}

// IsKeylessFormat reports whether a stored profile format runs without an API
// key: Ollama (a local server) and Claude Code (the CLI can use its own login).
func IsKeylessFormat(format string) bool {
	switch strings.TrimSpace(format) {
	case string(FormatOllama), string(FormatClaudeCode):
		return true
	}
	return false
}

// claudeCodeTransport answers Norma's Anthropic Messages requests by running the
// Claude Code CLI. It never opens a network connection itself.
type claudeCodeTransport struct {
	bin       string
	model     string
	token     string // optional: OAuth token (sk-ant-oat…) or API key (sk-ant-api…)
	configDir string // optional CLAUDE_CONFIG_DIR
	proxy     string // optional outbound proxy for the CLI
	timeout   time.Duration
	slots     chan struct{}
	// run executes the CLI; replaced in tests.
	run func(ctx context.Context, t *claudeCodeTransport, args []string, env []string, dir string, stdin []byte) ([]byte, []byte, error)
}

func newClaudeCodeTransport(c Config) (*claudeCodeTransport, error) {
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = DefaultClaudeCodeModel
	}
	if strings.HasPrefix(model, "-") || strings.ContainsAny(model, " \t\r\n") {
		return nil, errors.New("claude-code: invalid model name")
	}
	bin := strings.TrimSpace(os.Getenv(envClaudeCodeBin))
	if bin == "" {
		bin = "claude"
	}
	timeout := defaultClaudeCodeTimeout
	if v, err := time.ParseDuration(strings.TrimSpace(os.Getenv(envClaudeCodeTimeout))); err == nil && v > 0 {
		timeout = v
	}
	token := strings.TrimSpace(c.APIKey)
	if !validHeaderValue(token) && token != "" {
		return nil, errors.New("claude-code: the token contains invalid characters")
	}
	return &claudeCodeTransport{
		bin:       bin,
		model:     model,
		token:     token,
		configDir: strings.TrimSpace(os.Getenv(envClaudeCodeConfigDir)),
		proxy:     strings.TrimSpace(c.Proxy),
		timeout:   timeout,
		slots:     claudeCodeSemaphore(),
		run:       runClaudeCodeProcess,
	}, nil
}

// claudeCodeRequest is the part of an Anthropic Messages request the adapter uses.
type claudeCodeRequest struct {
	System       json.RawMessage     `json:"system"`
	Messages     []claudeCodeMessage `json:"messages"`
	Tools        []claudeCodeTool    `json:"tools"`
	Stream       bool                `json:"stream"`
	OutputConfig *struct {
		Effort string `json:"effort"`
	} `json:"output_config"`
}

type claudeCodeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeCodeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type claudeCodeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// claudeCodeResult is the CLI's --output-format json document.
type claudeCodeResult struct {
	Type           string `json:"type"`
	Subtype        string `json:"subtype"`
	IsError        bool   `json:"is_error"`
	Result         string `json:"result"`
	StopReason     string `json:"stop_reason"`
	APIErrorStatus *int   `json:"api_error_status"`
	Usage          struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// claudeCodeToolCall is one entry of the reply protocol's tool_calls array.
type claudeCodeToolCall struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (t *claudeCodeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Host != "claude-code.invalid" || !strings.HasSuffix(req.URL.Path, "/v1/messages") {
		return nil, errors.New("claude-code: unexpected request destination")
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	var body claudeCodeRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		return claudeCodeError(req, http.StatusBadRequest, "invalid_request_error", "claude-code: decode request: "+err.Error()), nil
	}
	systemPrompt, err := renderClaudeCodeSystem(body.System, body.Tools)
	if err != nil {
		return claudeCodeError(req, http.StatusBadRequest, "invalid_request_error", "claude-code: "+err.Error()), nil
	}
	prompt, err := renderClaudeCodePrompt(body.Messages, len(body.Tools) > 0)
	if err != nil {
		return claudeCodeError(req, http.StatusBadRequest, "invalid_request_error", "claude-code: "+err.Error()), nil
	}
	effort := ""
	if body.OutputConfig != nil {
		switch e := strings.TrimSpace(body.OutputConfig.Effort); e {
		case "low", "medium", "high", "xhigh", "max":
			effort = e
		}
	}

	ctx := req.Context()
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.slots }()

	res, status, msg := t.call(ctx, systemPrompt, prompt, effort)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if status != 0 {
		errType := "api_error"
		switch status {
		case http.StatusUnauthorized, http.StatusForbidden:
			errType = "authentication_error"
		case http.StatusTooManyRequests:
			errType = "rate_limit_error"
		}
		return claudeCodeError(req, status, errType, msg), nil
	}

	content, stopReason := claudeCodeContent(res.Result, body.Tools)
	if res.StopReason == "max_tokens" && stopReason != "tool_use" {
		stopReason = "max_tokens"
	}
	usage := map[string]int{
		"input_tokens":                res.Usage.InputTokens,
		"output_tokens":               res.Usage.OutputTokens,
		"cache_read_input_tokens":     res.Usage.CacheReadInputTokens,
		"cache_creation_input_tokens": res.Usage.CacheCreationInputTokens,
	}
	msgID := "msg_cc_" + randomHex(12)
	if body.Stream {
		return claudeCodeSSE(req, msgID, t.model, content, stopReason, usage), nil
	}
	out, err := json.Marshal(map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": t.model,
		"content": content, "stop_reason": stopReason, "stop_sequence": nil, "usage": usage,
	})
	if err != nil {
		return nil, err
	}
	return claudeCodeResponse(req, http.StatusOK, "application/json", out), nil
}

// call runs one CLI process. A non-zero status means failure, with msg as the
// error text; otherwise res holds the CLI's result document.
func (t *claudeCodeTransport) call(ctx context.Context, systemPrompt, prompt, effort string) (res claudeCodeResult, status int, msg string) {
	if t.configDir != "" {
		// The CLI keeps its login and state here; private to the server user.
		if err := os.MkdirAll(t.configDir, 0o700); err != nil {
			return res, http.StatusInternalServerError, "claude-code: create config dir: " + err.Error()
		}
	}
	dir, err := os.MkdirTemp("", "artex-claude-code-")
	if err != nil {
		return res, http.StatusInternalServerError, "claude-code: create work dir: " + err.Error()
	}
	defer os.RemoveAll(dir)
	spFile := filepath.Join(dir, "system-prompt.txt")
	if err := os.WriteFile(spFile, []byte(systemPrompt), 0o600); err != nil {
		return res, http.StatusInternalServerError, "claude-code: write system prompt: " + err.Error()
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--model", t.model,
		"--system-prompt-file", spFile,
		// Nothing but text generation: no built-in tools, no MCP servers, no
		// user/project/local settings (hooks, permissions), no slash commands
		// or skills, and nothing written to the session store.
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--disable-slash-commands",
		"--no-session-persistence",
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}

	callCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	stdout, stderr, runErr := t.run(callCtx, t, args, t.env(dir), dir, []byte(prompt))
	if ctx.Err() != nil {
		return res, http.StatusRequestTimeout, ctx.Err().Error()
	}
	if callCtx.Err() != nil {
		return res, http.StatusGatewayTimeout, fmt.Sprintf("claude-code: the CLI did not answer within %s", t.timeout)
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &res); err != nil || res.Type != "result" {
		detail := t.scrub(clipBody(string(stderr) + " " + string(stdout)))
		if runErr != nil {
			var execErr *exec.Error
			if errors.As(runErr, &execErr) || errors.Is(runErr, fs.ErrNotExist) || errors.Is(runErr, fs.ErrPermission) {
				return res, http.StatusBadGateway, fmt.Sprintf("claude-code: cannot run %q: %v (install Claude Code or set %s)", t.bin, runErr, envClaudeCodeBin)
			}
			return res, http.StatusBadGateway, fmt.Sprintf("claude-code: the CLI failed (%v): %s", runErr, detail)
		}
		return res, http.StatusBadGateway, "claude-code: unexpected CLI output: " + detail
	}
	if res.IsError || res.Subtype != "success" {
		text := t.scrub(strings.TrimSpace(res.Result))
		if text == "" {
			text = res.Subtype
		}
		return res, claudeCodeErrorStatus(res.APIErrorStatus, text), "claude-code: " + text
	}
	return res, 0, ""
}

// claudeCodeErrorStatus picks the HTTP status for a failed CLI run, so Norma's
// retry loop and ARTEX's failover treat it like the matching API error.
func claudeCodeErrorStatus(apiStatus *int, text string) int {
	if apiStatus != nil && *apiStatus >= 400 && *apiStatus <= 599 {
		return *apiStatus
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "not logged in"), strings.Contains(lower, "/login"),
		strings.Contains(lower, "invalid api key"), strings.Contains(lower, "oauth token"),
		strings.Contains(lower, "authentication"):
		return http.StatusUnauthorized
	case strings.Contains(lower, "usage limit"), strings.Contains(lower, "limit reached"),
		strings.Contains(lower, "hit your limit"):
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

// env is the CLI's whole environment: never os.Environ(), which holds the
// server's own secrets until the startup scrub and the operator's shell values.
func (t *claudeCodeTransport) env(workDir string) []string {
	env := []string{
		"PATH=" + claudeCodePath(),
		"LANG=C.UTF-8",
		"TMPDIR=" + workDir,
		"DISABLE_AUTOUPDATER=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	if user := os.Getenv("USER"); user != "" {
		env = append(env, "USER="+user)
	}
	if t.configDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+t.configDir)
	} else if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+dir)
	}
	switch {
	case strings.HasPrefix(t.token, "sk-ant-api"):
		env = append(env, "ANTHROPIC_API_KEY="+t.token)
	case t.token != "":
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+t.token)
	}
	if t.proxy != "" {
		env = append(env, "HTTPS_PROXY="+t.proxy, "HTTP_PROXY="+t.proxy)
	}
	return env
}

func claudeCodePath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
}

// scrub removes the profile token from text that may reach logs or the UI.
func (t *claudeCodeTransport) scrub(s string) string {
	if t.token != "" {
		s = strings.ReplaceAll(s, t.token, "[redacted]")
	}
	return s
}

func runClaudeCodeProcess(ctx context.Context, t *claudeCodeTransport, args []string, env []string, dir string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, t.bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	stdout := &limitedBuffer{max: claudeCodeMaxOutput}
	stderr := &limitedBuffer{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// limitedBuffer keeps the first max bytes written and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

const claudeCodeProtocol = `# Reply protocol

You are the model behind an API adapter. You cannot run tools yourself: the
caller runs them and sends the results back in the next turn.

The tools you may call are listed below as JSON (name, description, input JSON Schema):

<tools>
%s
</tools>

Answer with exactly ONE JSON object and nothing else (no code fence, no prose around it):

{"text": "<what you say to the user; may be empty>", "tool_calls": [{"name": "<tool name>", "input": {<arguments>}}]}

Rules:
- Only use tool names from the list above; each input must satisfy that tool's schema.
- Several independent calls may share one reply; they run in the order given.
- When you need no tool, or the work is finished, send "tool_calls": [].
- Tool results arrive as <tool_result> elements in the next user turn.`

// renderClaudeCodeSystem joins the request's system prompt and, when tools are
// offered, appends the tool list and reply protocol.
func renderClaudeCodeSystem(system json.RawMessage, tools []claudeCodeTool) (string, error) {
	var sb strings.Builder
	text, err := claudeCodeText(system)
	if err != nil {
		return "", fmt.Errorf("decode system prompt: %w", err)
	}
	sb.WriteString(strings.TrimSpace(text))
	if len(tools) > 0 {
		list, err := json.MarshalIndent(tools, "", "  ")
		if err != nil {
			return "", err
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, claudeCodeProtocol, list)
	}
	if sb.Len() == 0 {
		sb.WriteString("You are a helpful assistant.")
	}
	return sb.String(), nil
}

// renderClaudeCodePrompt renders the whole conversation as one prompt, since
// each CLI process starts a fresh session.
func renderClaudeCodePrompt(msgs []claudeCodeMessage, withTools bool) (string, error) {
	if len(msgs) == 0 {
		return "", errors.New("request has no messages")
	}
	var sb strings.Builder
	sb.WriteString("<conversation>\n")
	for _, m := range msgs {
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		blocks, err := decodeBlocks(m.Content)
		if err != nil {
			return "", fmt.Errorf("decode %s message: %w", role, err)
		}
		fmt.Fprintf(&sb, "<turn role=%q>\n", role)
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if s := strings.TrimSpace(b.Text); s != "" {
					sb.WriteString(s)
					sb.WriteString("\n")
				}
			case "tool_use":
				in := b.Input
				if len(bytes.TrimSpace(in)) == 0 {
					in = json.RawMessage("{}")
				}
				fmt.Fprintf(&sb, "<tool_call id=%q name=%q>%s</tool_call>\n", b.ID, b.Name, compactJSON(in))
			case "tool_result":
				text, err := claudeCodeText(b.Content)
				if err != nil {
					return "", fmt.Errorf("decode tool result: %w", err)
				}
				fmt.Fprintf(&sb, "<tool_result tool_use_id=%q is_error=\"%t\">\n%s\n</tool_result>\n", b.ToolUseID, b.IsError, strings.TrimSpace(text))
			case "image", "document":
				fmt.Fprintf(&sb, "[%s omitted: not supported by the claude-code adapter]\n", b.Type)
			}
			// thinking / redacted_thinking blocks belong to another session's
			// signatures and are dropped.
		}
		sb.WriteString("</turn>\n")
	}
	sb.WriteString("</conversation>\n\n")
	if withTools {
		sb.WriteString("Write the assistant's next turn now, as one JSON object following the reply protocol.")
	} else {
		sb.WriteString("Write the assistant's next turn now.")
	}
	return sb.String(), nil
}

// decodeBlocks accepts content as a plain string or an array of blocks.
func decodeBlocks(raw json.RawMessage) ([]claudeCodeBlock, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []claudeCodeBlock{{Type: "text", Text: s}}, nil
	}
	var blocks []claudeCodeBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// claudeCodeText flattens string-or-blocks content to text.
func claudeCodeText(raw json.RawMessage) (string, error) {
	blocks, err := decodeBlocks(raw)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image", "document":
			parts = append(parts, "["+b.Type+" omitted]")
		}
	}
	return strings.Join(parts, "\n"), nil
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// claudeCodeContent turns the CLI's reply text into Anthropic content blocks.
// Without tools the reply is plain text. With tools it should be the protocol's
// JSON object; a reply that is not falls back to plain text, so a model that
// answers in prose still ends its turn instead of failing the request.
func claudeCodeContent(result string, tools []claudeCodeTool) ([]map[string]any, string) {
	result = strings.TrimSpace(result)
	if len(tools) == 0 {
		return textContent(result), "end_turn"
	}
	reply, ok := parseClaudeCodeReply(result)
	if !ok {
		return textContent(result), "end_turn"
	}
	content := textContent(strings.TrimSpace(reply.Text))
	stopReason := "end_turn"
	for _, call := range reply.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		content = append(content, map[string]any{
			"type": "tool_use", "id": "toolu_cc_" + randomHex(12), "name": name, "input": toolInput(call.Input),
		})
		stopReason = "tool_use"
	}
	return content, stopReason
}

func textContent(s string) []map[string]any {
	if s == "" {
		return []map[string]any{}
	}
	return []map[string]any{{"type": "text", "text": s}}
}

type claudeCodeReply struct {
	Text      string               `json:"text"`
	ToolCalls []claudeCodeToolCall `json:"tool_calls"`
}

// parseClaudeCodeReply decodes the protocol object, tolerating a code fence or
// prose around it.
func parseClaudeCodeReply(s string) (claudeCodeReply, bool) {
	candidates := []string{s}
	if inner, ok := stripCodeFence(s); ok {
		candidates = append(candidates, inner)
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		candidates = append(candidates, s[i:j+1])
	}
	for _, c := range candidates {
		var probe map[string]json.RawMessage
		if json.Unmarshal([]byte(c), &probe) != nil {
			continue
		}
		_, hasText := probe["text"]
		_, hasCalls := probe["tool_calls"]
		if !hasText && !hasCalls {
			continue
		}
		var r claudeCodeReply
		if json.Unmarshal([]byte(c), &r) == nil {
			return r, true
		}
	}
	return claudeCodeReply{}, false
}

func stripCodeFence(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return "", false
	}
	s = strings.TrimPrefix(s, "```")
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s), true
}

// toolInput normalizes a tool call's input to a JSON object: a string holding
// JSON is decoded, anything that is not an object becomes {}.
func toolInput(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			raw = bytes.TrimSpace([]byte(s))
		}
	}
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(compactJSON(raw))
}

// claudeCodeSSE renders a finished message as an Anthropic SSE stream.
func claudeCodeSSE(req *http.Request, id, model string, content []map[string]any, stopReason string, usage map[string]int) *http.Response {
	var buf bytes.Buffer
	event := func(name string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(&buf, "event: %s\ndata: %s\n\n", name, b)
	}
	event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usage,
	}})
	for i, block := range content {
		switch block["type"] {
		case "text":
			event("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": block["text"]}})
		case "tool_use":
			event("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}}})
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(block["input"].(json.RawMessage))}})
		}
		event("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": usage})
	event("message_stop", map[string]any{"type": "message_stop"})
	return claudeCodeResponse(req, http.StatusOK, "text/event-stream", buf.Bytes())
}

func claudeCodeError(req *http.Request, status int, errType, message string) *http.Response {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": errType, "message": message}})
	return claudeCodeResponse(req, status, "application/json", body)
}

func claudeCodeResponse(req *http.Request, status int, contentType string, body []byte) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", contentType)
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
