package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/intercept"
)

func TestLeaseStatusAndWaitForLeaseTableDriven(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		hostArg   string
		setup     func(g *intercept.StatefulTargetGuard, advance func(time.Duration))
		wantCount int
		wantCheck func(t *testing.T, ts *ToolSet, ctx context.Context, resp map[string]any, overview map[string]any)
	}{
		{
			name:      "empty_leases_returns_empty_slice",
			wantCount: 0,
			wantCheck: func(t *testing.T, _ *ToolSet, _ context.Context, resp map[string]any, overview map[string]any) {
				if enabled, _ := resp["enabled"].(bool); !enabled {
					t.Fatalf("expected enabled=true, got %v", resp["enabled"])
				}
				if _, ok := overview["mutation_leases"]; ok {
					t.Fatalf("did not expect mutation_leases in overview when empty")
				}
			},
		},
		{
			name: "active_and_stale_leases_reported_in_tool_and_overview",
			env:  map[string]string{intercept.StatefulLeaseTTLEnv: "100"},
			setup: func(g *intercept.StatefulTargetGuard, advance func(time.Duration)) {
				g.AcquireMutationLease("42", "api.lab.local", 1688)
				advance(80 * time.Second)
				g.AcquireMutationLease("42", "auth.lab.local", 1690)
				advance(30 * time.Second)
			},
			wantCount: 2,
			wantCheck: func(t *testing.T, _ *ToolSet, _ context.Context, resp map[string]any, overview map[string]any) {
				leases, _ := resp["leases"].([]any)
				first, _ := leases[0].(map[string]any)
				second, _ := leases[1].(map[string]any)
				if first["host"] != "api.lab.local" || first["holder_intent_id"] != float64(1688) || first["stale"] != true {
					t.Fatalf("unexpected first lease: %+v", first)
				}
				if second["host"] != "auth.lab.local" || second["holder_intent_id"] != float64(1690) || second["stale"] != false {
					t.Fatalf("unexpected second lease: %+v", second)
				}
				if _, ok := overview["mutation_leases"]; !ok {
					t.Fatalf("expected mutation_leases section in graph_overview")
				}
			},
		},
		{
			name: "wait_for_lease_succeeds_after_holder_release",
			setup: func(g *intercept.StatefulTargetGuard, _ func(time.Duration)) {
				g.AcquireMutationLease("42", "api.lab.local", 1688)
				go func() {
					time.Sleep(30 * time.Millisecond)
					g.ReleaseIntent("42", 1688)
				}()
			},
			wantCount: 1,
			wantCheck: func(t *testing.T, ts *ToolSet, ctx context.Context, _ map[string]any, _ map[string]any) {
				res, err := ts.waitForLease().Call(ctx, json.RawMessage(`{"host":"api.lab.local","max_wait_s":2}`), nil)
				if err != nil || res.IsError {
					t.Fatalf("wait_for_lease failed: err=%v res=%s", err, res.Flatten())
				}
				var w struct {
					Acquired       bool   `json:"acquired"`
					Host           string `json:"host"`
					HolderIntentID int64  `json:"holder_intent_id"`
				}
				if err := json.Unmarshal([]byte(res.Flatten()), &w); err != nil {
					t.Fatal(err)
				}
				if !w.Acquired || w.Host != "api.lab.local" || w.HolderIntentID != 1689 {
					t.Fatalf("expected wait_for_lease acquired by 1689, got %+v", w)
				}
			},
		},
		{
			name: "flag_off_hides_graph_overview_and_bypass_wait",
			env:  map[string]string{intercept.StatefulGuardEnv: "OFF"},
			setup: func(g *intercept.StatefulTargetGuard, _ func(time.Duration)) {
				g.AcquireMutationLease("42", "api.lab.local", 1688)
			},
			wantCount: 0,
			wantCheck: func(t *testing.T, ts *ToolSet, ctx context.Context, resp map[string]any, overview map[string]any) {
				if enabled, ok := resp["enabled"].(bool); !ok || enabled {
					t.Fatalf("expected enabled=false, got %v", resp["enabled"])
				}
				if _, ok := overview["mutation_leases"]; ok {
					t.Fatalf("expected no mutation_leases in overview when guard off")
				}
				res, err := ts.waitForLease().Call(ctx, json.RawMessage(`{"host":"api.lab.local"}`), nil)
				if err != nil || res.IsError {
					t.Fatalf("wait_for_lease when off failed: %v %s", err, res.Flatten())
				}
				if !strings.Contains(res.Flatten(), `"acquired":true`) {
					t.Fatalf("expected immediate acquired=true when guard off, got %s", res.Flatten())
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			now := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)
			g := intercept.NewStatefulTargetGuard()
			if !strings.Contains(tc.name, "wait_for_lease") {
				g.SetClock(func() time.Time { return now })
			}
			if tc.setup != nil {
				tc.setup(g, func(d time.Duration) { now = now.Add(d) })
			}

			ts := NewToolSet(nil, "worker#1")
			ts.SetTaskID(42)
			ts.SetStatefulGuard(g)

			ctx := intercept.WithStatefulActor(intercept.WithStatefulGuard(context.Background(), g), "42", 1689)
			arg := tc.hostArg
			if arg == "" {
				arg = `{}`
			}
			res, err := ts.leaseStatus().Call(ctx, json.RawMessage(arg), nil)
			if err != nil || res.IsError {
				t.Fatalf("lease_status call failed: %v %s", err, res.Flatten())
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(res.Flatten()), &parsed); err != nil {
				t.Fatalf("unmarshal lease_status result: %v (%q)", err, res.Flatten())
			}
			leases, _ := parsed["leases"].([]any)
			if len(leases) != tc.wantCount {
				t.Fatalf("expected %d leases, got %d (%v)", tc.wantCount, len(leases), parsed)
			}

			ov := map[string]any{}
			if ls := ts.statefulLeaseOverview(); len(ls) > 0 {
				ov["mutation_leases"] = ls
			}
			if tc.wantCheck != nil {
				tc.wantCheck(t, ts, ctx, parsed, ov)
			}
		})
	}
}

