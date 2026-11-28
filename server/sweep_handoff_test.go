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

// TestRunIntentSweepLoopEmitsGateConstantFactAndOperatorHandoff verifies
// end-to-end that 8 probes with different inputs returning identical 403s
// record a planner-visible gate_constant fact, emit an OPERATOR INPUT REQUESTED
// handoff activity, and settle the intent as paused (not a silent pause).
func TestRunIntentSweepLoopEmitsGateConstantFactAndOperatorHandoff(t *testing.T) {
	skipWithoutDSN(t)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"invalid peer certificate"}`))
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
		if cur > 10 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "c_end", "object": "chat.completion", "model": "mock-worker",
				"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
			})
			return
		}
		tcID := "s" + string(rune('a'+cur))
		cmdArgs, _ := json.Marshal(map[string]string{
			"command": "curl -s -i -H 'X-Estate-Peer: peer-" + tcID + "' " + target.URL + "/api/v1/deploy",
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c_" + tcID, "object": "chat.completion", "model": "mock-worker",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":       tcID,
						"type":     "function",
						"function": map[string]any{"name": "Bash", "arguments": string(cmdArgs)},
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
		Name:      "mock-sweep-worker",
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

	task, err := m.CreateTaskWithOptions("sweep handoff", "Audit "+target.URL, db.TaskCreateOptions{
		LLMProfileIDs: []int64{pid},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	intentID, err := task.Store.AddIntent(map[string]any{"summary": "Sweep X-Estate-Peer on " + target.URL + "/api/v1/deploy"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	token, err := task.Store.ClaimIntentToken(intentID, "work#1")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := task.Store.GetNode(intentID)
	if err != nil || intent == nil {
		t.Fatal(err)
	}

	_, worker := s.engine.snapshotFor(task)
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
	if node.State != "paused" {
		t.Fatalf("expected intent paused after sweep loop, got %q", node.State)
	}

	// Verify gate_constant fact is visible to the planner.
	facts, err := task.Store.ListByKind(db.KindFact, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawGateFact bool
	for _, f := range facts {
		var p struct {
			Subtype string `json:"subtype"`
			Path    string `json:"path"`
		}
		if json.Unmarshal(f.Payload, &p) == nil && p.Subtype == db.FactSubtypeGateConstant && p.Path == "/api/v1/deploy" {
			sawGateFact = true
			break
		}
	}
	if !sawGateFact {
		t.Fatalf("expected gate_constant fact recorded in store")
	}

	acts, _, err := task.Store.ActivityList(&intentID, 0, 100)
	if err != nil {
		t.Fatal(err)
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
	if !sawAskOp || !sawHandoffResult {
		t.Fatalf("expected operator handoff activity and result, got sawAskOp=%v sawHandoff=%v", sawAskOp, sawHandoffResult)
	}
}
