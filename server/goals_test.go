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
