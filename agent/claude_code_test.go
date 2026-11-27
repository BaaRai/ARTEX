package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// fakeClaudeCLI installs a shell script standing in for the claude binary. It
// records its arguments, environment, stdin and system prompt file under dir and
// prints dir/response.json, so tests drive the real exec path.
func fakeClaudeCLI(t *testing.T, response string) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
D='` + dir + `'
: > "$D/args.txt"
sp=""
prev=""
for a in "$@"; do
  printf '[%s]\n' "$a" >> "$D/args.txt"
  if [ "$prev" = "--system-prompt-file" ]; then sp="$a"; fi
  prev="$a"
done
env > "$D/env.txt"
cat > "$D/stdin.txt"
[ -n "$sp" ] && cp "$sp" "$D/system.txt"
echo x >> "$D/calls.txt"
cat "$D/response.json"
`
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(response), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envClaudeCodeBin, bin)
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func claudeResult(text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": text, "stop_reason": "end_turn",
		"usage": map[string]int{"input_tokens": 10, "output_tokens": 20, "cache_read_input_tokens": 3, "cache_creation_input_tokens": 5},
	})
	return string(b)
}

func claudeCodeProvider(t *testing.T, token string) llm.Provider {
	t.Helper()
	c := ConfigFrom("claude-code", "opus", "", token, "")
	c.Retry.ConnectAttempts = -1
	p, err := c.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func toolRequest() llm.CompletionRequest {
	return llm.CompletionRequest{
		System: []string{"You are the ARTEX worker."},
		Tools: []llm.ToolSchema{{Name: "Bash", Description: "Run a shell command", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}},
		}}},
		Messages: []llm.Message{
			llm.UserText("scan example.test"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.TextBlock("Checking DNS first."),
				{Type: llm.BlockToolUse, ID: "toolu_1", Name: "Bash", Input: json.RawMessage(`{"command":"dig example.test"}`)},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("toolu_1", "example.test. 300 IN A 10.0.0.1", false)}},
		},
	}
}

func TestClaudeCodeConfig(t *testing.T) {
	c := ConfigFrom("claude-code", "", "https://ignored.example", "", "")
	if c.Format != FormatClaudeCode || c.Provider() != "claude-code" {
		t.Fatalf("format = %q provider = %q", c.Format, c.Provider())
	}
	if c.BaseURL != "" || c.Model != DefaultClaudeCodeModel {
		t.Fatalf("base url = %q model = %q", c.BaseURL, c.Model)
	}
	if c.NeedsAPIKey() || !IsKeylessFormat("claude-code") || IsKeylessFormat("anthropic") {
		t.Fatal("claude-code must be keyless and anthropic must not")
	}
	if _, err := ConfigFrom("claude-code", "--dangerous", "", "", "").NewProvider(); err == nil {
		t.Fatal("a model name that looks like a flag must be rejected")
	}
}

func TestClaudeCodeFromEnv(t *testing.T) {
	t.Setenv("ARTEX_LLM_PROVIDER", "claude-code")
	t.Setenv("ARTEX_LLM_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api-should-not-be-used")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat-test")
	c, ok := FromEnv()
	if !ok || c.Format != FormatClaudeCode || c.APIKey != "sk-ant-oat-test" || c.Model != DefaultClaudeCodeModel {
		t.Fatalf("FromEnv = %+v ok=%v", c, ok)
	}
}

func TestClaudeCodeToolCallRoundTrip(t *testing.T) {
	dir := fakeClaudeCLI(t, claudeResult(`{"text":"Now the web port.","tool_calls":[{"name":"Bash","input":{"command":"curl -sI http://10.0.0.1"}}]}`))
	t.Setenv("ARTEX_PG_PASSWORD", "server-secret")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api-server-key")

	msg, stop, usage, err := claudeCodeProvider(t, "sk-ant-oat-profile").Complete(context.Background(), toolRequest())
	if err != nil {
		t.Fatal(err)
	}
	if stop != "tool_use" {
		t.Fatalf("stop reason = %q", stop)
	}
	uses := msg.ToolUses()
	if len(uses) != 1 || uses[0].Name != "Bash" || !strings.HasPrefix(uses[0].ID, "toolu_cc_") {
		t.Fatalf("tool uses = %+v", uses)
	}
	var in map[string]string
	if err := json.Unmarshal(uses[0].Input, &in); err != nil || in["command"] != "curl -sI http://10.0.0.1" {
		t.Fatalf("tool input = %s (%v)", uses[0].Input, err)
	}
	if msg.Content[0].Text != "Now the web port." {
		t.Fatalf("text = %+v", msg.Content[0])
	}
	if usage.InputTokens != 18 || usage.OutputTokens != 20 || usage.CacheReadTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}

	args := readFile(t, filepath.Join(dir, "args.txt"))
	for _, want := range []string{"[-p]", "[--tools]\n[]", "[--strict-mcp-config]", "[--setting-sources]\n[]", "[--disable-slash-commands]", "[--no-session-persistence]", "[--model]\n[opus]"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	env := readFile(t, filepath.Join(dir, "env.txt"))
	if strings.Contains(env, "server-secret") || strings.Contains(env, "sk-ant-api-server-key") {
		t.Errorf("server secrets leaked into the CLI env:\n%s", env)
	}
	if !strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat-profile") {
		t.Errorf("profile token missing from the CLI env:\n%s", env)
	}
	system := readFile(t, filepath.Join(dir, "system.txt"))
	if !strings.HasPrefix(system, "You are the ARTEX worker.") || !strings.Contains(system, `"name": "Bash"`) || !strings.Contains(system, "tool_calls") {
		t.Errorf("system prompt:\n%s", system)
	}
	stdin := readFile(t, filepath.Join(dir, "stdin.txt"))
	for _, want := range []string{"scan example.test", `<tool_call id="toolu_1" name="Bash">{"command":"dig example.test"}</tool_call>`, `<tool_result tool_use_id="toolu_1" is_error="false">`, "10.0.0.1"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("prompt missing %q:\n%s", want, stdin)
		}
	}
}

func TestClaudeCodeStream(t *testing.T) {
	fakeClaudeCLI(t, claudeResult("```json\n{\"text\":\"\",\"tool_calls\":[{\"name\":\"Bash\",\"input\":\"{\\\"command\\\":\\\"id\\\"}\"}]}\n```"))
	var sawTool bool
	var toolJSON, stop string
	for ev, err := range claudeCodeProvider(t, "").Stream(context.Background(), toolRequest()) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case llm.SEToolUseStart:
			sawTool = ev.ToolName == "Bash"
		case llm.SEToolInputJSON:
			toolJSON += ev.Text
		case llm.SEMessageDelta:
			stop = ev.StopReason
		}
	}
	if !sawTool || toolJSON != `{"command":"id"}` || stop != "tool_use" {
		t.Fatalf("tool=%v json=%q stop=%q", sawTool, toolJSON, stop)
	}
}

