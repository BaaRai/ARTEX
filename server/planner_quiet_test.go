package server

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

// Planner quiet-mode (#79): an edge round is dropped only when BOTH hold — the
// open-goal fingerprint is unchanged since the last completed round AND active
// work exists (open or running intents). Every other combination plans.
func TestPlannerQuietSkipEdge(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if m.pg == nil {
		t.Skip("no postgres for this run")
	}

	created, err := m.CreateTaskWithOptions("quiet-mode probe", "probe goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tt, ok := m.Task(created.ID)
	if !ok {
		t.Fatal("task not found after creation")
	}
	store := tt.Store
	e := NewEngine(m)

	gid, err := store.AddGoal(map[string]any{"summary": "g1"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	iid, err := store.AddIntent(map[string]any{"summary": "i1"}, 0, nil, "seed")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := store.OpenGoalFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" {
		t.Fatal("fingerprint must not be empty with an open goal")
	}
	e.goalFP.Store(tt.ID, fp)
	if !e.quietSkipEdge(tt) {
		t.Fatal("unchanged goals + active intent must skip the edge round")
	}

	// a goal closes -> the fingerprint changes -> plan
	if err := store.SetNodeState(gid, "met"); err != nil {
		t.Fatal(err)
	}
	if e.quietSkipEdge(tt) {
		t.Fatal("a closed goal must allow planning")
	}

	// goal back open, intent drained -> no active work -> plan
	if err := store.SetNodeState(gid, "open"); err != nil {
		t.Fatal(err)
	}
	fp2, err := store.OpenGoalFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	e.goalFP.Store(tt.ID, fp2)
	if err := store.SetIntentState(iid, "done"); err != nil {
		t.Fatal(err)
	}
	if e.quietSkipEdge(tt) {
		t.Fatal("no active intent must allow planning")
	}
}
