package intercept

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStatefulTargetGuardTableDriven(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(g *StatefulTargetGuard)
		intentID     int64
		tool         string
		input        string
		rendered     string
		method       string
		wantErrSub   string
		recordRO     bool
		checkAfterRO string
	}{
		{
			name:       "post_blocked_without_baseline",
			intentID:   1,
			tool:       "Bash",
			input:      `{"command":"curl -X POST https://target.lab.local/api/v1/certificates/upload -d '{}'"}`,
			wantErrSub: "pre-mutation baseline required",
		},
		{
			name:     "get_runs_without_baseline_and_records_it",
			intentID: 1,
			tool:     "WebFetch",
			input:    `{"url":"https://target.lab.local/api/v1/estate"}`,
			recordRO: true,
		},
		{
			name: "post_allowed_after_baseline",
			setup: func(g *StatefulTargetGuard) {
				g.RecordBaseline("task-1", "target.lab.local")
			},
			intentID: 1,
			tool:     "Bash",
			input:    `{"command":"curl -X POST https://target.lab.local/api/v1/certificates/upload -d '{}'"}`,
		},
		{
			name: "concurrent_mutating_intent_rejected_while_lease_held",
			setup: func(g *StatefulTargetGuard) {
				g.RecordBaseline("task-1", "target.lab.local")
				ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 1)
				if !ok {
					t.Fatal("setup lease failed")
				}
			},
			intentID:   2,
			tool:       "custom_http_post",
			rendered:   "https://target.lab.local/api/v1/deploy",
			method:     "POST",
			wantErrSub: "mutation lease for host target.lab.local is held by intent #1",
		},
		{
			name: "readonly_runs_in_parallel_while_mutation_lease_held",
			setup: func(g *StatefulTargetGuard) {
				g.RecordBaseline("task-1", "target.lab.local")
				g.AcquireMutationLease("task-1", "target.lab.local", 1)
			},
			intentID: 2,
			tool:     "WebFetch",
			input:    `{"url":"https://target.lab.local/docs"}`,
		},
		{
			name:       "custom_script_tool_post_blocked_without_baseline",
			intentID:   3,
			tool:       "custom_py_script",
			input:      `{"url":"https://script.lab.local/api/v1/upload"}`,
			rendered:   `import requests; requests.post(params["url"], json={})`,
			wantErrSub: "pre-mutation baseline required for host script.lab.local",
		},
		{
			name: "custom_script_tool_post_allowed_after_baseline",
			setup: func(g *StatefulTargetGuard) {
				g.RecordBaseline("task-1", "script.lab.local")
			},
			intentID: 3,
			tool:     "custom_py_script",
			input:    `{"url":"https://script.lab.local/api/v1/upload"}`,
			rendered: `import requests; requests.post(params["url"], json={})`,
		},
		{
			name: "release_intent_frees_lease_for_next_intent",
			setup: func(g *StatefulTargetGuard) {
				g.RecordBaseline("task-1", "target.lab.local")
				g.AcquireMutationLease("task-1", "target.lab.local", 1)
				g.ReleaseIntent("task-1", 1)
			},
			intentID: 2,
			tool:     "custom_http_post",
			rendered: "https://target.lab.local/api/v1/deploy",
			method:   "POST",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewStatefulTargetGuard()
			if tc.setup != nil {
				tc.setup(g)
			}
			ctx := WithStatefulActor(WithStatefulGuard(context.Background(), g), "task-1", tc.intentID)
			err := CheckStatefulTool(ctx, tc.tool, json.RawMessage(tc.input), tc.rendered, tc.method)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.recordRO {
				RecordStatefulReadOnly(ctx, tc.tool, json.RawMessage(tc.input), tc.rendered, tc.method)
				if !g.HasBaseline("task-1", "target.lab.local") {
					t.Fatalf("expected baseline recorded after read-only call")
				}
			}
		})
	}
}
