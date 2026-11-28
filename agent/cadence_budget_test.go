package agent

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlannerTimeBudgetRenderingAndFocusThreshold(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name      string
		tc        TaskClock
		wantSubs  []string
		forbidSub string
	}{
		{
			name:      "no_deadline_empty",
			tc:        TaskClock{},
			forbidSub: "remaining_seconds",
		},
		{
			name: "above_25_pct_no_focus_mode",
			tc: TaskClock{
				DeadlineUnix:  now.Add(500 * time.Second).Unix(),
				BudgetSeconds: 1000,
			},
			wantSubs:  []string{"remaining_seconds=500", "budget_seconds=1000"},
			forbidSub: "below 25%",
		},
		{
			name: "below_25_pct_triggers_focus_mode",
			tc: TaskClock{
				DeadlineUnix:  now.Add(200 * time.Second).Unix(),
				BudgetSeconds: 1000,
			},
			wantSubs: []string{"remaining_seconds=200", "below 25%", "focused", "NOT broad"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := plannerTimeBudgetBlock(tc.tc, now)
			out := plannerRoundInput("lead", "situational"+block, "", "goal", "")
			for _, sub := range tc.wantSubs {
				if !strings.Contains(out, sub) {
					t.Fatalf("expected %q in plannerRoundInput output %q", sub, out)
				}
			}
			if tc.forbidSub != "" && strings.Contains(out, tc.forbidSub) {
				t.Fatalf("did not expect %q in %q", tc.forbidSub, out)
			}
		})
	}
}

func TestSeedIntentBudgetAndFirstWriteNotify(t *testing.T) {
	if got := SeedIntentBudget(1000, 1200*time.Second); got != 200*time.Second {
		t.Fatalf("SeedIntentBudget(1000, 1200s) = %v, want 200s", got)
	}
	if got := SeedIntentBudget(1000, 60*time.Second); got != 60*time.Second {
		t.Fatalf("SeedIntentBudget clamped by workerTimeout = %v, want 60s", got)
	}
	if got := SeedIntentBudget(0, 500*time.Second); got != 100*time.Second {
		t.Fatalf("SeedIntentBudget(0, 500s) = %v, want 100s", got)
	}
	if got := SeedIntentBudget(0, 0); got != DefaultSeedIntentBudget {
		t.Fatalf("SeedIntentBudget(0, 0) = %v, want DefaultSeedIntentBudget (%v)", got, DefaultSeedIntentBudget)
	}

	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	store := d.Exploration(mustCreateExploration(t))
	as := d.Assets()
	tsx := NewToolSet(store, "work#1")
	tsx.SetAssetStore(as, as.Companies())
	var woken atomic.Int32
	tsx.SetNotify(func() { woken.Add(1) })
	tsx.SetNotifyFinding(func(int64, string) {})

	// Arbitrary record_fact does NOT wake planner.
	res, err := tsx.recordFact().Call(t.Context(), json.RawMessage(`{"facts":[{"summary":"Seed checked robots.txt","evidence":"curl 404","confidence":"observed","source":"tool_output"}]}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("record_fact failed: %v %s", err, res.Flatten())
	}
	if woken.Load() != 0 {
		t.Fatalf("expected record_fact NOT to wake planner, got woken=%d", woken.Load())
	}

	// insert_assets batch wakes planner once at batch completion.
	res, err = tsx.insertAssets().Call(t.Context(), json.RawMessage(`{"assets":[{"type":"subdomain","domain":"api.lab.local"},{"type":"service","url":"https://api.lab.local/v1"}]}`), nil)
	if err != nil || res.IsError || strings.Contains(res.Flatten(), `"index":0,"error"`) {
		t.Fatalf("insert_assets failed: %v %s", err, res.Flatten())
	}
	if woken.Load() != 1 {
		t.Fatalf("expected insert_assets batch to wake planner once (woken=%d, res=%s)", woken.Load(), res.Flatten())
	}

	// report_finding also wakes planner even when SetNotifyFinding is active.
	res, err = tsx.addFinding().Call(t.Context(), json.RawMessage(`{"summary":"IDOR on upload","detail":"tenant crossover","evidence":"HTTP 200 flag1","risk":"high"}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("report_finding failed: %v %s", err, res.Flatten())
	}
	if woken.Load() != 2 {
		t.Fatalf("expected report_finding to wake planner immediately (woken=%d)", woken.Load())
	}
}
