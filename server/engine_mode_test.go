package server

import (
	"encoding/json"
	"testing"
)

func TestEngineModeFor(t *testing.T) {
	const now = int64(10_000)
	cases := []struct {
		name string
		in   engineModeInput
		want string
	}{
		{"not started is idle", engineModeInput{}, "idle"},
		{"paused wins over everything", engineModeInput{Paused: true, Running: true, ActiveCalls: 2, InFlight: 1, LastActivity: now - 999}, "paused"},
		{"model call in progress is exploring", engineModeInput{Running: true, ActiveCalls: 1, InFlight: 1, LastActivity: now}, "exploring"},
		{"running with no work is idle", engineModeInput{Running: true}, "idle"},
		{"running intent, recent activity, no call is idle", engineModeInput{Running: true, InFlight: 1, LastActivity: now - 60}, "idle"},
		{"running intent, quiet over 2 minutes, no call is stalled", engineModeInput{Running: true, InFlight: 1, LastActivity: now - 121}, "stalled"},
		{"no heartbeat yet is not stalled", engineModeInput{Running: true, InFlight: 1}, "idle"},
		{"engine not running is never stalled", engineModeInput{InFlight: 1, LastActivity: now - 999}, "idle"},
	}
	for _, c := range cases {
		if got := engineModeFor(c.in, now); got != c.want {
			t.Errorf("%s: engineModeFor(%+v) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestTaskDTOEngineModeOnlyForLiveTasks(t *testing.T) {
	live := TaskDTO{Status: "running", EngineMode: "idle"}
	b, _ := json.Marshal(live)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["engine_mode"] != "idle" {
		t.Fatalf("running task: engine_mode = %v, want idle", m["engine_mode"])
	}

	done := TaskDTO{Status: "done"}
	b, _ = json.Marshal(done)
	m = nil
	_ = json.Unmarshal(b, &m)
	if _, ok := m["engine_mode"]; ok {
		t.Fatalf("finished task should omit engine_mode, got %v", m["engine_mode"])
	}
}
