package agent

import (
	"context"
	"encoding/json"
	"os"
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
		"CLAUDE_CODE_EFFORT_LEVEL=max",
		"ARTEX_TEST=kept",
	})
	if len(got) != 2 || got[0] != "PATH=C:\\bin" || got[1] != "ARTEX_TEST=kept" {
		t.Fatalf("unexpected child env: %v", got)
	}
}

func TestClaudeCodeEffortFromConfig(t *testing.T) {
	t.Setenv("ARTEX_CLAUDE_CODE_BIN", os.Args[0])
	for _, tc := range []struct {
		effort string
		want   bool
	}{
		{"", false},
		{"low", true},
		{"medium", true},
		{"high", true},
		{"xhigh", true},
		{"max", true},
	} {
		t.Run(tc.effort, func(t *testing.T) {
			provider, err := (Config{Format: FormatClaudeCode, Model: "sonnet", ReasoningEffort: tc.effort}).NewProvider()
			if err != nil {
				t.Fatal(err)
			}
			p, ok := provider.(*claudeCodeProvider)
			if !ok {
				t.Fatalf("provider=%T", provider)
			}
			if p.effort != tc.effort {
				t.Fatalf("effort=%q; want %q", p.effort, tc.effort)
			}
			p.run = func(_ context.Context, args []string, _ string) ([]byte, error) {
				if len(args) > 1 && args[0] == "auth" {
					return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
				}
				found := false
				for i, arg := range args {
					if arg == "--effort" {
						found = true
						if i+1 >= len(args) || args[i+1] != tc.effort {
							t.Errorf("effort args = %v", args)
						}
					}
				}
				if found != tc.want {
					t.Errorf("effort flag present = %v; want %v", found, tc.want)
				}
				return []byte(`{"is_error":false,"structured_output":{"kind":"text","text":"OK"}}`), nil
			}
			if _, _, _, err := p.Complete(t.Context(), llm.CompletionRequest{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClaudeCodeModelAndEffortValidation(t *testing.T) {
	t.Setenv("ARTEX_CLAUDE_CODE_BIN", os.Args[0])
	for _, model := range []string{"sonnet", "opus[1m]", "sonnet[1m]", "claude-sonnet-5-5", "claude-opus-4-8[1m]"} {
		if _, err := NewClaudeCodeProvider(model, "high"); err != nil {
			t.Fatalf("valid model %q rejected: %v", model, err)
		}
	}
	for _, model := range []string{"sonnet[2m]", "opus[bad]", "--model"} {
		if _, err := NewClaudeCodeProvider(model, "high"); err == nil {
			t.Fatalf("invalid model %q accepted", model)
		}
	}
	for _, effort := range []string{"ultracode", "auto", "extreme", "high --tools Bash"} {
		if _, err := NewClaudeCodeProvider("sonnet", effort); err == nil {
			t.Fatalf("invalid effort %q accepted", effort)
		}
	}
}

func TestClaudeCodeListModelsInitializeOnly(t *testing.T) {
	var handshakeArgs []string
	var handshakeInput string
	run := func(_ context.Context, args []string, input string) ([]byte, error) {
		if len(args) > 1 && args[0] == "auth" {
			return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
		}
		handshakeArgs, handshakeInput = args, input
		return []byte("{\"type\":\"system\",\"subtype\":\"init\"}\n" +
			`{"type":"control_response","response":{"subtype":"success","response":{"models":[{"value":"default","resolvedModel":"claude-sonnet-5-5","displayName":"Sonnet 5.5","description":"Sonnet 5.5 · Daily coding"},{"value":"opus","resolvedModel":"claude-opus-4-8[1m]","displayName":"Opus 4.8 (1M context)","description":"Opus 4.8 · Complex tasks"},{"value":"malicious","resolvedModel":"--api-key=secret","displayName":"Unsafe"}]}}}` + "\n"), nil
	}
	models, err := claudeCodeListModels(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "claude-sonnet-5-5" || models[0].Label != "Sonnet 5.5" || models[1].ID != "claude-opus-4-8[1m]" {
		t.Fatalf("models=%+v", models)
	}
	if handshakeInput != "{\"type\":\"control_request\",\"request_id\":\"artex-model-picker\",\"request\":{\"subtype\":\"initialize\"}}\n" {
		t.Fatalf("unexpected handshake input: %q", handshakeInput)
	}
	for _, want := range []string{"-p", "--input-format", "stream-json", "--output-format", "--tools", "--setting-sources", "--strict-mcp-config", "--no-session-persistence"} {
		if !containsString(handshakeArgs, want) {
			t.Errorf("missing %q in %v", want, handshakeArgs)
		}
	}
	if containsString(handshakeArgs, "--json-schema") {
		t.Errorf("model picker must not send a model request: %v", handshakeArgs)
	}
}

func TestClaudeCodeListModelsFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"missing response", `{"type":"system","subtype":"init"}`},
		{"empty models", `{"type":"control_response","response":{"subtype":"success","response":{"models":[]}}}`},
		{"error with models", `{"type":"control_response","response":{"subtype":"error","response":{"models":[{"resolvedModel":"claude-sonnet-5-5"}]}}}`},
		{"invalid JSON", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models, err := claudeCodeListModels(t.Context(), func(_ context.Context, args []string, _ string) ([]byte, error) {
				if len(args) > 1 && args[0] == "auth" {
					return []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), nil
				}
				return []byte(tc.out), nil
			})
			if err == nil || len(models) != 0 {
				t.Fatalf("models=%+v err=%v", models, err)
			}
		})
	}
	if models, err := claudeCodeListModels(t.Context(), func(context.Context, []string, string) ([]byte, error) {
		return []byte(`{"loggedIn":false,"authMethod":"none"}`), nil
	}); err == nil || len(models) != 0 {
		t.Fatalf("logged-out result models=%+v err=%v", models, err)
	}
}

func TestClaudeCodeExitDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
	}{
		{"auth", "auth"},
		{"network", "network"},
		{"rate-limit", "rate-limit"},
		{"model", "model"},
		{"context", "context"},
		{"max-turns", "max-turns"},
		{"auth-errors-array", "auth"},
		{"rate-limit-status-string", "rate-limit"},
		{"policy", "policy"},
		{"policy-403", "policy"},
		{"policy-stderr", "policy"},
		{"policy-prompt", "unknown"},
		{"unknown", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARTEX_CLAUDE_EXIT_TEST", tc.name)
			_, err := claudeCodeCommand(os.Args[0])(t.Context(), []string{"-test.run=^TestClaudeCodeExitHelper$"}, "")
			if err == nil || !strings.Contains(err.Error(), "category: "+tc.category) {
				t.Fatalf("exit diagnostic = %v; want category %q", err, tc.category)
			}
			for _, secret := range []string{"secret_token_123", "/private/secret/path"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("exit diagnostic leaked private CLI output: %v", err)
				}
			}
		})
	}
}

