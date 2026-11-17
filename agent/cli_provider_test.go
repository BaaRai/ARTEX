package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

var addTool = llm.ToolSchema{
	Name:        "add",
	Description: "Add two numbers.",
	InputSchema: map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}},
		"required":   []string{"a", "b"},
	},
}

func transcriptRequest(tools ...llm.ToolSchema) llm.CompletionRequest {
	return llm.CompletionRequest{
		System: []string{"You are a pentest agent.", "  ", "Stay in scope."},
		Messages: []llm.Message{
			llm.UserText("What is 2+3?"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.BlockThinking, Thinking: "secret chain of thought"},
				llm.TextBlock("Let me add them."),
				{Type: llm.BlockToolUse, ID: "call_1", Name: "add", Input: json.RawMessage(`{"a":2,"b":3}`)},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				llm.ToolResultText("call_1", "5", false),
			}},
		},
		Tools: tools,
	}
}

func TestRenderCLIPromptTranscript(t *testing.T) {
	system, prompt := renderCLIPrompt(transcriptRequest(), false)
	if system != "You are a pentest agent.\n\nStay in scope." {
		t.Fatalf("system = %q", system)
	}
	if strings.Contains(prompt, "pentest agent") {
		t.Fatal("system prompt leaked into the prompt of a CLI that takes a system flag")
	}
	for _, want := range []string{
		"## user\nWhat is 2+3?",
		"## assistant\nLet me add them.",
		"[tool call id=call_1 name=add]\n{\"a\":2,\"b\":3}\n[/tool call]",
		"[tool result tool_use_id=call_1 is_error=false]\n5\n[/tool result]",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "secret chain of thought") {
		t.Error("thinking block was rendered into the prompt")
	}
	if strings.Contains(prompt, "# Tools") || strings.Contains(prompt, "tool_calls") {
		t.Error("a request without tools rendered a tools section")
	}
	if strings.Index(prompt, "What is 2+3?") > strings.Index(prompt, "[tool result") {
		t.Error("messages are out of order")
	}
}

func TestRenderCLIPromptFoldsSystemAndListsTools(t *testing.T) {
	system, prompt := renderCLIPrompt(transcriptRequest(addTool), true)
	if system != "" {
		t.Fatalf("folded system still returned for the flag: %q", system)
	}
	if !strings.HasPrefix(prompt, "# System instructions\n\nYou are a pentest agent.\n\nStay in scope.") {
		t.Fatalf("system prompt not at the top of the prompt:\n%s", prompt)
	}
	for _, want := range []string{"## add\nAdd two numbers.", `"required":["a","b"]`, "tool_calls", "JSON object encoded as a string", "leave tool_calls empty"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("tools section is missing %q:\n%s", want, prompt)
		}
	}
}

