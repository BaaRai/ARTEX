package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRetargetTaskUpdatesGoalConstraintsScopeAndStopsStaleIntents(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, "", m.dir, m.dir)
	t.Cleanup(func() { stopTestServer(t, s, cancel) })

	task, err := m.CreateTaskWithOptions("retarget", "Capture flags on https://old-0c3f.ctf.local/docs", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	// Seed goal node, constraint, scope, and stale + unrelated intents.
	gid, err := task.Store.AddNode(db.KindGoal, map[string]any{"text": "Capture flag 2 on old-0c3f.ctf.local"}, 0, "open", "goals", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.Store.AddConstraint("allow", "only test old-0c3f.ctf.local", "goals"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Assets().AddAgentScope(mustTaskID(t, task.ID), "subdomain", "old-0c3f.ctf.local", "seed", "manual"); err != nil {
		t.Fatal(err)
	}
	staleID, err := task.Store.AddIntent(map[string]any{"summary": "Poll https://old-0c3f.ctf.local/api/v1/estate"}, 0, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	keepID, err := task.Store.AddIntent(map[string]any{"summary": "Analyze offline docs"}, 0, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	res, stopped, err := s.RetargetTask(ctx, task.ID, "old-0c3f.ctf.local", "https://new-4791.ctf.local")
	if err != nil {
		t.Fatalf("RetargetTask: %v", err)
	}
	if stopped != 1 || res.GoalsUpdated != 1 || res.ConstraintsUpdated != 1 {
		t.Fatalf("unexpected retarget counts: res=%+v stopped=%d", res, stopped)
	}
	if !strings.Contains(task.Goal, "new-4791.ctf.local") || strings.Contains(task.Goal, "old-0c3f.ctf.local") {
		t.Fatalf("live task.Goal not updated: %q", task.Goal)
	}
	goalNode, err := task.Store.GetNode(gid)
	if err != nil || goalNode == nil {
		t.Fatal(err)
	}
	var gp map[string]any
	_ = json.Unmarshal(goalNode.Payload, &gp)
	if !strings.Contains(gp["text"].(string), "new-4791.ctf.local") {
		t.Fatalf("goal node payload not updated: %+v", gp)
	}
	cs, _ := task.Store.ListConstraints()
	if len(cs) != 1 || !strings.Contains(cs[0].Text, "new-4791.ctf.local") {
		t.Fatalf("constraint not updated: %+v", cs)
	}
	staleNode, _ := task.Store.GetNode(staleID)
	keepNode, _ := task.Store.GetNode(keepID)
	if staleNode.State != "stopped" || keepNode.State != "open" {
		t.Fatalf("intent states mismatch: stale=%s keep=%s", staleNode.State, keepNode.State)
	}
}
