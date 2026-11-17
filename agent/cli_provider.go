package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// Subscription CLI formats. Each model call runs the vendor's own CLI as one
// subprocess, so a profile rides the subscription the CLI is logged in with on
// the host instead of an API key. See cliBackends for how each one is driven.
const (
	FormatClaudeCLI llm.Format = "claude-cli"
	FormatCodexCLI  llm.Format = "codex-cli"
	FormatGrokCLI   llm.Format = "grok-cli"
)

// cliCall is everything one CLI invocation may need. The runner fills every
// field; each backend's args func picks the ones its CLI takes. Schema and
// SchemaFile are empty when the request offers no tools.
type cliCall struct {
	Model           string
	Effort          string
	System          string
	Schema          string
	SchemaFile      string
	PromptFile      string
	LastMessageFile string
}

// cliBackend is one row of the CLI registry: how to invoke a vendor CLI
// headless and how to read its answer back. The prompt is always written to
// cliCall.PromptFile and piped to stdin; args decides which one the CLI reads.
type cliBackend struct {
	binary string
	// foldSystem prepends the system prompt to the transcript, for a CLI with
	// no flag that replaces its own system prompt.
	foldSystem bool
	args       func(cliCall) []string
	// parse returns the model's final reply (the schema JSON when tools were
	// offered, plain text otherwise) and the call's token usage.
	parse func(stdout []byte, call cliCall) (string, llm.Usage, error)
}

var cliBackends = map[llm.Format]cliBackend{
	FormatClaudeCLI: {
		binary: "claude",
		args: func(c cliCall) []string {
			a := []string{"-p", "--output-format", "json", "--tools", "", "--system-prompt", c.System,
				"--no-session-persistence", "--setting-sources", "", "--strict-mcp-config"}
			if c.Schema != "" {
				a = append(a, "--json-schema", c.Schema)
			}
			return appendFlag(appendFlag(a, "--model", c.Model), "--effort", c.Effort)
		},
		parse: parseClaudeCLI,
	},
	FormatCodexCLI: {
		binary:     "codex",
		foldSystem: true,
		args: func(c cliCall) []string {
			// features.shell_tool=false removes Codex's shell; -s read-only
			// confines anything else it could still touch. --ignore-user-config
			// must stay off: the user's login route lives in ~/.codex/config.toml.
			a := []string{"exec", "--json", "-s", "read-only", "--ephemeral", "--skip-git-repo-check",
				"--ignore-rules", "-c", "features.shell_tool=false", "-o", c.LastMessageFile}
			if c.SchemaFile != "" {
				a = append(a, "--output-schema", c.SchemaFile)
			}
			a = appendFlag(a, "-m", c.Model)
			if c.Effort != "" {
				a = append(a, "-c", fmt.Sprintf("model_reasoning_effort=%q", c.Effort))
			}
			return append(a, "-")
		},
		parse: parseCodexCLI,
	},
	FormatGrokCLI: {
		binary: "grok",
		args: func(c cliCall) []string {
			// grok reads "-p -" as the literal prompt "-", so the prompt goes by file.
			// --sandbox is left off: it fails to start bwrap on some hosts.
			a := []string{"--prompt-file", c.PromptFile, "--output-format", "json", "--tools", "",
				"--system-prompt-override", c.System, "--disable-web-search", "--no-subagents"}
			if c.Schema != "" {
				a = append(a, "--json-schema", c.Schema)
			}
			return appendFlag(appendFlag(a, "-m", c.Model), "--effort", c.Effort)
		},
		parse: parseGrokCLI,
	},
}

func appendFlag(args []string, flag, value string) []string {
	if value == "" {
		return args
	}
	return append(args, flag, value)
}

// isCLIFormat reports whether f runs through a subscription CLI.
func isCLIFormat(f llm.Format) bool {
	_, ok := cliBackends[f]
	return ok
}

