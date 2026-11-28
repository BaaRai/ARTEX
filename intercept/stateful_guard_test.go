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
		name         string
		env          map[string]string
		setup        func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration))
		intentID     int64
		tool         string
		input        string
		rendered     string
		method       string
		wantErrAll   []string
		forbidErrSub []string
		recordRO     bool
		verify       func(t *testing.T, g *StatefulTargetGuard, ctx context.Context, advance func(time.Duration))
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
			name: "concurrent_mutating_intent_rejected_with_worker_only_guidance",
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
				"lease_status",
				"wait_for_lease",
				"do read-only work",
				"report blocked",
			},
			forbidErrSub: []string{"kill_work", "ask the planner to kill"},
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
			name: "long_600s_tool_call_safe_under_default_900s_ttl",
			setup: func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				if ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 10); !ok {
					t.Fatal("setup lease failed")
				}
				advance(600 * time.Second) // max Bash timeout = 600s (< 900s default TTL)
			},
			intentID:   11,
			tool:       "Bash",
			input:      `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			wantErrAll: []string{"intent #10", "held 600s", "last heartbeat 600s ago", "ttl 900s"},
		},
		{
			name: "heartbeat_refresh_prevents_steal_past_total_ttl",
			env:  map[string]string{StatefulLeaseTTLEnv: "600"},
			setup: func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				if ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 1); !ok {
					t.Fatal("setup lease failed")
				}
				advance(400 * time.Second)
				g.Heartbeat("task-1", 1)
				advance(400 * time.Second) // total held 800s > 600s
				g.Heartbeat("task-1", 1)
				advance(100 * time.Second) // lastBeat 100s < 600s
			},
			intentID:   2,
			tool:       "Bash",
			input:      `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			wantErrAll: []string{"intent #1", "held 900s", "last heartbeat 100s ago", "ttl 600s"},
		},
		{
			name: "stale_lease_takeover_after_ttl_logs_warn_and_blocks_old_holder",
			env:  map[string]string{StatefulLeaseTTLEnv: "300"},
			setup: func(t *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.RecordBaseline("task-1", "target.lab.local")
				if ok, _ := g.AcquireMutationLease("task-1", "target.lab.local", 77); !ok {
					t.Fatal("setup lease failed")
				}
				advance(305 * time.Second) // tool exceeded TTL
			},
			intentID: 88,
			tool:     "Bash",
			input:    `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			verify: func(t *testing.T, g *StatefulTargetGuard, _ context.Context, _ func(time.Duration)) {
				sts := g.LeaseStatuses("task-1", "target.lab.local")
				if len(sts) != 1 || sts[0].HolderIntentID != 88 || sts[0].Stale {
					t.Fatalf("expected intent #88 fresh holder after stale takeover, got %+v", sts)
				}
				// Old holder's late heartbeat must not steal back or refresh intent #88.
				g.Heartbeat("task-1", 77)
				// Old holder attempting another mutation after takeover must be rejected.
				oldCtx := WithStatefulActor(WithStatefulGuard(context.Background(), g), "task-1", 77)
				err := CheckStatefulTool(oldCtx, "Bash", json.RawMessage(`{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`), "", "")
				if err == nil || !strings.Contains(err.Error(), "intent #88") {
					t.Fatalf("expected old holder #77 blocked by new holder #88, got %v", err)
				}
			},
		},
		{
			name: "guard_off_case_insensitive_bypasses_all_hooks",
			env:  map[string]string{StatefulGuardEnv: "OfF"},
			setup: func(_ *testing.T, g *StatefulTargetGuard, advance func(time.Duration)) {
				g.AcquireMutationLease("task-1", "target.lab.local", 1)
				advance(50 * time.Second)
			},
			intentID: 2,
			tool:     "Bash",
			input:    `{"command":"curl -X POST https://target.lab.local/api/v1/mutate -d '{}'"}`,
			verify: func(t *testing.T, g *StatefulTargetGuard, _ context.Context, _ func(time.Duration)) {
				// RecordStatefulReadOnly must not record baselines when disabled.
				roCtx := WithStatefulActor(WithStatefulGuard(context.Background(), g), "task-1", 2)
				RecordStatefulReadOnly(roCtx, "WebFetch", json.RawMessage(`{"url":"https://ro.lab.local/"}`), "", "")
				if g.HasBaseline("task-1", "ro.lab.local") {
					t.Fatalf("expected RecordStatefulReadOnly bypassed when guard off")
				}
				// TouchStatefulHeartbeat must not update lastBeat when disabled.
				hbCtx := WithStatefulActor(WithStatefulGuard(context.Background(), g), "task-1", 1)
				TouchStatefulHeartbeat(hbCtx)
				sts := g.LeaseStatuses("task-1", "target.lab.local")
				if len(sts) != 1 || sts[0].LastHeartbeatAgeSeconds != 50 {
					t.Fatalf("expected TouchStatefulHeartbeat bypassed when guard off, got %+v", sts)
				}
			},
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
				for _, bad := range tc.forbidErrSub {
					if strings.Contains(msg, bad) {
						t.Fatalf("error %q must not contain %q", msg, bad)
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
				for _, want := range []string{
					"WARN",
					"host=target.lab.local",
					"old_holder_intent_id=77",
					"new_holder_intent_id=88",
					"held_age_s=305",
					"last_heartbeat_age_s=305",
				} {
					if !strings.Contains(logged, want) {
						t.Fatalf("expected WARN log containing %q, got %q", want, logged)
					}
				}
			}
			if tc.verify != nil {
				tc.verify(t, g, ctx, advance)
			}
		})
	}
}

func TestStatefulEnvValidationTableDriven(t *testing.T) {
	ttlCases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "unset_default_900", raw: "", want: 900 * time.Second},
		{name: "valid_300", raw: "300", want: 300 * time.Second},
		{name: "zero_falls_back", raw: "0", want: 900 * time.Second},
		{name: "negative_falls_back", raw: "-42", want: 900 * time.Second},
		{name: "non_numeric_falls_back", raw: "ten-minutes", want: 900 * time.Second},
		{name: "huge_int64_overflow_falls_back", raw: "9223372036854775807", want: 900 * time.Second},
		{name: "huge_string_falls_back", raw: "999999999999999999999999", want: 900 * time.Second},
		{name: "above_24h_falls_back", raw: "100000", want: 900 * time.Second},
	}
	for _, tc := range ttlCases {
		t.Run("ttl/"+tc.name, func(t *testing.T) {
			t.Setenv(StatefulLeaseTTLEnv, tc.raw)
			if got := StatefulLeaseTTL(); got != tc.want {
				t.Fatalf("StatefulLeaseTTL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			if !StatefulGuardEnabled() {
				t.Fatal("invalid TTL must never disable guard")
			}
		})
	}

	flagCases := []struct {
		raw  string
		want bool
	}{
		{"", true},
		{"on", true},
		{"ON", true},
		{"true", true},
		{"off", false},
		{"OFF", false},
		{"False", false},
		{"0", false},
		{"no", false},
		{"NO", false},
	}
	for _, tc := range flagCases {
		t.Run("flag/"+tc.raw, func(t *testing.T) {
			t.Setenv(StatefulGuardEnv, tc.raw)
			if got := StatefulGuardEnabled(); got != tc.want {
				t.Fatalf("StatefulGuardEnabled(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWaitForMutationLeaseTableDriven(t *testing.T) {
	t.Run("wait_success_when_holder_releases", func(t *testing.T) {
		g := NewStatefulTargetGuard()
		g.AcquireMutationLease("task-1", "api.lab.local", 1)
		go func() {
			time.Sleep(25 * time.Millisecond)
			g.ReleaseIntent("task-1", 1)
		}()
		res := g.WaitForMutationLease(context.Background(), "task-1", "api.lab.local", 2, 2*time.Second, 5*time.Millisecond)
		if !res.Acquired || res.HolderIntentID != 2 {
			t.Fatalf("expected wait to acquire for #2, got %+v", res)
		}
	})

	t.Run("wait_timeout_returns_false_with_holder_details", func(t *testing.T) {
		g := NewStatefulTargetGuard()
		g.AcquireMutationLease("task-1", "api.lab.local", 10)
		res := g.WaitForMutationLease(context.Background(), "task-1", "api.lab.local", 11, 40*time.Millisecond, 10*time.Millisecond)
		if res.Acquired || res.HolderIntentID != 10 || res.Stale {
			t.Fatalf("expected wait timeout held by #10, got %+v", res)
		}
	})

	t.Run("wait_takes_over_stale_holder", func(t *testing.T) {
		t.Setenv(StatefulLeaseTTLEnv, "10")
		now := time.Date(2026, 11, 27, 13, 0, 0, 0, time.UTC)
		g := NewStatefulTargetGuard()
		g.SetClock(func() time.Time { return now })
		g.AcquireMutationLease("task-1", "api.lab.local", 10)
		now = now.Add(15 * time.Second)
		res := g.WaitForMutationLease(context.Background(), "task-1", "api.lab.local", 12, time.Second, 5*time.Millisecond)
		if !res.Acquired || res.HolderIntentID != 12 {
			t.Fatalf("expected stale takeover to #12, got %+v", res)
		}
	})
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
