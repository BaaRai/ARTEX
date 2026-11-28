package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// TestRunIntentPollLoopEmitsAskOperatorActivityAndPausesWithHandoffCause verifies
// end-to-end that when a worker hits 3 identical read-only polls interleaved
// with sleep, the engine itself emits the OPERATOR INPUT REQUESTED activity
// (askOperatorActivity shape), emits a non-error operator_handoff result, and
// settles the intent as paused.
func TestRunIntentPollLoopEmitsAskOperatorActivityAndPausesWithHandoffCause(t *testing.T) {
	skipWithoutDSN(t)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":173,"status":"churned"}`))
	}))
	defer target.Close()

	var (
		mu   sync.Mutex
		turn int
	)
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		turn++
		cur := turn
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		var tcID, tcName, tcArgs string
		pollCmd, _ := json.Marshal(map[string]string{"command": "curl -s " + target.URL + "/api/v1/estate"})
		switch cur {
		case 1:
			tcID, tcName, tcArgs = "t1", "Bash", string(pollCmd)
		case 2:
			tcID, tcName, tcArgs = "t2", "Bash", `{"command":"sleep 0"}`
		case 3:
			tcID, tcName, tcArgs = "t3", "Bash", string(pollCmd)
		case 4:
			tcID, tcName, tcArgs = "t4", "Bash", `{"command":"sleep 0"}`
		case 5:
			tcID, tcName, tcArgs = "t5", "Bash", string(pollCmd)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "c_end", "object": "chat.completion", "model": "mock-worker",
				"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c_" + tcID, "object": "chat.completion", "model": "mock-worker",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":       tcID,
						"type":     "function",
						"function": map[string]any{"name": tcName, "arguments": tcArgs},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer mockLLM.Close()

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
		Name:      "mock-poll-worker",
		Format:    "openai",
		BaseURL:   mockLLM.URL + "/v1",
		Model:     "mock-worker",
		APIKey:    "test-key",
		Streaming: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteProfile(pid) })

	task, err := m.CreateTaskWithOptions("poll escalation", "Audit "+target.URL, db.TaskCreateOptions{
		LLMProfileIDs: []int64{pid},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	intentID, err := task.Store.AddIntent(map[string]any{"summary": "Poll target estate"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	token, err := task.Store.ClaimIntentToken(intentID, "work#1")
	if err != nil {
		t.Fatalf("ClaimIntentToken: %v", err)
	}
	intent, err := task.Store.GetNode(intentID)
	if err != nil || intent == nil {
		t.Fatalf("GetNode: %v %v", intent, err)
	}

	_, worker := s.engine.snapshotFor(task)
	if worker == nil {
		t.Fatal("expected worker snapshot")
	}

	if !s.engine.beginTaskOperation(task.ID) {
		t.Fatal("beginTaskOperation failed")
	}
	ok := s.engine.runIntent(ctx, task, "work#1", worker, intent, token, "", "")
	s.engine.decInflight(task.ID)
	if !ok {
		t.Fatal("runIntent returned false")
	}

	node, err := task.Store.GetNode(intentID)
	if err != nil || node == nil {
		t.Fatal(err)
	}
	acts, _, err := task.Store.ActivityList(&intentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if node.State != "paused" {
		for i, a := range acts {
			t.Logf("act[%d]: kind=%s tool=%s err=%v sum=%q detail=%q", i, a.Kind, a.Tool, a.IsError, a.Summary, a.Detail)
		}
		t.Fatalf("expected intent settled as paused, got %q", node.State)
	}
	var sawAskOp, sawHandoffResult bool
	for _, a := range acts {
		if strings.Contains(a.Summary, "OPERATOR INPUT REQUESTED:") && !a.IsError {
			sawAskOp = true
		}
		if a.Kind == "result" && !a.IsError && strings.Contains(a.Summary, agent.AbortOperatorHandoff.Short) {
			sawHandoffResult = true
		}
	}
	if !sawAskOp {
		t.Fatalf("expected non-error OPERATOR INPUT REQUESTED activity, got: %+v", acts)
	}
	if !sawHandoffResult {
		t.Fatalf("expected non-error operator_handoff result activity, got: %+v", acts)
	}
}