func TestLeaseToolsSeededForPlannerAndWorkerAndPolicies(t *testing.T) {
	ts := NewToolSet(nil, "")
	for _, toolName := range []string{"lease_status", "wait_for_lease"} {
		if !toolNameSet(ts.PlannerTools())[toolName] {
			t.Fatalf("PlannerTools missing %s", toolName)
		}
		if !toolNameSet(ts.WorkerTools())[toolName] {
			t.Fatalf("WorkerTools missing %s", toolName)
		}
		var found bool
		for _, sd := range BuiltinToolSeeds() {
			if sd.Key == toolName {
				found = true
				if !slices.Contains(sd.Agents, "planner") || !slices.Contains(sd.Agents, "worker") {
					t.Fatalf("expected %s seeded to planner+worker, got %v", toolName, sd.Agents)
				}
			}
		}
		if !found {
			t.Fatalf("BuiltinToolSeeds missing %s", toolName)
		}
	}

	roundText := plannerRoundInput("lead", "sit", "", "goal", "")
	for _, want := range []string{"lease_status", "mutation_leases", "kill_work", "stale"} {
		if !strings.Contains(roundText, want) {
			t.Fatalf("plannerRoundInput missing %q", want)
		}
	}
	for _, want := range []string{"lease_status", "mutation_leases", "kill_work", "stale"} {
		if !strings.Contains(plannerDefaultTmpl, want) {
			t.Fatalf("plannerDefaultTmpl missing %q", want)
		}
	}
	if !strings.Contains(workerDefaultTmpl, "wait_for_lease") {
		t.Fatalf("workerDefaultTmpl should mention wait_for_lease")
	}
}