func TestClaudeCodeProseFallsBackToText(t *testing.T) {
	fakeClaudeCLI(t, claudeResult("The scan is complete; no open ports."))
	msg, stop, _, err := claudeCodeProvider(t, "").Complete(context.Background(), toolRequest())
	if err != nil {
		t.Fatal(err)
	}
	if stop != "end_turn" || len(msg.ToolUses()) != 0 || msg.Content[0].Text != "The scan is complete; no open ports." {
		t.Fatalf("stop=%q content=%+v", stop, msg.Content)
	}
}

func TestClaudeCodeNoToolsIsPlainText(t *testing.T) {
	dir := fakeClaudeCLI(t, claudeResult(`{"text":"looks like json"}`))
	req := llm.CompletionRequest{System: []string{"Reply OK."}, Messages: []llm.Message{llm.UserText("ping")}}
	msg, _, _, err := claudeCodeProvider(t, "").Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content[0].Text != `{"text":"looks like json"}` {
		t.Fatalf("content = %+v", msg.Content)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "system.txt")), "Reply protocol") {
		t.Fatal("the tool protocol must not be added when no tools are offered")
	}
}

func TestClaudeCodeAuthErrorIsNotRetried(t *testing.T) {
	dir := fakeClaudeCLI(t, `{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login (token sk-ant-oat-secret)","api_error_status":null}`)
	c := ConfigFrom("claude-code", "sonnet", "", "sk-ant-oat-secret", "")
	p, err := c.NewProvider() // default retries: a 401 must still be sent once
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = p.Complete(context.Background(), toolRequest())
	if err == nil || !strings.Contains(err.Error(), "status 401") || !strings.Contains(err.Error(), "Not logged in") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "sk-ant-oat-secret") {
		t.Fatalf("token leaked into the error: %v", err)
	}
	if calls := strings.Count(readFile(t, filepath.Join(dir, "calls.txt")), "x"); calls != 1 {
		t.Fatalf("CLI ran %d times, want 1", calls)
	}
}

func TestClaudeCodeMissingBinary(t *testing.T) {
	t.Setenv(envClaudeCodeBin, filepath.Join(t.TempDir(), "no-such-claude"))
	_, _, _, err := claudeCodeProvider(t, "").Complete(context.Background(), toolRequest())
	if err == nil || !strings.Contains(err.Error(), "cannot run") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseClaudeCodeReply(t *testing.T) {
	for _, in := range []string{
		`{"text":"a","tool_calls":[]}`,
		"```json\n{\"text\":\"a\",\"tool_calls\":[]}\n```",
		`Sure. {"text":"a","tool_calls":[]} done`,
	} {
		r, ok := parseClaudeCodeReply(in)
		if !ok || r.Text != "a" {
			t.Errorf("parse(%q) = %+v %v", in, r, ok)
		}
	}
	if _, ok := parseClaudeCodeReply(`{"unrelated":1}`); ok {
		t.Error("an object without text/tool_calls must not count as a reply")
	}
	if got := string(toolInput(json.RawMessage(`[1,2]`))); got != "{}" {
		t.Errorf("non-object input = %s, want {}", got)
	}
}
