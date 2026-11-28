package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHypothesisLedgerOverviewAndClosedVariantRejection(t *testing.T) {
	ts := guardTaskStore(t)
	tsx := NewToolSet(ts, "planner")

	// 1. Create an open hypothesis and close it with close_reason.
	res, err := tsx.upsertHypothesis().Call(context.Background(), json.RawMessage(`{
		"hypothesis":"X-Estate-Peer accepts seed fingerprint",
		"direction":"Probe X-Estate-Peer with seed fingerprints",
		"status":"open"
	}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("create hypothesis failed: %v %s", err, res.Flatten())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(res.Flatten()), &created); err != nil || created.ID <= 0 {
		t.Fatalf("parse created hypothesis: %v (%s)", err, res.Flatten())
	}

	closeBody, _ := json.Marshal(map[string]any{
		"id":           created.ID,
		"status":       "closed",
		"close_reason": "negative_sweep_identical_responses",
	})
	if res := callGuardTool(t, tsx.upsertHypothesis(), string(closeBody)); res.IsError {
		t.Fatalf("close hypothesis failed: %s", res.Flatten())
	}

	// 2. graph_overview surfaces closed_hypotheses.
	ov := tsx.graphOverviewData()
	closed, _ := ov["closed_hypotheses"].([]map[string]any)
	if len(closed) != 1 || closed[0]["close_reason"] != "negative_sweep_identical_responses" {
		t.Fatalf("expected 1 closed hypothesis in overview, got %+v", ov["closed_hypotheses"])
	}

	// 3. add_intent rejects variants matching by hypothesis_id OR normalized direction.
	byID, _ := json.Marshal(map[string]any{
		"summary":       "Try another X-Estate-Peer header",
		"hypothesis_id": created.ID,
	})
	if res := callGuardTool(t, tsx.addIntent(), string(byID)); !res.IsError || !strings.Contains(res.Flatten(), "is closed") {
		t.Fatalf("expected closed hypothesis_id rejection, got: %s", res.Flatten())
	}

	byDir := `{"summary":"Probe X-Estate-Peer with seed fingerprints"}`
	if res := callGuardTool(t, tsx.addIntent(), byDir); !res.IsError || !strings.Contains(res.Flatten(), "is closed") {
		t.Fatalf("expected closed normalized-direction rejection, got: %s", res.Flatten())
	}

	// 4. Providing both new_evidence and reopen_reason reopens the hypothesis and creates the intent.
	reopen, _ := json.Marshal(map[string]any{
		"summary":       "Test X-Estate-Peer with enrolment token from contract",
		"hypothesis_id": created.ID,
		"new_evidence":  "GET /api/v1/contract returned enrolment_token",
		"reopen_reason": "enrolment record format differs from seed fingerprint",
	})
	if res := callGuardTool(t, tsx.addIntent(), string(reopen)); res.IsError {
		t.Fatalf("reopen add_intent failed: %s", res.Flatten())
	}
	ov2 := tsx.graphOverviewData()
	if openH, _ := ov2["open_hypotheses"].([]map[string]any); len(openH) != 1 {
		t.Fatalf("expected reopened hypothesis in open_hypotheses, got %+v", ov2["open_hypotheses"])
	}
}
