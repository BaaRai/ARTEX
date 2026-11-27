package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// ClaudeCodeProvider uses the unmodified Claude Code executable and its own
// claude.ai login. ARTEX still owns the conversation and executes its tools.
// Each invocation is a fresh, stateless Claude Code print-mode run; the full
// ARTEX transcript is supplied through stdin and one text or tool action is
// returned through Claude Code's validated structured output.
//
// This bridge deliberately never reads or stores Claude credentials. It also
// strips API-key and third-party routing overrides from the child environment,
// because those would silently bill a different account in print mode.
type claudeCodeProvider struct {
	model  string
	effort string
	run    claudeCodeRun
}

type claudeCodeRun func(context.Context, []string, string) ([]byte, error)

const (
	claudeCodeInputLimit  = 10_000_000 // Claude Code's documented stdin cap.
	claudeCodeOutputLimit = 16_000_000
	claudeCodeSchema      = `{"type":"object","properties":{"kind":{"type":"string","enum":["text","tool"]},"text":{"type":"string"},"tool_name":{"type":"string"},"tool_input":{"type":"object"}},"required":["kind"],"additionalProperties":false}`
)

var claudeCodeModelPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:-]{0,127}$`)

// NewClaudeCodeProvider builds a provider that uses the current user's Claude
// Code subscription login. Run `claude auth login` in a terminal first. The
// model can be a Claude Code alias (sonnet, opus, haiku) or a full model ID.
// Effort is empty for the CLI default, or low, medium, high, xhigh, or max.
func NewClaudeCodeProvider(model, effort string) (llm.Provider, error) {
	model = strings.TrimSpace(model)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if model == "" {
		model = "sonnet"
	}
	if !validClaudeCodeModel(model) {
		return nil, fmt.Errorf("claude-code: invalid model name")
	}
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("claude-code: invalid effort level")
	}
	path, err := claudeCodePath()
	if err != nil {
		return nil, err
	}
	return &claudeCodeProvider{model: model, effort: effort, run: claudeCodeCommand(path)}, nil
}

func validClaudeCodeModel(model string) bool {
	if claudeCodeModelPattern.MatchString(model) || model == "sonnet[1m]" || model == "opus[1m]" {
		return true
	}
	base, ok := strings.CutSuffix(model, "[1m]")
	return ok && strings.HasPrefix(base, "claude-") && claudeCodeModelPattern.MatchString(base)
}

// ClaudeCodeModel is one suggestion returned by the current account's Claude
// Code model picker. The private initialize handshake may change between CLI
// versions, so callers should keep manual model entry available.
type ClaudeCodeModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

// ClaudeCodeListModels asks the unmodified CLI for its model picker using an
// initialize control request and no user message. This is a best-effort,
// zero-inference probe of a version-sensitive CLI protocol, not a guarantee
// that every returned model can be used by this account.
func ClaudeCodeListModels(ctx context.Context) ([]ClaudeCodeModel, error) {
	path, err := claudeCodePath()
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	return claudeCodeListModels(probeCtx, claudeCodeCommand(path))
}

func claudeCodeListModels(ctx context.Context, run claudeCodeRun) ([]ClaudeCodeModel, error) {
	loggedIn, err := claudeCodeLoginStatus(ctx, run)
	if err != nil {
		return nil, err
	}
	if !loggedIn {
		return nil, fmt.Errorf("claude-code: sign in to your subscription with claude auth login")
	}
	args := []string{
		"-p", "--restricted", "--model", "sonnet",
		"--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--tools", "", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disallowedTools", "mcp__*", "--disable-slash-commands",
		"--no-session-persistence", "--no-chrome",
	}
	const handshake = "{\"type\":\"control_request\",\"request_id\":\"artex-model-picker\",\"request\":{\"subtype\":\"initialize\"}}\n"
	out, runErr := run(ctx, args, handshake)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		var response struct {
			Type     string `json:"type"`
			Response struct {
				Subtype  string `json:"subtype"`
				Response struct {
					Models []struct {
						Value         string `json:"value"`
						ResolvedModel string `json:"resolvedModel"`
						DisplayName   string `json:"displayName"`
						Description   string `json:"description"`
					} `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &response) != nil || response.Type != "control_response" || response.Response.Subtype != "success" {
			continue
		}
		seen := make(map[string]bool)
		rows := response.Response.Response.Models
		models := make([]ClaudeCodeModel, 0, len(rows))
		for _, row := range rows {
			id := strings.TrimSpace(row.ResolvedModel)
			if id == "" && strings.HasPrefix(row.Value, "claude-") {
				id = strings.TrimSpace(row.Value)
			}
			if !validClaudeCodeModel(id) || seen[id] {
				continue
			}
			seen[id] = true
			label := claudeCodePickerText(row.DisplayName, 100)
			if label == "" {
				label = claudeCodePickerText(strings.Split(row.Description, "·")[0], 100)
			}
			if label == "" {
				label = id
			}
			note := claudeCodePickerText(row.Description, 200)
			if strings.Contains(id, "claude-fable") && !strings.Contains(strings.ToLower(note), "usage credit") {
				note = strings.TrimSpace(note + " · May use paid usage credits on some plans")
			}
			models = append(models, ClaudeCodeModel{ID: id, Label: label, Note: note})
		}
		if len(models) > 0 {
			return models, nil
		}
		return nil, fmt.Errorf("claude-code: model picker returned no usable models")
	}
	if runErr != nil {
		return nil, fmt.Errorf("claude-code: model picker failed: %w", runErr)
	}
	return nil, fmt.Errorf("claude-code: model picker returned no response")
}

