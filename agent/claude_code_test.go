package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestClaudeCodeToolRoundTrip(t *testing.T) {
	var inferenceArgs []string
	var inferenceInput string
	p := &claudeCodeProvider{model: "sonnet", run: func(_ context.Context, args []string, input string) ([]byte, error) {
		if len(args) > 1 && args[0] == "auth" {
			return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
		}
		inferenceArgs, inferenceInput = args, input
		return []byte(`{"is_error":false,"structured_output":{"kind":"tool","tool_name":"read_file","tool_input":{"path":"README.md"}},"usage":{"input_tokens":15,"output_tokens":7}}`), nil
	}}
	req := llm.CompletionRequest{
		System: []string{"You are a file reviewer."},
		Messages: []llm.Message{
			llm.UserText("Read the readme"),
		},
		Tools: []llm.ToolSchema{{Name: "read_file", Description: "Read a file", InputSchema: map[string]any{"type": "object"}}},
	}
	msg, stop, usage, err := p.Complete(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if stop != "tool_use" || usage.InputTokens != 15 || usage.OutputTokens != 7 {
		t.Fatalf("stop=%q usage=%+v", stop, usage)
	}
	if uses := msg.ToolUses(); len(uses) != 1 || uses[0].Name != "read_file" || string(uses[0].Input) != `{"path":"README.md"}` || uses[0].ID == "" {
		t.Fatalf("tool uses = %+v", uses)
	}
	for _, want := range []string{"--restricted", "--tools", "--strict-mcp-config", "--disallowedTools", "--no-session-persistence", "--max-turns", "--json-schema", "--system-prompt-file"} {
		if !containsString(inferenceArgs, want) {
			t.Errorf("CLI args missing %q: %v", want, inferenceArgs)
		}
	}
	if len(inferenceArgs) == 0 || !strings.Contains(inferenceArgs[len(inferenceArgs)-1], "stdin") {
		t.Fatalf("CLI invocation must include an explicit stdin prompt: %v", inferenceArgs)
	}
	var payload struct {
		Messages []llm.Message    `json:"messages"`
		Tools    []llm.ToolSchema `json:"tools"`
	}
	if err := json.Unmarshal([]byte(inferenceInput), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 1 || payload.Messages[0].Text() != "Read the readme" || len(payload.Tools) != 1 {
		t.Fatalf("input not preserved: %s", inferenceInput)
	}
}

func TestClaudeCodeStatusRequiresSubscriptionLogin(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   bool
	}{
		{"claude.ai", true},
		{"api_key", false},
		{"third_party", false},
		{"none", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			got, err := claudeCodeLoginStatus(t.Context(), func(context.Context, []string, string) ([]byte, error) {
				return []byte(`{"loggedIn":true,"authMethod":"` + tc.method + `"}`), nil
			})
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestClaudeCodeUsageLimitMarker(t *testing.T) {
	p := &claudeCodeProvider{model: "sonnet", run: func(_ context.Context, args []string, _ string) ([]byte, error) {
		if len(args) > 1 && args[0] == "auth" {
			return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
		}
		return []byte(`{"is_error":true,"result":"You've hit your session limit. It resets in 2 hours."}`), nil
	}}
	_, _, _, err := p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}})
	if err == nil || !strings.Contains(err.Error(), "claude_subscription_usage_limit_exceeded") {
		t.Fatalf("expected subscription limit marker, got %v", err)
	}
	if isClaudeSubscriptionLimit("rate limit: too many requests") {
		t.Fatal("ordinary rate limit misclassified as plan exhaustion")
	}
	if !isClaudeSubscriptionLimit("Third-party apps now draw from your extra usage") {
		t.Fatal("Claude extra-usage billing stop was not recognized")
	}
	if !isClaudeSubscriptionLimit("You're out of extra usage") {
		t.Fatal("Claude extra-usage exhaustion was not recognized")
	}
}

func TestClaudeCodeSuccessfulAnswerCanMentionUsageLimit(t *testing.T) {
	p := &claudeCodeProvider{model: "sonnet", run: func(_ context.Context, args []string, _ string) ([]byte, error) {
		if len(args) > 1 && args[0] == "auth" {
			return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
		}
		return []byte(`{"is_error":false,"structured_output":{"kind":"text","text":"The message says usage limit reached."}}`), nil
	}}
	msg, stop, _, err := p.Complete(t.Context(), llm.CompletionRequest{})
	if err != nil || stop != "end_turn" || msg.Text() != "The message says usage limit reached." {
		t.Fatalf("msg=%+v stop=%q err=%v", msg, stop, err)
	}
	a := llm.NewAccumulator()
	for event, err := range p.Stream(t.Context(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		a.Add(event)
	}
	if a.StopReason != "end_turn" || a.Message().Text() != msg.Text() {
		t.Fatalf("stream result stop=%q message=%+v", a.StopReason, a.Message())
	}
}

func TestClaudeCodeStreamReassemblesToolUse(t *testing.T) {
	p := &claudeCodeProvider{model: "sonnet", run: func(_ context.Context, args []string, _ string) ([]byte, error) {
		if len(args) > 1 && args[0] == "auth" {
			return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
		}
		return []byte(`{"is_error":false,"structured_output":{"kind":"tool","tool_name":"read_file","tool_input":{"path":"README.md"}},"usage":{"input_tokens":15,"output_tokens":7}}`), nil
	}}
	a := llm.NewAccumulator()
	for event, err := range p.Stream(t.Context(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "read_file"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		a.Add(event)
	}
	msg := a.Message()
	if a.StopReason != "tool_use" || a.Usage.InputTokens != 15 || len(msg.ToolUses()) != 1 || msg.ToolUses()[0].Name != "read_file" {
		t.Fatalf("stop=%q usage=%+v message=%+v", a.StopReason, a.Usage, msg)
	}
}

func TestClaudeCodeRejectsUnknownTool(t *testing.T) {
	p := &claudeCodeProvider{model: "sonnet", run: func(_ context.Context, args []string, _ string) ([]byte, error) {
		if len(args) > 1 && args[0] == "auth" {
			return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
		}
		return []byte(`{"is_error":false,"structured_output":{"kind":"tool","tool_name":"made_up","tool_input":{}}}`), nil
	}}
	_, _, _, err := p.Complete(t.Context(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "read_file"}}})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestClaudeCodeScrubsAuthenticationOverrides(t *testing.T) {
	got := claudeCodeEnv([]string{
		"PATH=C:\\bin", "ANTHROPIC_API_KEY=secret", "claude_code_simple=1",
		"ANTHROPIC_BASE_URL=https://gateway.example", "CLAUDE_CODE_USE_BEDROCK=1",
		"ARTEX_TEST=kept",
	})
	if len(got) != 2 || got[0] != "PATH=C:\\bin" || got[1] != "ARTEX_TEST=kept" {
		t.Fatalf("unexpected child env: %v", got)
	}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