// The schema must stay inside OpenAI strict mode, the strictest of the three
// CLIs' structured-output engines: every object closes additionalProperties
// and requires all of its properties.
func TestCLIToolCallSchemaIsStrict(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal(cliToolCallSchema([]llm.ToolSchema{addTool, {Name: "http_get"}}), &s); err != nil {
		t.Fatal(err)
	}
	var check func(path string, node map[string]any)
	check = func(path string, node map[string]any) {
		if props, ok := node["properties"].(map[string]any); ok {
			if node["additionalProperties"] != false {
				t.Errorf("%s: additionalProperties must be false", path)
			}
			var required []string
			for _, r := range node["required"].([]any) {
				required = append(required, r.(string))
			}
			for name, p := range props {
				if !slices.Contains(required, name) {
					t.Errorf("%s: property %q is not required", path, name)
				}
				check(path+"."+name, p.(map[string]any))
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			check(path+"[]", items)
		}
	}
	check("$", s)
	call := s["properties"].(map[string]any)["tool_calls"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if got := call["name"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []any{"add", "http_get"}) {
		t.Errorf("name enum = %v, want the offered tool names", got)
	}
	if call["arguments"].(map[string]any)["type"] != "string" {
		t.Error("arguments must travel as a JSON-encoded string")
	}
}

func TestDecodeCLIReply(t *testing.T) {
	t.Run("plain text without tools", func(t *testing.T) {
		msg, stop, err := decodeCLIReply("  OK\n", false)
		if err != nil || stop != "end_turn" {
			t.Fatalf("stop=%q err=%v", stop, err)
		}
		if msg.Role != llm.RoleAssistant || len(msg.Content) != 1 || msg.Text() != "OK" {
			t.Fatalf("message = %+v", msg)
		}
	})
	t.Run("tool calls", func(t *testing.T) {
		reply := `{"text":"Adding.","tool_calls":[{"name":"add","arguments":"{\"a\": 2, \"b\": 3}"},{"name":"add","arguments":"{\"a\":1,\"b\":1}"}]}`
		msg, stop, err := decodeCLIReply(reply, true)
		if err != nil || stop != "tool_use" {
			t.Fatalf("stop=%q err=%v", stop, err)
		}
		if msg.Content[0].Type != llm.BlockText || msg.Content[0].Text != "Adding." {
			t.Fatalf("first block = %+v, want the text", msg.Content[0])
		}
		uses := msg.ToolUses()
		if len(uses) != 2 || uses[0].Name != "add" || string(uses[0].Input) != `{"a":2,"b":3}` {
			t.Fatalf("tool uses = %+v", uses)
		}
		if uses[0].ID == uses[1].ID || !strings.HasPrefix(uses[0].ID, "cli_") {
			t.Fatalf("tool ids must be unique per call: %q %q", uses[0].ID, uses[1].ID)
		}
	})
	t.Run("final answer with tools offered", func(t *testing.T) {
		msg, stop, err := decodeCLIReply(`{"text":"It is 5.","tool_calls":[]}`, true)
		if err != nil || stop != "end_turn" || msg.Text() != "It is 5." || len(msg.ToolUses()) != 0 {
			t.Fatalf("msg=%+v stop=%q err=%v", msg, stop, err)
		}
	})
	t.Run("arguments that are not a JSON object become {}", func(t *testing.T) {
		for _, args := range []string{"", "not json", "[1,2]", `"x"`, "null", "{broken"} {
			raw, _ := json.Marshal(map[string]any{"text": "", "tool_calls": []any{map[string]string{"name": "add", "arguments": args}}})
			msg, _, err := decodeCLIReply(string(raw), true)
			if err != nil {
				t.Fatalf("%q: %v", args, err)
			}
			if in := msg.ToolUses()[0].Input; string(in) != "{}" || !json.Valid(in) {
				t.Errorf("arguments %q decoded to input %q, want {}", args, in)
			}
			if len(msg.Content) != 1 {
				t.Errorf("empty text produced a text block: %+v", msg.Content)
			}
		}
	})
	t.Run("reply that is not the schema", func(t *testing.T) {
		if _, _, err := decodeCLIReply("Sure, I will add them.", true); err == nil {
			t.Fatal("a free-text reply was accepted where tool-call JSON was required")
		}
	})
}

func TestCLIUsageLimitFailsOver(t *testing.T) {
	for _, tc := range []struct {
		output string
		quota  bool
	}{
		{`{"type":"error","message":"You've hit your usage limit. Upgrade to Pro or try again at 3:04 PM."}`, true},
		{"You've hit your limit · resets 3pm (Europe/Paris)", true},
		{"Claude AI usage limit reached|1767225600", true},
		{"5-hour limit reached ∙ resets 9pm", true},
		// The CLI's wording carries a transient-rate-limit phrase; failover must still win.
		{"usage limit reached: too many requests per minute on your plan", true},
		{"Error: connect ECONNREFUSED 127.0.0.1:443", false},
		{"Not logged in. Please run /login", false},
	} {
		err := cliFailure(FormatCodexCLI, "codex exited: exit status 1", tc.output)
		if got := IsQuotaExhaustedMessage(err.Error()); got != tc.quota {
			t.Errorf("%q: quota exhausted = %v, want %v (error %q)", tc.output, got, tc.quota, err)
		}
		if !strings.Contains(err.Error(), "codex-cli") {
			t.Errorf("error does not name the format: %q", err)
		}
	}
}

// Every registered CLI must be keyless in db.IsKeylessFormat too, or its
// profiles would be rejected for lacking a key.
func TestConfigForCLIFormats(t *testing.T) {
	for f := range cliBackends {
		c := ConfigFrom(" "+string(f)+" ", "", "https://api.example.com", "sk-stale", "http://proxy:3128")
		if c.Format != f || c.Model != "" || c.BaseURL != "" || c.APIKey != "" || c.Proxy != "" {
			t.Errorf("ConfigFrom(%s) = %+v, want the format, the CLI's default model and no endpoint, key or proxy", f, c)
		}
		if c.NeedsAPIKey() {
			t.Errorf("%s needs an API key", f)
		}
		if c.Provider() != string(f) {
			t.Errorf("Provider() = %q, want %q", c.Provider(), f)
		}
		if p, err := c.NewProvider(); err != nil {
			t.Errorf("%s: NewProvider: %v", f, err)
		} else if _, ok := p.(*cliProvider); !ok {
			t.Errorf("%s: NewProvider built %T, want the CLI provider", f, p)
		}
		if got := ConfigFrom(string(f), "sonnet", "", "", "").Model; got != "sonnet" {
			t.Errorf("%s: model = %q, want the one given", f, got)
		}
	}
}

func TestFromEnvCLIFormatNeedsNoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ARTEX_LLM_BASE_URL", "https://api.example.com")
	t.Setenv("ARTEX_LLM_MODEL", "")
	t.Setenv("ARTEX_LLM_PROVIDER", " Codex-CLI ")
	c, ok := FromEnv()
	if !ok {
		t.Fatal("FromEnv rejected a keyless CLI format")
	}
	if c.Format != FormatCodexCLI || c.Model != "" || c.BaseURL != "" || c.APIKey != "" {
		t.Fatalf("FromEnv = %+v", c)
	}
}

// fakeCLI installs an executable shell script named name at the front of PATH.
// Every script records its argv (one per line) to $FAKE_CLI_ARGS and the prompt
// it was given to $FAKE_CLI_PROMPT, then prints the canned output.
func fakeCLI(t *testing.T, name, script string) (argsFile, promptFile string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_CLI_ARGS\"\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile, promptFile = filepath.Join(dir, "args"), filepath.Join(dir, "prompt")
	t.Setenv("FAKE_CLI_ARGS", argsFile)
	t.Setenv("FAKE_CLI_PROMPT", promptFile)
	return argsFile, promptFile
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// flagValue returns the argument after flag, or fails the test.
func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("argv has no %s: %q", flag, args)
	}
	return args[i+1]
}

