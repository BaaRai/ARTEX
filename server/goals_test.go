package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestCreateGoalsNilTask(t *testing.T) {
	got, err := (&Server{}).createGoals(context.Background(), nil, nil)
	if err == nil || len(got) != 0 {
		t.Fatalf("nil task must return an error and no goals: got=%+v err=%v", got, err)
	}
}

// TestStartTaskEngineFailsTaskOnPlainTextDecomposer verifies end-to-end that
// when the Goal Decomposer returns set_goals as plain text instead of a
// structured tool call, startTaskEngine retries once, never materializes empty
// or fallback goals, never starts the planner, and explicitly fails the task.
func TestStartTaskEngineFailsTaskOnPlainTextDecomposer(t *testing.T) {
	skipWithoutDSN(t)
	var (
		mu    sync.Mutex
		calls int
	)
	mockOpenAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "mock-local",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": `set_goals({"goals":[{"text":"Audit lab target"}]})`,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 12, "total_tokens": 32},
		})
	}))
	defer mockOpenAI.Close()

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

	pid, err := m.pg.SaveProfile(&db.LLMProfile{
		Name:      "mock-plaintext-decomposer",
		Format:    "openai",
		BaseURL:   mockOpenAI.URL + "/v1",
		Model:     "mock-local",
		APIKey:    "test-key",
		Streaming: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteProfile(pid) })

	task, err := m.CreateTaskWithOptions("planning-only lab check", "Audit lab target 10.10.52.1", db.TaskCreateOptions{
		LLMProfileIDs: []int64{pid},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	s.startTaskEngine(task)

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("expected exactly 2 decomposer calls (initial + 1 retry), got %d", gotCalls)
	}

	if st := task.lifecycleSnapshot().Status; st != "failed" {
		t.Fatalf("expected in-memory task status failed, got %q", st)
	}
	stored, err := m.pg.GetTask(mustTaskID(t, task.ID))
	if err != nil || stored == nil {
		t.Fatalf("GetTask: %v %v", stored, err)
	}
	if stored.Status != "failed" {
		t.Fatalf("expected persisted task status failed, got %q", stored.Status)
	}

	goals, err := task.Store.ListByKind(db.KindGoal, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 0 {
		t.Fatalf("expected 0 goal nodes materialized after contract failure, got %d", len(goals))
	}

	acts, _, err := task.Store.ActivityList(nil, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	foundErrAct := false
	for _, a := range acts {
		if a.Kind == "result" && a.IsError && strings.Contains(a.Summary, "Goal decomposition failed") {
			foundErrAct = true
			break
		}
	}
	if !foundErrAct {
		t.Fatalf("expected error activity describing goal decomposition failure, got %+v", acts)
	}
}

// TestCreateGoalsNoProviderSeedsRawGoalAndKeepsTaskAlive verifies that when no
// LLM provider is configured, createGoals falls back to seeding the raw task
// goal as a single goal node linked from the origin fact, keeping the task
// alive instead of marking it failed.
func TestCreateGoalsNoProviderSeedsRawGoalAndKeepsTaskAlive(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}
	// Ensure no global LLM profile is configured in this test DB.
	profiles, err := m.pg.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) > 0 {
		t.Skip("test database already has configured LLM profiles")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, "", m.dir, m.dir)
	t.Cleanup(func() { stopTestServer(t, s, cancel) })

	const rawGoal = "Probe http://127.0.0.1:9/v1 for unauthenticated exposure"
	task, err := m.CreateTaskWithOptions("no-provider fallback", rawGoal, db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	// Seed origin fact as launchTask does so the spawns link is exercised.
	s.seed(task, rawGoal)
	originID, err := task.Store.OriginFactID()
	if err != nil || originID <= 0 {
		t.Fatalf("expected origin fact seeded, got id=%d err=%v", originID, err)
	}

	goals, err := s.createGoals(ctx, task, nil)
	if err != nil {
		t.Fatalf("createGoals without provider must fall back cleanly, got err=%v", err)
	}
	if len(goals) != 1 || goals[0].Text != rawGoal {
		t.Fatalf("unexpected fallback goals: %+v", goals)
	}

	nodes, err := task.Store.ListByKind(db.KindGoal, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 persisted goal node, got %d", len(nodes))
	}
	edges, err := task.Store.Edges(100)
	if err != nil {
		t.Fatal(err)
	}
	linked := false
	for _, e := range edges {
		if e.From == originID && e.Rel == db.RelSpawns && e.To == nodes[0].ID {
			linked = true
			break
		}
	}
	if !linked {
		t.Fatalf("fallback goal node #%d not linked from origin fact #%d via %q (edges=%+v)", nodes[0].ID, originID, db.RelSpawns, edges)
	}
	if st := task.lifecycleSnapshot().Status; st == "failed" {
		t.Fatalf("task without provider must stay alive, got status=%q", st)
	}
}
