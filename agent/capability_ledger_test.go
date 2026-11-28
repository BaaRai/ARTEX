package agent

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func TestCapabilityLedgerCreationOverviewStrictRejectionAndUnverifiedGoalBlock(t *testing.T) {
	ts := guardTaskStore(t)
	tsx := NewToolSet(ts, "planner")
	ResetCapabilityStrictMode()
	t.Cleanup(ResetCapabilityStrictMode)

	// 1. Record an unverified capability (no proving_ids or traffic_refs) -> stored verified=false, cannot prove goal.
	goalID, err := ts.AddNode(db.KindGoal, map[string]any{"text": "Obtain edge peer access"}, 0, "open", "goals", nil)
	if err != nil {
		t.Fatal(err)
	}
	unverRes := callGuardTool(t, tsx.recordCapability(), `{
		"status":"held",
		"holder":"member_session",
		"verb":"impersonate_edge",
		"object":"internal-origin",
		"evidence":"guessed header",
		"confidence":"observed"
	}`)
	if unverRes.IsError {
		t.Fatalf("record_capability unverified failed: %s", unverRes.Flatten())
	}
	var unver struct {
		ID       int64 `json:"id"`
		Verified bool  `json:"verified"`
	}
	if err := json.Unmarshal([]byte(unverRes.Flatten()), &unver); err != nil || unver.Verified {
		t.Fatalf("expected verified=false without proving refs, got: %s", unverRes.Flatten())
	}
	proveBody, _ := json.Marshal(map[string]any{
		"goal_id":     goalID,
		"evidence_id": unver.ID,
		"reason":      "claimed capability",
	})
	if res := callGuardTool(t, tsx.proveGoal(), string(proveBody)); !res.IsError || !strings.Contains(res.Flatten(), "unverified") {
		t.Fatalf("unverified capability must not satisfy a goal, got: %s", res.Flatten())
	}

	// 2. Record a verified capability linked to a confirmed fact.
	factID, err := ts.AddNode(db.KindFact, map[string]any{
		"summary": "supersedes IDOR retired foreign cert",
		"source":  "target_response",
	}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	verBody, _ := json.Marshal(map[string]any{
		"status":      "held",
		"holder":      "member_session",
		"verb":        "supersede_cert",
		"object":      "foreign_cert",
		"evidence":    "POST /api/v1/certificates/upload returned proof",
		"confidence":  "observed",
		"proving_ids": []int64{factID},
	})
	verRes := callGuardTool(t, tsx.recordCapability(), string(verBody))
	if verRes.IsError {
		t.Fatalf("verified record_capability failed: %s", verRes.Flatten())
	}
	var ver struct {
		ID       int64 `json:"id"`
		Verified bool  `json:"verified"`
	}
	if err := json.Unmarshal([]byte(verRes.Flatten()), &ver); err != nil || !ver.Verified {
		t.Fatalf("expected verified=true with proving_ids, got: %s", verRes.Flatten())
	}

	// Record a required capability so graph_overview shows both held and required.
	reqRes := callGuardTool(t, tsx.recordCapability(), `{
		"status":"required",
		"holder":"edge_peer",
		"verb":"present_enrolment",
		"object":"X-Estate-Peer",
		"evidence":"required for internal origin",
		"confidence":"observed"
	}`)
	if reqRes.IsError {
		t.Fatalf("required record_capability failed: %s", reqRes.Flatten())
	}

	ov := tsx.graphOverviewData()
	held, _ := ov["capabilities_held"].([]map[string]any)
	req, _ := ov["capabilities_required"].([]map[string]any)
	if len(held) != 2 || len(req) != 1 {
		t.Fatalf("unexpected capability overview: held=%+v req=%+v", ov["capabilities_held"], ov["capabilities_required"])
	}

	// 3. Shadow mode flags missing gains without rejecting; strict mode rejects missing gains.
	SetCapabilityStrictMode(false)
	shadowRes := callGuardTool(t, tsx.addIntent(), `{"summary":"Probe /docs without gains"}`)
	if shadowRes.IsError {
		t.Fatalf("shadow mode must not reject add_intent: %s", shadowRes.Flatten())
	}
	SetCapabilityStrictMode(true)
	strictRes := callGuardTool(t, tsx.addIntent(), `{"summary":"Probe /docs without gains"}`)
	if !strictRes.IsError || !strings.Contains(strictRes.Flatten(), "gains") {
		t.Fatalf("strict mode must reject add_intent without gains: %s", strictRes.Flatten())
	}
}

type capabilityChainProvider struct {
	mu    sync.Mutex
	reqs  []llm.CompletionRequest
	round int
}

func (p *capabilityChainProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	idx := len(p.reqs)
	p.mu.Unlock()
	var evs []llm.StreamEvent
	switch idx {
	case 1:
		// Round 1, turn 1: planner dispatches Step 1 with gains.
		evs = []llm.StreamEvent{
			{Type: llm.SEToolUseStart, ToolID: "c1", ToolName: "add_intent"},
			{Type: llm.SEToolInputJSON, Text: `{"summary":"Fetch pristine contract and enrolment record","gains":[{"verb":"read_enrolment","object":"estate_client"}]}`},
			{Type: llm.SEMessageDelta, StopReason: "tool_use"},
		}
	case 2:
		evs = []llm.StreamEvent{{Type: llm.SETextDelta, Text: "Step 1 dispatched."}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
	case 3:
		// Round 2, turn 1: planner sees held capability and dispatches Step 2 consuming it.
		evs = []llm.StreamEvent{
			{Type: llm.SEToolUseStart, ToolID: "c2", ToolName: "add_intent"},
			{Type: llm.SEToolInputJSON, Text: `{"summary":"Present enrolment record in X-Estate-Peer","consumes":[{"verb":"read_enrolment","object":"estate_client"}],"gains":[{"verb":"access_origin","object":"internal_api"}]}`},
			{Type: llm.SEMessageDelta, StopReason: "tool_use"},
		}
	default:
		evs = []llm.StreamEvent{{Type: llm.SETextDelta, Text: "Step 2 dispatched."}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, ev := range evs {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (p *capabilityChainProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for ev, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(ev)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

// End-to-end two-step chain planned from a capability gap.
func TestPlannerTwoStepChainFromCapabilityGap(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("cap chain", "Acquire internal origin access via enrolment", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := d.Exploration(task.ExplorationID)
	if _, err := ts.AddNode(db.KindGoal, map[string]any{"text": "Access internal origin"}, 0, "open", "goals", nil); err != nil {
		t.Fatal(err)
	}
	tsx := NewToolSet(ts, "planner")
	// Seed required capability gap.
	if res := callGuardTool(t, tsx.recordCapability(), `{"status":"required","holder":"edge_peer","verb":"access_origin","object":"internal_api","evidence":"goal requirement","confidence":"observed"}`); res.IsError {
		t.Fatal(res.Flatten())
	}

	prov := &capabilityChainProvider{}
	pl := NewPlanner(prov, "test-model", t.TempDir(), nil, 0, 3)

	// Round 1: sees capabilities_required gap, dispatches Step 1.
	if _, _, err := pl.Plan(t.Context(), task.ID, nil, ts, "Access internal origin", nil, nil, nil); err != nil {
		t.Fatalf("round 1 Plan failed: %v", err)
	}
	// Simulate Step 1 completing and granting verified capability.
	factID, err := ts.AddNode(db.KindFact, map[string]any{"summary": "Captured enrolment record"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	capBody, _ := json.Marshal(map[string]any{
		"status": "held", "holder": "member_session", "verb": "read_enrolment", "object": "estate_client",
		"evidence": "GET /api/v1/contract", "confidence": "observed", "proving_ids": []int64{factID},
	})
	if res := callGuardTool(t, tsx.recordCapability(), string(capBody)); res.IsError {
		t.Fatal(res.Flatten())
	}

	// Round 2: sees capabilities_held + capabilities_required, dispatches Step 2 consuming Step 1's capability.
	if _, _, err := pl.Plan(t.Context(), task.ID, nil, ts, "Access internal origin", nil, nil, nil); err != nil {
		t.Fatalf("round 2 Plan failed: %v", err)
	}

	// Verify Round 2 prompt carried capabilities_held & capabilities_required and 2 chained intents exist.
	var r2Parts []string
	for _, msg := range prov.reqs[2].Messages {
		for _, b := range msg.Content {
			r2Parts = append(r2Parts, b.Text)
		}
	}
	r2Msg := strings.Join(r2Parts, "\n")
	if !strings.Contains(r2Msg, "capabilities_held") || !strings.Contains(r2Msg, "capabilities_required") {
		t.Fatalf("round 2 planner prompt missing capability ledger sections: %s", r2Msg)
	}
	intents, err := ts.ListByKind(db.KindIntent, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 {
		t.Fatalf("expected 2 chained intents, got %d", len(intents))
	}
	if !strings.Contains(string(intents[0].Payload), "consumes") && !strings.Contains(string(intents[1].Payload), "consumes") {
		t.Fatalf("expected Step 2 intent to declare consumes, got payloads: %s / %s", intents[0].Payload, intents[1].Payload)
	}
}