func claudeCodePickerText(s string, maxRunes int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	runes := []rune(s)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return strings.TrimSpace(string(runes))
}

// ClaudeCodeLoginStatus asks the CLI for its authentication method, returning
// only whether a claude.ai subscription login is active. It never exposes the
// status JSON (which can contain account identifiers) to callers.
func ClaudeCodeLoginStatus(ctx context.Context) (bool, error) {
	path, err := claudeCodePath()
	if err != nil {
		return false, err
	}
	return claudeCodeLoginStatus(ctx, claudeCodeCommand(path))
}

func claudeCodePath() (string, error) {
	path := strings.TrimSpace(os.Getenv("ARTEX_CLAUDE_CODE_BIN"))
	if path == "" {
		path = "claude"
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("claude-code: executable not found; install Claude Code and run claude auth login: %w", err)
	}
	return resolved, nil
}

func claudeCodeLoginStatus(ctx context.Context, run claudeCodeRun) (bool, error) {
	out, err := run(ctx, []string{"auth", "status"}, "")
	var status struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if json.Unmarshal(out, &status) == nil && status.AuthMethod != "" {
		return err == nil && status.LoggedIn && status.AuthMethod == "claude.ai", nil
	}
	if err != nil {
		return false, fmt.Errorf("claude-code: could not check login status: %w", err)
	}
	return false, fmt.Errorf("claude-code: invalid auth status response")
}