func TestClaudeCodeExitQuotaPreserved(t *testing.T) {
	for _, mode := range []string{"quota-stderr", "quota-stdout"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ARTEX_CLAUDE_EXIT_TEST", mode)
			out, err := claudeCodeCommand(os.Args[0])(t.Context(), []string{"-test.run=^TestClaudeCodeExitHelper$"}, "")
			if !isClaudeSubscriptionLimit(string(out)) {
				t.Fatalf("quota marker missing from CLI result: %s", out)
			}
			if mode == "quota-stderr" && err != nil {
				t.Fatalf("stderr quota should be normalized before returning: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret_token_123") {
				t.Fatalf("quota diagnostic leaked private CLI output: %v", err)
			}
		})
	}
}
func TestClaudeCodeExitHelper(t *testing.T) {
	switch os.Getenv("ARTEX_CLAUDE_EXIT_TEST") {
	case "auth":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"subtype":"error_during_execution","result":"Authentication failed: secret_token_123 /private/secret/path"}`)
	case "network":
		_, _ = os.Stderr.WriteString("ECONNRESET: secret_token_123 /private/secret/path")
	case "rate-limit":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"result":"rate_limit: too many requests; secret_token_123 /private/secret/path"}`)
	case "model":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"error":{"type":"model_not_found","message":"secret_token_123 /private/secret/path"}}`)
	case "context":
		_, _ = os.Stderr.WriteString("maximum context length exceeded: secret_token_123 /private/secret/path")
	case "max-turns":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"subtype":"error_max_turns","result":"secret_token_123 /private/secret/path"}`)
	case "quota-stderr":
		_, _ = os.Stderr.WriteString("You've hit your session limit. secret_token_123")
	case "quota-stdout":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"result":"You've hit your session limit. secret_token_123"}`)
	case "auth-errors-array":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"errors":[{"type":"authentication_failed","message":"secret_token_123 /private/secret/path"}]}`)
	case "rate-limit-status-string":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"api_error_status":"429","result":"secret_token_123 /private/secret/path"}`)
	case "policy":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"result":"content_filter: secret_token_123 /private/secret/path"}`)
	case "policy-403":
		_, _ = os.Stdout.WriteString(`{"is_error":true,"api_error_status":403,"result":"content_filter: secret_token_123 /private/secret/path"}`)
	case "policy-stderr":
		_, _ = os.Stderr.WriteString("content_filter: secret_token_123 /private/secret/path")
	case "policy-prompt":
		_, _ = os.Stdout.WriteString(`{"is_error":false,"result":"The user asked about a policy violation. secret_token_123 /private/secret/path"}`)
	case "unknown":
		_, _ = os.Stdout.WriteString("secret_token_123 /private/secret/path")
		_, _ = os.Stderr.WriteString("secret_token_123 /private/secret/path")
	default:
		return
	}
	os.Exit(1)
}
func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