const toolReply = `{"text":"Adding.","tool_calls":[{"name":"add","arguments":"{\"a\":2,\"b\":3}"}]}`

func assertAddCall(t *testing.T, msg llm.Message, stop string) {
	t.Helper()
	uses := msg.ToolUses()
	if stop != "tool_use" || msg.Text() != "Adding." || len(uses) != 1 || uses[0].Name != "add" || string(uses[0].Input) != `{"a":2,"b":3}` {
		t.Fatalf("msg=%+v stop=%q, want text Adding. and one add call", msg, stop)
	}
}

func TestCLIProviderClaude(t *testing.T) {
	argsFile, promptFile := fakeCLI(t, "claude", `cat > "$FAKE_CLI_PROMPT"
printf '{"type":"result","subtype":"success","is_error":false,"result":"","structured_output":%s,"usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":3}}' "$FAKE_REPLY"
`)
	t.Setenv("FAKE_REPLY", toolReply)
	p := newCLIProvider(Config{Format: FormatClaudeCLI, Model: "sonnet", ReasoningEffort: "high"})
	msg, stop, usage, err := p.Complete(context.Background(), transcriptRequest(addTool))
	if err != nil {
		t.Fatal(err)
	}
	assertAddCall(t, msg, stop)
	if usage != (llm.Usage{InputTokens: 12, OutputTokens: 5, CacheReadTokens: 7, CacheWriteTokens: 3}) {
		t.Errorf("usage = %+v", usage)
	}
	args := readArgs(t, argsFile)
	if flagValue(t, args, "--model") != "sonnet" || flagValue(t, args, "--effort") != "high" || flagValue(t, args, "--tools") != "" {
		t.Errorf("argv = %q", args)
	}
	if !strings.Contains(flagValue(t, args, "--system-prompt"), "pentest agent") {
		t.Errorf("system prompt not passed by flag: %q", args)
	}
	if !json.Valid([]byte(flagValue(t, args, "--json-schema"))) {
		t.Errorf("--json-schema is not JSON: %q", args)
	}
	if prompt, _ := os.ReadFile(promptFile); !strings.Contains(string(prompt), "What is 2+3?") {
		t.Errorf("transcript not on stdin: %q", prompt)
	}
}