func parseClaudeCLI(stdout []byte, call cliCall) (string, llm.Usage, error) {
	var out struct {
		IsError          bool            `json:"is_error"`
		Subtype          string          `json:"subtype"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return "", llm.Usage{}, fmt.Errorf("unreadable JSON output: %w", err)
	}
	if out.IsError {
		return "", llm.Usage{}, fmt.Errorf("reported an error (%s): %s", out.Subtype, out.Result)
	}
	usage := llm.Usage{
		InputTokens:      out.Usage.InputTokens,
		OutputTokens:     out.Usage.OutputTokens,
		CacheReadTokens:  out.Usage.CacheReadInputTokens,
		CacheWriteTokens: out.Usage.CacheCreationInputTokens,
	}
	if call.Schema != "" && hasJSON(out.StructuredOutput) {
		return string(out.StructuredOutput), usage, nil
	}
	return out.Result, usage, nil
}

func parseCodexCLI(stdout []byte, call cliCall) (string, llm.Usage, error) {
	var usage llm.Usage
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "error":
			return "", usage, fmt.Errorf("reported an error: %s", ev.Message)
		case "turn.failed":
			return "", usage, fmt.Errorf("turn failed: %s", ev.Error.Message)
		case "turn.completed":
			// Codex reports OpenAI semantics: input_tokens already includes the cached part.
			usage.Add(llm.Usage{
				InputTokens:     ev.Usage.InputTokens,
				OutputTokens:    ev.Usage.OutputTokens,
				CacheReadTokens: ev.Usage.CachedInputTokens,
			})
		}
	}
	reply, err := os.ReadFile(call.LastMessageFile)
	if err != nil {
		return "", usage, fmt.Errorf("wrote no final message: %w", err)
	}
	return string(reply), usage, nil
}

func parseGrokCLI(stdout []byte, call cliCall) (string, llm.Usage, error) {
	var out struct {
		Text             string          `json:"text"`
		StructuredOutput json.RawMessage `json:"structuredOutput"`
		Usage            struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return "", llm.Usage{}, fmt.Errorf("unreadable JSON output: %w", err)
	}
	usage := llm.Usage{
		InputTokens:      out.Usage.InputTokens,
		OutputTokens:     out.Usage.OutputTokens,
		CacheReadTokens:  out.Usage.CacheReadInputTokens,
		CacheWriteTokens: out.Usage.CacheCreationInputTokens,
	}
	if call.Schema != "" && hasJSON(out.StructuredOutput) {
		return string(out.StructuredOutput), usage, nil
	}
	return out.Text, usage, nil
}

func hasJSON(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

// renderCLIPrompt flattens a completion request into the single text prompt a
// CLI takes. It returns the system prompt for the CLI's system flag (empty when
// foldSystem puts it at the top of the prompt instead) and the prompt itself.
func renderCLIPrompt(req llm.CompletionRequest, foldSystem bool) (system, prompt string) {
	system = joinSystemSegments(req.System)
	var b strings.Builder
	if foldSystem && system != "" {
		b.WriteString("# System instructions\n\n")
		b.WriteString(system)
		b.WriteString("\n\n")
		system = ""
	}
	b.WriteString("# Conversation\n\nThe conversation so far, oldest first. Write the assistant's next turn.\n")
	for _, m := range req.Messages {
		fmt.Fprintf(&b, "\n## %s\n", m.Role)
		for _, blk := range m.Content {
			renderCLIBlock(&b, blk)
		}
	}
	if len(req.Tools) > 0 {
		b.WriteString("\n# Tools\n\nYou can call these tools. Each input_schema is the JSON Schema of that tool's arguments.\n")
		for _, t := range req.Tools {
			schema, _ := json.Marshal(t.InputSchema)
			fmt.Fprintf(&b, "\n## %s\n%s\ninput_schema: %s\n", t.Name, strings.TrimSpace(t.Description), schema)
		}
		b.WriteString("\n# How to answer\n\n" +
			"To call tools, put one entry per call in tool_calls: name is the tool name, and arguments is the " +
			"arguments as a JSON object encoded as a string, for example \"{\\\"path\\\": \\\"/tmp\\\"}\". " +
			"Put any text you want to say before the calls in text. The tool results come back in the next turn.\n" +
			"To finish, leave tool_calls empty and put your answer in text.\n")
	}
	return system, b.String()
}

func renderCLIBlock(b *strings.Builder, blk llm.ContentBlock) {
	switch blk.Type {
	case llm.BlockText:
		b.WriteString(blk.Text)
		b.WriteString("\n")
	case llm.BlockToolUse:
		input := string(blk.Input)
		if input == "" {
			input = "{}"
		}
		fmt.Fprintf(b, "[tool call id=%s name=%s]\n%s\n[/tool call]\n", blk.ID, blk.Name, input)
	case llm.BlockToolResult:
		fmt.Fprintf(b, "[tool result tool_use_id=%s is_error=%t]\n", blk.ToolUseID, blk.IsError)
		for _, c := range blk.Content {
			if c.Type == llm.BlockText {
				b.WriteString(c.Text)
				b.WriteString("\n")
			}
		}
		b.WriteString("[/tool result]\n")
	}
}

func joinSystemSegments(segs []string) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// cliToolCallSchema is the JSON Schema a CLI's structured output must match
// when tools are offered. It keeps to OpenAI strict mode (every property
// required, no additional properties), which cannot express an arbitrary
// object, so each call's arguments travel as a JSON-encoded string.
func cliToolCallSchema(tools []llm.ToolSchema) []byte {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"text", "tool_calls"},
		"properties": map[string]any{
			"text": map[string]any{"type": "string"},
			"tool_calls": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"name", "arguments"},
					"properties": map[string]any{
						"name":      map[string]any{"type": "string", "enum": names},
						"arguments": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(schema)
	return b
}

// decodeCLIReply turns a CLI's final reply into an assistant message and its
// stop reason. Without tools the reply is the plain answer text. With tools it
// must be the cliToolCallSchema JSON.
func decodeCLIReply(reply string, withTools bool) (llm.Message, string, error) {
	msg := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{}}
	if !withTools {
		if text := strings.TrimSpace(reply); text != "" {
			msg.Content = append(msg.Content, llm.TextBlock(text))
		}
		return msg, "end_turn", nil
	}
	var out struct {
		Text      string `json:"text"`
		ToolCalls []struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply)), &out); err != nil {
		return llm.Message{}, "", fmt.Errorf("reply does not match the tool-call schema: %w", err)
	}
	if text := strings.TrimSpace(out.Text); text != "" {
		msg.Content = append(msg.Content, llm.TextBlock(text))
	}
	for _, c := range out.ToolCalls {
		msg.Content = append(msg.Content, llm.ContentBlock{
			Type:  llm.BlockToolUse,
			ID:    newCLIToolID(),
			Name:  c.Name,
			Input: toolArgumentsJSON(c.Arguments),
		})
	}
	if len(out.ToolCalls) > 0 {
		return msg, "tool_use", nil
	}
	return msg, "end_turn", nil
}

// toolArgumentsJSON returns the arguments as compact JSON, or {} when they are
// not a JSON object: transcripts persist tool input as raw JSON, so it must
// always parse.
func toolArgumentsJSON(args string) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &obj) != nil || obj == nil {
		return json.RawMessage("{}")
	}
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(args)) != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(buf.Bytes())
}

func newCLIToolID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "cli_" + hex.EncodeToString(b[:])
}

// cliUsageLimit matches a CLI saying the subscription's usage allowance is
// spent, as opposed to an ordinary failure.
var cliUsageLimit = regexp.MustCompile(`(?i)usage limit|hit your (?:usage )?limit|(?:usage|session|weekly|daily|monthly|5-hour|plan|subscription) limit (?:reached|exceeded)`)

// cliFailure builds the error for a failed CLI call. A spent subscription
// allowance is reworded so IsQuotaExhaustedMessage recognizes it and the task
// router fails over to the next profile; output is the CLI's stderr/stdout.
func cliFailure(format llm.Format, what, output string) error {
	if line := cliUsageLimit.FindString(output); line != "" {
		detail := lineAround(output, line)
		msg := fmt.Sprintf("%s: quota exhausted: the subscription usage limit was reached (%s)", format, detail)
		// The CLI's own words may carry a phrase that IsQuotaExhaustedMessage
		// reads as a transient rate limit; the failover signal must win.
		if !IsQuotaExhaustedMessage(msg) {
			msg = fmt.Sprintf("%s: quota exhausted: the subscription usage limit was reached", format)
		}
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %s: %s", format, what, outputTail(output))
}

// lineAround returns the trimmed line of output containing match, clipped.
func lineAround(output, match string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, match) {
			return clipString(strings.TrimSpace(line), 300)
		}
	}
	return match
}

// outputTail keeps the end of a CLI's output, where its error usually is.
func outputTail(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "(no output)"
	}
	const max = 2000
	if len(output) > max {
		return "…" + output[len(output)-max:]
	}
	return output
}

func clipString(s string, max int) string {
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// cliWaitDelay bounds how long a cancelled CLI's leftover child processes may
// hold its output pipes open before Wait gives up on them.
const cliWaitDelay = 5 * time.Second

// cliProvider implements llm.Provider by running one CLI process per call.
type cliProvider struct {
	format  llm.Format
	backend cliBackend
	model   string
	effort  string
}

func newCLIProvider(c Config) *cliProvider {
	return &cliProvider{format: c.Format, backend: cliBackends[c.Format], model: c.Model, effort: c.ReasoningEffort}
}

func (p *cliProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	bin, err := exec.LookPath(p.backend.binary)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("%s: the %q CLI was not found on PATH: install it and log in to it on the host that runs the ARTEX server: %w",
			p.format, p.backend.binary, err)
	}
	dir, err := os.MkdirTemp("", "artex-"+string(p.format)+"-")
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("%s: %w", p.format, err)
	}
	defer os.RemoveAll(dir)

	system, prompt := renderCLIPrompt(req, p.backend.foldSystem)
	call := cliCall{
		Model:           p.model,
		Effort:          p.effort,
		System:          system,
		PromptFile:      filepath.Join(dir, "prompt.txt"),
		LastMessageFile: filepath.Join(dir, "last-message.txt"),
	}
	if err := os.WriteFile(call.PromptFile, []byte(prompt), 0o600); err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("%s: %w", p.format, err)
	}
	withTools := len(req.Tools) > 0
	if withTools {
		call.Schema = string(cliToolCallSchema(req.Tools))
		call.SchemaFile = filepath.Join(dir, "schema.json")
		if err := os.WriteFile(call.SchemaFile, []byte(call.Schema), 0o600); err != nil {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("%s: %w", p.format, err)
		}
	}

	cmd := exec.CommandContext(ctx, bin, p.backend.args(call)...)
	// An empty scratch dir keeps the CLI from picking up a project's files or
	// instructions; the inherited env keeps HOME, where its login lives.
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.WaitDelay = cliWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return llm.Message{}, "", llm.Usage{}, ctx.Err()
	}
	output := stderr.String() + "\n" + stdout.String()
	if runErr != nil {
		return llm.Message{}, "", llm.Usage{}, cliFailure(p.format, fmt.Sprintf("%s exited: %v", p.backend.binary, runErr), output)
	}
	reply, usage, err := p.backend.parse(stdout.Bytes(), call)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, cliFailure(p.format, fmt.Sprintf("%s %v", p.backend.binary, err), output)
	}
	msg, stop, err := decodeCLIReply(reply, withTools)
	if err != nil {
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("%s: %w: %s", p.format, err, outputTail(reply))
	}
	return msg, stop, usage, nil
}

// Stream runs Complete and replays its message as stream events, so the
// harness's Accumulator rebuilds the same message a non-streaming call returns.
func (p *cliProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		msg, stop, usage, err := p.Complete(ctx, req)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		events := []llm.StreamEvent{{Type: llm.SEMessageStart, Usage: usage}}
		for i, blk := range msg.Content {
			switch blk.Type {
			case llm.BlockText:
				events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: blk.Text})
			case llm.BlockToolUse:
				events = append(events,
					llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: blk.ID, ToolName: blk.Name, ToolIndex: i},
					llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(blk.Input)})
			}
		}
		events = append(events,
			llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: stop},
			llm.StreamEvent{Type: llm.SEMessageStop})
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}
