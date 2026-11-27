package intercept

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestStatefulTargetGuardTableDriven(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		setup      func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration))
		intentID   int64
		tool       string
		input      string
		rendered   string
		method     string
		wantErrAll []string
		recordRO   bool
		verify     func(t *testing.T, g *StatefulTargetGuard)
	}{
		{
			name:       "post_blocked_without_baseline",
			intentID:   1,
			tool:       "Bash",
			input:      `{"command":"curl -X POST https://target.lab.local/api/v1/certificates/upload -d '{}'"}`,
			wantErrAll: []string{"pre-mutation baseline required"},
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
			setup: func(_ *testing.T, g *StatefulTargetGuard, _ func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
			},
			intentID: 1,
			tool:     "Bash",
			input:    `{"command":"curl -X POST https://target.lab.local/api/v1/certificates/upload -d '{}'"}`,
		},
		{
			name: "concurrent_mutating_intent_rejected_with_enriched_conflict_error",
			setup: func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 1)
				if !ok {
					t.Fatal("setup lease failed")
				}
				advance(125 * time.Second)
				g.Heartbeat("task-1", 1)
				advance(45 * time.Second)
			},
			intentID: 2,
			tool:     "custom_http_post",
			rendered: "https://target.lab.local/api/v1/deploy",
			method:   "POST",
			wantErrAll: []string{
				"target.lab.local",
				"intent #1",
				"held 170s",
				"last heartbeat 45s ago",
				"do read-only work",
				"wait",
				"ask the planner to kill intent #1 if it is stale",
			},
		},
		{
			name: "readonly_runs_in_parallel_while_mutation_lease_held",
			setup: func(_ *testing.T, g *StatefulTargetGuard, _ func(time.Duration)) {
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
			wantErrAll: []string{"pre-mutation baseline required for host script.lab.local"},
		},
		{
			name: "custom_script_tool_post_allowed_after_baseline",
			setup: func(_ *testing.T, g *StatefulTargetGuard, _ func(time.Duration)) {
				g.RecordBaseline("task-1", "script.lab.local")
			},
			intentID: 3,
			tool:     "custom_py_script",
			input:    `{"url":"https://script.lab.local/api/v1/upload"}`,
			rendered: `import requests; requests.post(params["url"], json={})`,
		},
		{
			name: "release_intent_frees_lease_for_next_intent",
			setup: func(_ *testing.T, g *StatefulTargetGuard, _ func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				g.AcquireMutationLease("task-1", "target.lab.local", 1)
				g.ReleaseIntent("task-1", 1)
			},
			intentID: 2,
			tool:     "custom_http_post",
			rendered: "https://target.lab.local/api/v1/deploy",
			method:   "POST",
		},
		{
			name: "heartbeat_refresh_prevents_steal_past_total_ttl",
			env:  map[string]string{StatefulLeaseTTLEnv: "600"},
			setup: func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				if ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 1); !ok {
					t.Fatal("setup lease failed")
				}
				// Legitimate 400s script completes -> heartbeat refreshes at 400s.
				advance(400 * time.Second)
				g.Heartbeat("task-1", 1)
				// Another 400s script completes -> total held 800s (>600s), lastBeat fresh.
				advance(400 * time.Second)
				g.Heartbeat("task-1", 1)
				advance(100 * time.Second) // lastBeat only 100s old (<600s)
			},
			intentID: 2,
			tool:     "Bash",
			input:    `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			wantErrAll: []string{
				"intent #1",
				"held 900s",
				"last heartbeat 100s ago",
				"ttl 600s",
			},
		},
		{
			name: "stale_lease_takeover_after_ttl_logs_warn",
			env:  map[string]string{StatefulLeaseTTLEnv: "300"},
			setup: func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				if ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 77); !ok {
					t.Fatal("setup lease failed")
				}
				advance(305 * time.Second) // no heartbeat for >300s -> stale
			},
			intentID: 88,
			tool:     "Bash",
			input:    `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			verify: func(t *testing.T, g *StatefulTargetGuard) {
				sts := g.LeaseStatuses("task-1", "target.lab.local")
				if len(sts) != 1 || sts[0].HolderIntentID != 88 || sts[0].Stale {
					t.Fatalf("expected intent #88 fresh holder after stale takeover, got %+v", sts)
				}
				// Stale old holder's late heartbeat must not steal back or refresh intent #88's beat.
				g.Heartbeat("task-1", 77)
				stsAfter := g.LeaseStatuses("task-1", "target.lab.local")
				if stsAfter[0].HolderIntentID != 88 {
					t.Fatalf("late heartbeat altered holder: %+v", stsAfter)
				}
			},
		},
		{
			name:     "flag_off_bypasses_baseline_and_lease",
			env:      map[string]string{StatefulGuardEnv: "off"},
			intentID: 2,
			setup: func(_ *testing.T, g *StatefulTargetGuard, _ func(time.Duration)) {
				g.AcquireMutationLease("task-1", "target.lab.local", 1)
			},
			tool:  "Bash",
			input: `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
		},
		{
			name:     "flag_false_string_also_bypasses",
			env:      map[string]string{StatefulGuardEnv: "FALSE"},
			intentID: 2,
			tool:     "Bash",
			input:    `{"command":"curl -X DELETE https://target.lab.local/api/v1/item/1"}`,
		},
		{
			name:       "flag_on_explicit_still_enforces",
			env:        map[string]string{StatefulGuardEnv: "on"},
			intentID:   2,
			tool:       "Bash",
			input:      `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			wantErrAll: []string{"pre-mutation baseline required"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var logBuf bytes.Buffer
			if strings.Contains(tc.name, "logs_warn") {
				prev := slog.Default()
				slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
				t.Cleanup(func() { slog.SetDefault(prev) })
			}

			now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
			advance := func(d time.Duration) { now = now.Add(d) }
			g := NewStatefulTargetGuard()
			g.SetClock(func() time.Time { return now })
			if tc.setup != nil {
				tc.setup(t, g, advance)
			}

			ctx := WithStatefulActor(WithStatefulGuard(context.Background(), g), "task-1", tc.intentID)
			err := CheckStatefulTool(ctx, tc.tool, json.RawMessage(tc.input), tc.rendered, tc.method)
			if len(tc.wantErrAll) > 0 {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tc.wantErrAll)
				}
				msg := err.Error()
				for _, sub := range tc.wantErrAll {
					if !strings.Contains(msg, sub) {
						t.Fatalf("expected error %q to contain %q", msg, sub)
					}
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
			if strings.Contains(tc.name, "logs_warn") {
				logged := logBuf.String()
				if !strings.Contains(logged, "WARN") || !strings.Contains(logged, "old_holder_intent_id=77") || !strings.Contains(logged, "last_heartbeat_age_s=305") {
					t.Fatalf("expected WARN log naming old holder (77) and age (305), got %q", logged)
				}
			}
			if tc.verify != nil {
				tc.verify(t, g)
			}
		})
	}
}