func TestCLIProviderClaudePlainReplyWithoutTools(t *testing.T) {
	argsFile, _ := fakeCLI(t, "claude", `printf '{"type":"result","subtype":"success","is_error":false,"result":"OK","usage":{"input_tokens":1,"output_tokens":1}}'`)
	msg, stop, _, err := newCLIProvider(Config{Format: FormatClaudeCLI}).Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("ping")}})
	if err != nil || stop != "end_turn" || msg.Text() != "OK" {
		t.Fatalf("msg=%+v stop=%q err=%v", msg, stop, err)
	}
	args := readArgs(t, argsFile)
	if slices.Contains(args, "--json-schema") || slices.Contains(args, "--model") || slices.Contains(args, "--effort") {
		t.Errorf("argv carries a schema, model or effort nobody asked for: %q", args)
	}
}

func TestCLIProviderCodex(t *testing.T) {
	argsFile, promptFile := fakeCLI(t, "codex", `cat > "$FAKE_CLI_PROMPT"
out=""; prev=""
for a in "$@"; do [ "$prev" = "-o" ] && out="$a"; prev="$a"; done
printf '%s' "$FAKE_REPLY" > "$out"
echo '{"type":"thread.started","thread_id":"t1"}'
echo '{"type":"turn.completed","usage":{"input_tokens":20,"cached_input_tokens":8,"output_tokens":4}}'
`)
	t.Setenv("FAKE_REPLY", toolReply)
	p := newCLIProvider(Config{Format: FormatCodexCLI, Model: "gpt-5.5", ReasoningEffort: "low"})
	msg, stop, usage, err := p.Complete(context.Background(), transcriptRequest(addTool))
	if err != nil {
		t.Fatal(err)
	}
	assertAddCall(t, msg, stop)
	if usage != (llm.Usage{InputTokens: 20, OutputTokens: 4, CacheReadTokens: 8}) {
		t.Errorf("usage = %+v", usage)
	}
	args := readArgs(t, argsFile)
	if args[len(args)-1] != "-" || flagValue(t, args, "-m") != "gpt-5.5" || !slices.Contains(args, `model_reasoning_effort="low"`) || !slices.Contains(args, "features.shell_tool=false") {
		t.Errorf("argv = %q", args)
	}
	if slices.Contains(args, "--ignore-user-config") {
		t.Error("--ignore-user-config drops the user's Codex login route")
	}
	if flagValue(t, args, "--output-schema") == "" {
		t.Errorf("no schema file: %q", args)
	}
	prompt, _ := os.ReadFile(promptFile)
	if !strings.HasPrefix(string(prompt), "# System instructions\n\nYou are a pentest agent.") {
		t.Errorf("codex has no system flag, so the system prompt must lead the prompt: %q", prompt)
	}
}

