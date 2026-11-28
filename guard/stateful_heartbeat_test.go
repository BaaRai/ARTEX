package guard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/hook"
)

func TestGuardPrePostHooksRefreshStatefulLeaseHeartbeat(t *testing.T) {
	t.Setenv(intercept.StatefulLeaseTTLEnv, "300")
	now := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)
	sg := intercept.NewStatefulTargetGuard()
	sg.SetClock(func() time.Time { return now })
	sg.RecordBaseline("task-g", "api.lab.local")
	if ok, _ := sg.AcquireMutationLease("task-g", "api.lab.local", 10); !ok {
		t.Fatal("initial lease acquire failed")
	}

	g := New()
	ctx := intercept.WithStatefulActor(intercept.WithStatefulGuard(context.Background(), sg), "task-g", 10)

	// After 250s (<300s), preToolUse refreshes lastBeat even for a read-only call.
	now = now.Add(250 * time.Second)
	res := g.preToolUse(ctx, hook.Event{ToolName: "WebFetch", Input: json.RawMessage(`{"url":"https://api.lab.local/health"}`)})
	if res.Decision == "block" {
		t.Fatalf("unexpected block on read-only call: %v", res)
	}
	sts := sg.LeaseStatuses("task-g", "api.lab.local")
	if len(sts) != 1 || sts[0].LastHeartbeatAgeSeconds != 0 || sts[0].AgeSeconds != 250 {
		t.Fatalf("expected preToolUse heartbeat reset (beat=0, age=250), got %+v", sts)
	}

	// After 200s more (total held 450s > 300s), even an errored postToolUse resets lastBeat.
	now = now.Add(200 * time.Second)
	g.postToolUse(ctx, hook.Event{ToolName: "Bash", IsError: true, Input: json.RawMessage(`{"command":"sleep 1"}`)})
	sts = sg.LeaseStatuses("task-g", "api.lab.local")
	if len(sts) != 1 || sts[0].LastHeartbeatAgeSeconds != 0 || sts[0].AgeSeconds != 450 || sts[0].Stale {
		t.Fatalf("expected postToolUse(err) heartbeat reset (beat=0, age=450, stale=false), got %+v", sts)
	}
}
