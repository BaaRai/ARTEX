package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// TestCLIProvidersLive drives the real CLIs logged in on this host. It spends
// subscription usage, so it runs only with ARTEX_LIVE_CLI=1. Each format must
// pass the connection test and complete a run that calls a real tool and
// answers with its result.
func TestCLIProvidersLive(t *testing.T) {
	if os.Getenv("ARTEX_LIVE_CLI") != "1" {
		t.Skip("set ARTEX_LIVE_CLI=1 to run against the installed, logged-in CLIs")
	}
	for _, f := range []llm.Format{FormatClaudeCLI, FormatCodexCLI, FormatGrokCLI} {
		t.Run(string(f), func(t *testing.T) {
			cfg := ConfigFrom(string(f), os.Getenv("ARTEX_LIVE_CLI_MODEL_"+strings.ToUpper(strings.ReplaceAll(string(f), "-", "_"))), "", "", "")
			lat, reply, err := TestConnection(context.Background(), cfg)
			if err != nil {
				t.Fatalf("TestConnection: %v", err)
			}
			t.Logf("TestConnection: %v reply=%q", lat.Round(time.Millisecond), reply)

			var calls atomic.Int32
			var gotArgs atomic.Value
			add := tool.Build(tool.Spec{
				Name:        "add",
				Description: "Adds two numbers and returns their sum.",
				Schema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}},
					"required":   []any{"a", "b"},
				},
				ReadOnly:    func(json.RawMessage) bool { return true },
				Concurrent:  func(json.RawMessage) bool { return true },
				Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
				Run: func(_ context.Context, in json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
					calls.Add(1)
					gotArgs.Store(string(in))
					var a struct{ A, B float64 }
					if err := json.Unmarshal(in, &a); err != nil {
						return tool.Result{}, err
					}
					// A sum the model cannot guess proves the answer came from the tool.
					return tool.Text(fmt.Sprintf("%g", a.A+a.B+0.5)), nil
				},
			})
			prov, err := cfg.NewProvider()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			start := time.Now()
			answer, err := agentcore.Run(ctx, agentcore.Options{
				Provider:               prov,
				SystemPrompt:           []string{"You are a calculator. Always use the add tool for addition, then state the number the tool returned, exactly as returned."},
				Tools:                  []tool.CoreTool{add},
				PermissionMode:         acperm.ModeBypass,
				MaxTurns:               4,
				DisableBackgroundTasks: true,
			}, "What is 2024 plus 1?")
			if err != nil {
				t.Fatalf("agentcore.Run: %v", err)
			}
			t.Logf("Run: %v add calls=%d args=%v answer=%q", time.Since(start).Round(time.Millisecond), calls.Load(), gotArgs.Load(), answer)
			if calls.Load() == 0 {
				t.Fatal("the add tool was never invoked")
			}
			if !strings.Contains(answer, "2025.5") {
				t.Fatalf("answer %q does not use the tool's result 2025.5", answer)
			}
		})
	}
}
