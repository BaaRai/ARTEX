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

func TestLeaseStatusToolAndGraphOverviewTableDriven(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		hostArg   string
		setup     func(g *intercept.StatefulTargetGuard, advance func(time.Duration))
		wantCount int
		wantCheck func(t *testing.T, resp map[string]any, overview map[string]any)
	}{
		{
			name:      "empty_leases_returns_empty_slice",
			wantCount: 0,
			wantCheck: func(t *testing.T, resp map[string]any, overview map[string]any) {
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
				advance(30 * time.Second) // api.lab.local held 110s (>=100 stale); auth held 30s (fresh)
			},
			wantCount: 2,
			wantCheck: func(t *testing.T, resp map[string]any, overview map[string]any) {
				leases, _ := resp["leases"].([]any)
				first, _ := leases[0].(map[string]any)
				second, _ := leases[1].(map[string]any)
				if first["host"] != "api.lab.local" || first["holder_intent_id"] != float64(1688) || first["stale"] != true {
					t.Fatalf("unexpected first lease: %+v", first)
				}
				if first["age_seconds"] != float64(110) || first["last_heartbeat_age_seconds"] != float64(110) {
					t.Fatalf("expected age/beat 110s on first lease, got %+v", first)
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
			name:    "host_filter_selects_one_host",
			hostArg: `{"host":"auth.lab.local"}`,
			setup: func(g *intercept.StatefulTargetGuard, advance func(time.Duration)) {
				g.AcquireMutationLease("42", "api.lab.local", 1688)
				g.AcquireMutationLease("42", "auth.lab.local", 1690)
				advance(10 * time.Second)
			},
			wantCount: 1,
			wantCheck: func(t *testing.T, resp map[string]any, _ map[string]any) {
				leases, _ := resp["leases"].([]any)
				only, _ := leases[0].(map[string]any)
				if only["host"] != "auth.lab.local" || only["holder_intent_id"] != float64(1690) {
					t.Fatalf("unexpected filtered lease: %+v", only)
				}
			},
		},
		{
			name: "flag_off_reports_enabled_false_and_hides_overview_leases",
			env:  map[string]string{intercept.StatefulGuardEnv: "off"},
			setup: func(g *intercept.StatefulTargetGuard, _ func(time.Duration)) {
				g.AcquireMutationLease("42", "api.lab.local", 1688)
			},
			wantCount: 0,
			wantCheck: func(t *testing.T, resp map[string]any, overview map[string]any) {
				if enabled, ok := resp["enabled"].(bool); !ok || enabled {
					t.Fatalf("expected enabled=false, got %v", resp["enabled"])
				}
				if _, ok := overview["mutation_leases"]; ok {
					t.Fatalf("expected no mutation_leases in overview when guard is off")
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
			g.SetClock(func() time.Time { return now })
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
			if err != nil {
				t.Fatalf("lease_status call failed: %v", err)
			}
			if res.IsError {
				t.Fatalf("lease_status returned tool error: %s", res.Flatten())
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
			if leases := ts.statefulLeaseOverview(); len(leases) > 0 {
				ov["mutation_leases"] = leases
			}
			if tc.wantCheck != nil {
				tc.wantCheck(t, parsed, ov)
			}
		})
	}
}

func TestLeaseStatusSeededForPlannerAndWorkerAndPlannerPolicy(t *testing.T) {
	ts := NewToolSet(nil, "")
	if !toolNameSet(ts.PlannerTools())["lease_status"] {
		t.Fatalf("PlannerTools missing lease_status")
	}
	if !toolNameSet(ts.WorkerTools())["lease_status"] {
		t.Fatalf("WorkerTools missing lease_status")
	}

	var found bool
	for _, sd := range BuiltinToolSeeds() {
		if sd.Key == "lease_status" {
			found = true
			if !slices.Contains(sd.Agents, "planner") || !slices.Contains(sd.Agents, "worker") {
				t.Fatalf("expected lease_status seeded to planner+worker, got %v", sd.Agents)
			}
		}
	}
	if !found {
		t.Fatalf("BuiltinToolSeeds missing lease_status")
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
}