func TestCLIProviderCodexErrorEvent(t *testing.T) {
	fakeCLI(t, "codex", `cat > /dev/null
echo '{"type":"thread.started","thread_id":"t1"}'
echo '{"type":"error","message":"You'"'"'ve hit your usage limit. Upgrade to Pro or try again at 3:04 PM."}'
echo '{"type":"turn.failed","error":{"message":"usage limit"}}'
exit 1
`)
	_, _, _, err := newCLIProvider(Config{Format: FormatCodexCLI}).Complete(context.Background(), transcriptRequest())
	if err == nil || !IsQuotaExhaustedMessage(err.Error()) {
		t.Fatalf("err = %v, want a quota-exhausted error so the task fails over", err)
	}
}

func TestCLIProviderCodexRecoversFromReconnect(t *testing.T) {
	fakeCLI(t, "codex", `cat > /dev/null
out=""; prev=""
for a in "$@"; do [ "$prev" = "-o" ] && out="$a"; prev="$a"; done
echo '{"type":"thread.started","thread_id":"t1"}'
echo '{"type":"error","message":"Reconnecting... 2/5 (stream disconnected before completion)"}'
printf '%s' "$FAKE_REPLY" > "$out"
echo '{"type":"turn.completed","usage":{"input_tokens":20,"cached_input_tokens":8,"output_tokens":4}}'
`)
	t.Setenv("FAKE_REPLY", toolReply)
	msg, stop, _, err := newCLIProvider(Config{Format: FormatCodexCLI}).Complete(context.Background(), transcriptRequest(addTool))
	if err != nil {
		t.Fatalf("a reconnect that later completes must succeed: %v", err)
	}
	assertAddCall(t, msg, stop)
}

func TestCLIProviderCodexNoCompletedTurn(t *testing.T) {
	fakeCLI(t, "codex", `cat > /dev/null
echo '{"type":"thread.started","thread_id":"t1"}'
echo '{"type":"error","message":"Reconnecting... 5/5 (unexpected status 401 Unauthorized)"}'
`)
	_, _, _, err := newCLIProvider(Config{Format: FormatCodexCLI}).Complete(context.Background(), transcriptRequest(addTool))
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("err = %v, want the last Codex error when no turn completed", err)
	}
}

func TestCLIProviderGrok(t *testing.T) {
	argsFile, promptFile := fakeCLI(t, "grok", `pf=""; prev=""
for a in "$@"; do [ "$prev" = "--prompt-file" ] && pf="$a"; prev="$a"; done
cp "$pf" "$FAKE_CLI_PROMPT"
printf '{"text":"","structuredOutput":%s,"stopReason":"end_turn","usage":{"input_tokens":30,"output_tokens":6,"cache_read_input_tokens":9}}' "$FAKE_REPLY"
`)
	t.Setenv("FAKE_REPLY", toolReply)
	p := newCLIProvider(Config{Format: FormatGrokCLI, Model: "grok-4.7", ReasoningEffort: "high"})
	msg, stop, usage, err := p.Complete(context.Background(), transcriptRequest(addTool))
	if err != nil {
		t.Fatal(err)
	}
	assertAddCall(t, msg, stop)
	if usage != (llm.Usage{InputTokens: 30, OutputTokens: 6, CacheReadTokens: 9}) {
		t.Errorf("usage = %+v", usage)
	}
	args := readArgs(t, argsFile)
	if flagValue(t, args, "-m") != "grok-4.7" || flagValue(t, args, "--effort") != "high" || flagValue(t, args, "--tools") != "" {
		t.Errorf("argv = %q", args)
	}
	if slices.Contains(args, "--sandbox") {
		t.Error("--sandbox fails to start on hosts without a working bwrap")
	}
	if !strings.Contains(flagValue(t, args, "--system-prompt-override"), "pentest agent") {
		t.Errorf("system prompt not passed by flag: %q", args)
	}
	if prompt, _ := os.ReadFile(promptFile); !strings.Contains(string(prompt), "What is 2+3?") {
		t.Errorf("transcript not in the prompt file: %q", prompt)
	}
}