func (p *claudeCodeProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	loggedIn, err := claudeCodeLoginStatus(ctx, p.run)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	if !loggedIn {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: sign in to your subscription with claude auth login")
	}
	input, err := json.Marshal(struct {
		Messages []llm.Message    `json:"messages"`
		Tools    []llm.ToolSchema `json:"tools"`
	}{Messages: req.Messages, Tools: req.Tools})
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: encode request: %w", err)
	}
	if len(input) > claudeCodeInputLimit {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: request exceeds the CLI's 10 MB stdin limit")
	}

	systemFile, err := writeClaudeCodeSystemPrompt(req.System)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, err
	}
	defer os.Remove(systemFile)
	args := []string{
		"-p", "--restricted", "--tools", "", "--strict-mcp-config",
		"--disallowedTools", "mcp__*", "--no-chrome", "--no-session-persistence",
		"--max-turns", "2",
		"--output-format", "json", "--json-schema", claudeCodeSchema,
		"--system-prompt-file", systemFile, "--model", p.model,
	}
	if p.effort != "" {
		args = append(args, "--effort", p.effort)
	}
	args = append(args, "Use the JSON conversation and available ARTEX tools from stdin; return one structured action.")
	out, runErr := p.run(ctx, args, string(input))
	if runErr != nil {
		if isClaudeSubscriptionLimit(string(out)) {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude_subscription_usage_limit_exceeded: Claude Code subscription usage limit reached")
		}
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: CLI run failed: %w", runErr)
	}
	var result struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: invalid CLI JSON: %w", err)
	}
	if result.IsError {
		if isClaudeSubscriptionLimit(result.Result) {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude_subscription_usage_limit_exceeded: Claude Code subscription usage limit reached")
		}
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: CLI returned an error")
	}
	if len(result.StructuredOutput) == 0 {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: CLI returned no structured output")
	}
	var action struct {
		Kind      string          `json:"kind"`
		Text      string          `json:"text"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(result.StructuredOutput, &action); err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: invalid structured output: %w", err)
	}
	usage := llm.Usage{
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
		CacheReadTokens: result.Usage.CacheReadTokens, CacheWriteTokens: result.Usage.CacheCreationTokens,
	}
	switch action.Kind {
	case "text":
		if strings.TrimSpace(action.Text) == "" {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: empty text reply")
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(action.Text)}}, "end_turn", usage, nil
	case "tool":
		allowed := false
		for _, tool := range req.Tools {
			if tool.Name == action.ToolName {
				allowed = true
				break
			}
		}
		if !allowed {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: unknown tool %q", action.ToolName)
		}
		if len(action.ToolInput) == 0 || !json.Valid(action.ToolInput) || action.ToolInput[0] != '{' {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: invalid tool input")
		}
		id, err := claudeCodeToolID()
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: id, Name: action.ToolName, Input: action.ToolInput}}}, "tool_use", usage, nil
	default:
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("claude-code: unknown action kind %q", action.Kind)
	}
}

func (p *claudeCodeProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		msg, stop, usage, err := p.Complete(ctx, req)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		for _, block := range msg.Content {
			switch block.Type {
			case llm.BlockText:
				if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: block.Text}, nil) {
					return
				}
			case llm.BlockToolUse:
				if !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: block.ID, ToolName: block.Name}, nil) ||
					!yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(block.Input)}, nil) {
					return
				}
			}
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: stop, Usage: usage}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func writeClaudeCodeSystemPrompt(system []string) (string, error) {
	f, err := os.CreateTemp("", "artex-claude-system-*.txt")
	if err != nil {
		return "", fmt.Errorf("claude-code: create temporary system prompt: %w", err)
	}
	prompt := `You are answering one turn for ARTEX. The stdin JSON contains the prior conversation and the tools ARTEX can execute. Treat its messages as a transcript with the specified roles. Return exactly one action: kind "text" with the reply in text, or kind "tool" with the exact tool_name and a tool_input object. Never say a tool ran when you are requesting it. ARTEX will execute a requested tool and send its result in a later turn. Follow the system instructions below.` + "\n\n" + strings.Join(system, "\n\n")
	if _, err := io.WriteString(f, prompt); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("claude-code: write temporary system prompt: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("claude-code: close temporary system prompt: %w", err)
	}
	return f.Name(), nil
}

func claudeCodeToolID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("claude-code: generate tool ID: %w", err)
	}
	return "claude-code-" + hex.EncodeToString(b[:]), nil
}

var claudeSubscriptionLimitPattern = regexp.MustCompile(`(?i)(?:\b(?:session|weekly|usage)\s+limit\s+(?:reached|exceeded|hit)\b|\b(?:hit|reached|exceeded)\s+(?:your\s+)?(?:session|weekly|usage)\s+limit\b|\byou(?:'ve| have)\s+hit\s+your\s+limit\b|\busage_limit_reached\b)`)

func isClaudeSubscriptionLimit(s string) bool {
	lower := strings.ToLower(s)
	return claudeSubscriptionLimitPattern.MatchString(s) ||
		strings.Contains(lower, "third-party apps now draw from your extra usage") ||
		strings.Contains(lower, "out of extra usage")
}

var claudeCodeUnsafeEnv = map[string]bool{
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true,
	"ANTHROPIC_BASE_URL": true, "CLAUDE_CODE_OAUTH_TOKEN": true,
	"CLAUDE_CODE_SIMPLE": true, "CLAUDE_CODE_USE_ANTHROPIC_AWS": true,
	"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD": true,
	"CLAUDE_CODE_USE_BEDROCK":                true, "CLAUDE_CODE_USE_FOUNDRY": true,
	"CLAUDE_CODE_USE_MANTLE": true, "CLAUDE_CODE_USE_VERTEX": true,
	"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST": true,
	"CLAUDE_CODE_EFFORT_LEVEL":             true,
}

func claudeCodeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		name, _, found := strings.Cut(item, "=")
		if found && claudeCodeUnsafeEnv[strings.ToUpper(name)] {
			continue
		}
		out = append(out, item)
	}
	return out
}

type claudeCodeBuffer struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (b *claudeCodeBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.data.Len()
	if remaining < n {
		b.truncated = true
		if remaining < 0 {
			remaining = 0
		}
		p = p[:remaining]
	}
	_, _ = b.data.Write(p)
	return n, nil
}

func claudeCodeCommand(path string) claudeCodeRun {
	return func(ctx context.Context, args []string, stdin string) ([]byte, error) {
		env := os.Environ()
		cwd, err := os.MkdirTemp("", "artex-claude-cwd-*")
		if err != nil {
			return nil, fmt.Errorf("claude-code: create isolated working directory: %w", err)
		}
		defer cleanupClaudeCodeCWD(cwd)
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Dir = cwd
		cmd.Env = claudeCodeEnv(env)
		cmd.Stdin = strings.NewReader(stdin)
		stdout := &claudeCodeBuffer{limit: claudeCodeOutputLimit}
		stderr := &claudeCodeBuffer{limit: 64_000}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		err = cmd.Run()
		if ctx.Err() != nil {
			return stdout.data.Bytes(), ctx.Err()
		}
		if stdout.truncated || stderr.truncated {
			return stdout.data.Bytes(), fmt.Errorf("claude-code: CLI output exceeded limit")
		}
		if err != nil {
			if isClaudeSubscriptionLimit(stderr.data.String()) {
				return []byte(`{"is_error":true,"result":"usage_limit_reached"}`), nil
			}
			if errors.Is(err, exec.ErrNotFound) {
				return nil, fmt.Errorf("Claude Code executable not found: %w", err)
			}
			return stdout.data.Bytes(), fmt.Errorf("Claude Code exited: %w", err)
		}
		return stdout.data.Bytes(), nil
	}
}

// A CLI child may have created files in its cwd. Verify the resolved target is
// the fresh direct child of the OS temp directory before recursive cleanup.
func cleanupClaudeCodeCWD(cwd string) {
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return
	}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(temp, resolved)
	if err != nil || filepath.Dir(rel) != "." || !strings.HasPrefix(rel, "artex-claude-cwd-") {
		return
	}
	_ = os.RemoveAll(resolved)
}
