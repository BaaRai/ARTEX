package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
)

func TestRecordFactSharedCaptureDoesNotProveGoalPG(t *testing.T) {
	const (
		shared      = "service reports version 2.3"
		exchangeID  = "fixture-exchange-from-task-a"
		foreignURL  = "https://shared.example.test/task-a-resource"
		foreignBody = "unquoted response text from task A"
	)
	a, b := guardTaskStore(t), guardTaskStore(t)
	intentA := addGuardNode(t, a, db.KindIntent, map[string]any{"text": "read the shared service"}, "running")
	intentB := addGuardNode(t, b, db.KindIntent, map[string]any{"text": "check the shared service locally"}, "running")
	appendRow := func(store *db.ExplorationStore, intent int64, worker, kind, detail string) int64 {
		t.Helper()
		step, err := store.AppendActivity(db.Activity{NodeID: &intent, Worker: worker, Kind: kind, Detail: detail})
		if err != nil {
			t.Fatal(err)
		}
		return step
	}
	ownStep, err := b.AppendActivity(db.Activity{NodeID: &intentB, Worker: "worker-b", Kind: "tool_result", Tool: "Bash", Detail: shared})
	if err != nil {
		t.Fatal(err)
	}
	steps, _, err := b.ActivityList(&intentB, 0, 1)
	if err != nil || len(steps) != 1 || steps[0].ID != ownStep {
		t.Fatalf("task B activity: %v, err=%v", steps, err)
	}
	worker := NewToolSet(b, "worker-b")
	worker.SetOwnerNode(intentB)
	planner := NewToolSet(b, "planner")
	// Read the stored record, list its compact view, and try to prove a fresh goal.
	check := func(quote, source, wantSource, wantProv string, wantStep int64, proves bool) map[string]any {
		t.Helper()
		input := fmt.Sprintf(`{"summary":%q,"evidence":%q,"source":%q,"confidence":"observed"}`, quote, quote, source)
		result := callGuardTool(t, worker.recordFact(), input)
		factID, err := strconv.ParseInt(strings.TrimPrefix(result.Flatten(), "fact recorded: "), 10, 64)
		if result.IsError || err != nil {
			t.Fatalf("record_fact: %s", result.Flatten())
		}
		fact, err := b.GetNode(factID)
		if err != nil || fact == nil {
			t.Fatalf("read fact %d: %v", factID, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(fact.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		list := callReadJSON(t, planner.listFacts(), `{"limit":1}`).(map[string]any)["facts"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["id"] != float64(factID) {
			t.Fatalf("list_facts did not return new fact %d: %v", factID, list)
		}
		for _, view := range []map[string]any{payload, list[0].(map[string]any)} {
			if view["source"] != wantSource || view["provenance"] != wantProv {
				t.Errorf("%q source/provenance = %v/%v, want %s/%s", quote, view["source"], view["provenance"], wantSource, wantProv)
			}
			if wantStep > 0 && view["provenance_activity_id"] != float64(wantStep) {
				t.Errorf("%q refers to activity %v, want %d", quote, view["provenance_activity_id"], wantStep)
			}
			if _, present := view["provenance_activity_id"]; wantStep == 0 && present {
				t.Errorf("uncorroborated quote %q retained an activity reference", quote)
			}
			if _, present := view["provenance_traffic_id"]; present {
				t.Errorf("fact %d has an unscoped traffic reference", factID)
			}
			encoded, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			for _, foreign := range []string{exchangeID, foreignURL, foreignBody} {
				if strings.Contains(string(encoded), foreign) {
					t.Errorf("fact %d exposes %q", factID, foreign)
				}
			}
		}
		goal := addGuardNode(t, b, db.KindGoal, map[string]any{"text": quote}, "open")
		proof := callGuardTool(t, planner.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"verify the observation"}`, goal, factID))
		wantState, wantConfidence := "open", "inferred"
		if proves {
			wantState, wantConfidence = "met", "observed"
		}
		if proof.IsError == proves || nodeState(t, b, goal) != wantState || payload["confidence"] != wantConfidence {
			t.Errorf("%q: prove_goal %q, state %s, confidence %v; want state %s, confidence %s", quote, proof.Flatten(), nodeState(t, b, goal), payload["confidence"], wantState, wantConfidence)
		}
		if !proves && !strings.Contains(proof.Flatten(), "unverified") {
			t.Errorf("expected an unverified-evidence refusal for %q, got %s", quote, proof.Flatten())
		}
		return payload
	}

	shellFact := func() map[string]any {
		return check(shared, "tool_output", "inference", "unverified", ownStep, false)
	}
	withoutRecorder := shellFact()
	tr, err := traffic.Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	worker.SetFactTraffic(tr)
	beforeCapture := shellFact()

	appendRow(a, intentA, "worker-a", "tool_result", foreignURL+"\n"+shared)
	at := steps[0].CreatedAt
	raw := fmt.Sprintf(`{"log":{"version":"1.2","entries":[{
		"startedDateTime":%q,"_artexExchangeId":%q,"request":{"method":"GET","url":%q},
		"response":{"status":200,"content":{"mimeType":"text/plain","text":%q}}
	}]}}`, at.Format(time.RFC3339Nano), exchangeID, foreignURL, shared+"\n"+foreignBody)
	imported, err := tr.ImportHAR([]byte(raw), traffic.HARImportOptions{})
	if err != nil || imported.Imported != 1 {
		t.Fatalf("import capture: %+v, err=%v", imported, err)
	}
	if id, hit, err := tr.ResponseContaining([]string{shared}, at.Unix(), at.Unix()); err != nil || !hit || id != exchangeID {
		t.Fatalf("fixture capture lookup: %q, %v, %v", id, hit, err)
	}
	afterCapture := shellFact()
	if !reflect.DeepEqual(beforeCapture, afterCapture) || !reflect.DeepEqual(withoutRecorder, beforeCapture) {
		t.Errorf("task B fact changed with the recorder: detached=%v, empty=%v, matching=%v", withoutRecorder, beforeCapture, afterCapture)
	}

	const declared = "operator confirmed the maintenance window"
	otherIntent := addGuardNode(t, b, db.KindIntent, map[string]any{"text": "separate work"}, "running")
	appendRow(a, intentA, "user", "user", declared)
	appendRow(b, otherIntent, "user", "user", declared)
	appendRow(b, intentB, "worker-b", "user", declared)
	appendRow(b, intentB, "user", "tool_result", declared)
	check(declared, "operator_input", "inference", "unverified", 0, false)
	operatorStep := appendRow(b, intentB, "user", "user", declared)
	appendRow(b, intentB, "worker-b", "tool_result", declared)
	check(declared, "operator_input", "operator_input", "operator_input", operatorStep, true)
	check(declared+"\nextra statement absent from the operator message", "operator_input", "inference", "unverified", 0, false)
}