func TestCLIProviderNonZeroExit(t *testing.T) {
	fakeCLI(t, "claude", `echo "fatal: something broke" >&2
exit 3
`)
	_, _, _, err := newCLIProvider(Config{Format: FormatClaudeCLI}).Complete(context.Background(), transcriptRequest())
	if err == nil || !strings.Contains(err.Error(), "claude-cli") || !strings.Contains(err.Error(), "something broke") {
		t.Fatalf("err = %v, want the format and the CLI's stderr", err)
	}
	if IsQuotaExhaustedMessage(err.Error()) {
		t.Fatal("an ordinary CLI failure was reported as quota exhaustion")
	}
}

func TestCLIProviderClaudeIsError(t *testing.T) {
	fakeCLI(t, "claude", `printf '{"type":"result","subtype":"success","is_error":true,"result":"You'"'"'ve hit your limit · resets 3pm"}'`)
	_, _, _, err := newCLIProvider(Config{Format: FormatClaudeCLI}).Complete(context.Background(), transcriptRequest())
	if err == nil || !IsQuotaExhaustedMessage(err.Error()) {
		t.Fatalf("err = %v, want a quota-exhausted error", err)
	}
}

func TestCLIProviderMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, _, err := newCLIProvider(Config{Format: FormatGrokCLI}).Complete(context.Background(), transcriptRequest())
	if err == nil || !strings.Contains(err.Error(), "install") || !strings.Contains(err.Error(), `"grok"`) {
		t.Fatalf("err = %v, want an install-and-log-in hint naming the binary", err)
	}
}

// The harness streams by default; replaying Complete's message as events must
// rebuild the same message through the Accumulator.
func TestCLIProviderStreamMatchesComplete(t *testing.T) {
	fakeCLI(t, "claude", `printf '{"type":"result","subtype":"success","is_error":false,"result":"","structured_output":%s,"usage":{"input_tokens":12,"output_tokens":5}}' "$FAKE_REPLY"`)
	t.Setenv("FAKE_REPLY", `{"text":"Two calls.","tool_calls":[{"name":"add","arguments":"{\"a\":2,\"b\":3}"},{"name":"add","arguments":"oops"}]}`)
	p := newCLIProvider(Config{Format: FormatClaudeCLI})
	req := transcriptRequest(addTool)

	want, wantStop, wantUsage, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	acc := llm.NewAccumulator()
	for ev, err := range p.Stream(context.Background(), req) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(ev)
	}
	got := acc.Message()
	// Tool ids are random per call; each run makes its own.
	for _, m := range []*llm.Message{&want, &got} {
		for i := range m.Content {
			m.Content[i].ID = ""
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streamed message = %+v\nwant %+v", got, want)
	}
	if acc.StopReason != wantStop || acc.Usage != wantUsage {
		t.Fatalf("stream stop=%q usage=%+v, want %q %+v", acc.StopReason, acc.Usage, wantStop, wantUsage)
	}
}

func TestCLIProviderStreamError(t *testing.T) {
	fakeCLI(t, "grok", "exit 1\n")
	var gotErr error
	for _, err := range newCLIProvider(Config{Format: FormatGrokCLI}).Stream(context.Background(), transcriptRequest()) {
		gotErr = err
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "grok-cli") {
		t.Fatalf("stream error = %v", gotErr)
	}
}