func TestLeaseStatusesSortingAndStaleFlag(t *testing.T) {
	t.Setenv(StatefulLeaseTTLEnv, "120")
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	g := NewStatefulTargetGuard()
	g.SetClock(func() time.Time { return now })

	g.AcquireMutationLease("task-a", "z.example.test", 10)
	now = now.Add(90 * time.Second)
	g.AcquireMutationLease("task-a", "a.example.test", 20)
	now = now.Add(40 * time.Second) // z is 130s old (>=120 stale); a is 40s old (fresh)

	all := g.LeaseStatuses("task-a", "")
	if len(all) != 2 {
		t.Fatalf("expected 2 leases, got %d", len(all))
	}
	if all[0].Host != "a.example.test" || all[0].HolderIntentID != 20 || all[0].Stale || all[0].AgeSeconds != 40 || all[0].LastHeartbeatAgeSeconds != 40 {
		t.Fatalf("unexpected lease[0]: %+v", all[0])
	}
	if all[1].Host != "z.example.test" || all[1].HolderIntentID != 10 || !all[1].Stale || all[1].AgeSeconds != 130 || all[1].LastHeartbeatAgeSeconds != 130 {
		t.Fatalf("unexpected lease[1]: %+v", all[1])
	}

	filtered := g.LeaseStatuses("task-a", "Z.EXAMPLE.TEST.")
	if len(filtered) != 1 || filtered[0].Host != "z.example.test" {
		t.Fatalf("expected filtered z.example.test, got %+v", filtered)
	}
}
