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

	// Task B recorded ownStep (kind=tool_result) locally: task-scoped tool_output
	// stays provenance=recorded_activity tied to ownStep, NEVER gains
	// captured_traffic or provenance_traffic_id from Task A's shared proxy store.
	shellFact := func() map[string]any {
		return check(shared, "tool_output", "tool_output", factProvenanceRecorded, ownStep, true)
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

// TestProveGoalRefusesLinkedTaskFactPG verifies that prove_goal rejects facts
// belonging to a linked source task even when GetNodeWithSources can read them.
func TestProveGoalRefusesLinkedTaskFactPG(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	taskA, err := d.CreateTaskWithOptions("source-task-602", "source task", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(taskA.ID) })
	taskB, err := d.CreateTaskWithOptions("linked-task-602", "linked task", db.TaskCreateOptions{SourceTaskIDs: []int64{taskA.ID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(taskB.ID) })

	storeA := d.Exploration(taskA.ExplorationID)
	storeB := d.Exploration(taskB.ExplorationID)

	intentA := addGuardNode(t, storeA, db.KindIntent, map[string]any{"text": "verify tcp port"}, "running")
	const quote = "6379/tcp open redis_version:7.2.4"
	if _, err := storeA.AppendActivity(db.Activity{NodeID: &intentA, Worker: "worker-a", Kind: "tool_result", Tool: "Bash", Detail: quote}); err != nil {
		t.Fatal(err)
	}
	workerA := NewToolSet(storeA, "worker-a")
	workerA.SetOwnerNode(intentA)
	rec := callGuardTool(t, workerA.recordFact(), fmt.Sprintf(`{"summary":"redis open in task A","evidence":%q,"source":"tool_output","confidence":"observed"}`, quote))
	if rec.IsError {
		t.Fatalf("record_fact in task A: %s", rec.Flatten())
	}
	factA, err := strconv.ParseInt(strings.TrimPrefix(rec.Flatten(), "fact recorded: "), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity check: linked task B CAN see task A's fact via GetNodeWithSources.
	if seen, err := storeB.GetNodeWithSources(factA); err != nil || seen == nil {
		t.Fatalf("expected task B GetNodeWithSources(%d) to resolve linked fact, got %v, %v", factA, seen, err)
	}
	// prove_goal on task B MUST refuse task A's fact.
	goalB := addGuardNode(t, storeB, db.KindGoal, map[string]any{"text": "confirm redis on task B"}, "open")
	plannerB := NewToolSet(storeB, "planner")
	res := callGuardTool(t, plannerB.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"reuse linked fact"}`, goalB, factA))
	if !res.IsError || !strings.Contains(res.Flatten(), "this task") {
		t.Fatalf("linked-task fact must be refused by prove_goal: isErr=%v out=%q", res.IsError, res.Flatten())
	}
	if st := nodeState(t, storeB, goalB); st != "open" || plannerB.GoalMet {
		t.Fatalf("goalB must stay open: state=%q GoalMet=%v", st, plannerB.GoalMet)
	}
}

// TestProveGoalFailsClosedWithoutProvenancePG verifies that legacy or manually
// written facts without server-verified provenance (missing, empty, or retired
// captured_traffic) fail closed and cannot close a goal.
func TestProveGoalFailsClosedWithoutProvenancePG(t *testing.T) {
	s := guardTaskStore(t)
	goal := addGuardNode(t, s, db.KindGoal, map[string]any{"text": "verify non-http service"}, "open")
	planner := NewToolSet(s, "planner")

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"missing provenance", map[string]any{"summary": "legacy fact without provenance", "source": "tool_output", "confidence": "observed"}},
		{"empty provenance", map[string]any{"summary": "empty provenance fact", "source": "tool_output", "confidence": "observed", "provenance": ""}},
		{"retired captured_traffic", map[string]any{"summary": "retired capture fact", "source": "tool_output", "confidence": "observed", "provenance": "captured_traffic"}},
		{"missing activity id", map[string]any{"summary": "recorded without activity", "source": "tool_output", "confidence": "observed", "provenance": factProvenanceRecorded}},
		{"foreign or nonexistent activity id", map[string]any{"summary": "bogus activity ref", "source": "tool_output", "confidence": "observed", "provenance": factProvenanceRecorded, "provenance_activity_id": 999999999}},
	}
	for _, tc := range cases {
		factID := addGuardNode(t, s, db.KindFact, tc.payload, "confirmed")
		res := callGuardTool(t, planner.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"legacy check"}`, goal, factID))
		if !res.IsError || !strings.Contains(res.Flatten(), "fail-closed") {
			t.Errorf("%s: want fail-closed refusal, got isErr=%v out=%q", tc.name, res.IsError, res.Flatten())
		}
		if st := nodeState(t, s, goal); st != "open" || planner.GoalMet {
			t.Errorf("%s: goal closed unexpectedly: state=%q GoalMet=%v", tc.name, st, planner.GoalMet)
		}
	}
}

// TestProveGoalTaskScopedToolOutputNonHTTPPG verifies that TCP/DNS/non-HTTP
// goals can still close via a task-scoped observed tool_output fact tied to a
// recorded tool_result activity in this task (without an operator message),
// while assistant text or paraphrased tool_output facts are refused.
func TestProveGoalTaskScopedToolOutputNonHTTPPG(t *testing.T) {
	s := guardTaskStore(t)
	intent := addGuardNode(t, s, db.KindIntent, map[string]any{"text": "enumerate DNS AXFR"}, "running")
	// Assistant prose alone must NOT satisfy tool_output.
	const proseOnly = "assistant claims AXFR zone transfer succeeded on ns1.example.test"
	if _, err := s.AppendActivity(db.Activity{NodeID: &intent, Worker: "worker-1", Kind: "text", Detail: proseOnly}); err != nil {
		t.Fatal(err)
	}
	// Real tool_result output from dig DOES satisfy tool_output.
	const digOut = "internal.example.test. 300 IN A 10.20.30.40"
	if _, err := s.AppendActivity(db.Activity{NodeID: &intent, Worker: "worker-1", Kind: "tool_result", Tool: "Bash", Detail: digOut}); err != nil {
		t.Fatal(err)
	}
	worker := NewToolSet(s, "worker-1")
	worker.SetOwnerNode(intent)
	planner := NewToolSet(s, "planner")

	goal := addGuardNode(t, s, db.KindGoal, map[string]any{"text": "verify DNS zone transfer"}, "open")

	// 1. Quoting assistant prose (kind=text, not tool_result) is downgraded & refused.
	r1 := callGuardTool(t, worker.recordFact(), fmt.Sprintf(`{"summary":"AXFR worked","evidence":%q,"source":"tool_output","confidence":"observed"}`, proseOnly))
	f1, err := strconv.ParseInt(strings.TrimPrefix(r1.Flatten(), "fact recorded: "), 10, 64)
	if r1.IsError || err != nil {
		t.Fatalf("record prose fact: %s %v", r1.Flatten(), err)
	}
	if res := callGuardTool(t, planner.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"prose"}`, goal, f1)); !res.IsError || nodeState(t, s, goal) != "open" {
		t.Fatalf("assistant prose must not satisfy tool_output proof: isErr=%v out=%q", res.IsError, res.Flatten())
	}

	// 2. Quoting real tool_result output proves the non-HTTP goal without operator input.
	r2 := callGuardTool(t, worker.recordFact(), fmt.Sprintf(`{"summary":"AXFR returned internal A record","evidence":%q,"source":"tool_output","confidence":"observed"}`, digOut))
	f2, err := strconv.ParseInt(strings.TrimPrefix(r2.Flatten(), "fact recorded: "), 10, 64)
	if r2.IsError || err != nil {
		t.Fatalf("record tool_result fact: %s %v", r2.Flatten(), err)
	}
	if res := callGuardTool(t, planner.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"dig AXFR output"}`, goal, f2)); res.IsError || nodeState(t, s, goal) != "met" || !planner.GoalMet {
		t.Fatalf("task-scoped tool_output fact must close non-HTTP goal: isErr=%v state=%q GoalMet=%v out=%q", res.IsError, nodeState(t, s, goal), planner.GoalMet, res.Flatten())
	}
}

// TestProveGoalOperatorInputSummaryMustMatchEvidencePG verifies that an
// operator_input fact citing a real benign human message cannot close a goal
// with a fabricated summary (e.g. "admin access granted"), while a summary
// whose normalized text is contained in the verified operator quote still works.
func TestProveGoalOperatorInputSummaryMustMatchEvidencePG(t *testing.T) {
	s := guardTaskStore(t)
	intent := addGuardNode(t, s, db.KindIntent, map[string]any{"text": "check operator confirmation"}, "running")
	const benignMsg = "maintenance window starts at 02:00 UTC for routine patching"
	if _, err := s.AppendActivity(db.Activity{NodeID: &intent, Worker: "user", Kind: "user", Detail: benignMsg}); err != nil {
		t.Fatal(err)
	}
	worker := NewToolSet(s, "worker-1")
	worker.SetOwnerNode(intent)
	planner := NewToolSet(s, "planner")

	goal := addGuardNode(t, s, db.KindGoal, map[string]any{"text": "obtain admin access"}, "open")

	// 1. Benign operator quote + fabricated summary "admin access granted" -> REFUSED.
	recBad := callGuardTool(t, worker.recordFact(), fmt.Sprintf(`{"summary":"admin access granted","evidence":%q,"source":"operator_input","confidence":"observed"}`, benignMsg))
	badID, err := strconv.ParseInt(strings.TrimPrefix(recBad.Flatten(), "fact recorded: "), 10, 64)
	if recBad.IsError || err != nil {
		t.Fatalf("record mismatched operator_input fact: %s %v", recBad.Flatten(), err)
	}
	resBad := callGuardTool(t, planner.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"benign quote with admin summary"}`, goal, badID))
	if !resBad.IsError || !strings.Contains(resBad.Flatten(), "not contained in its verified operator evidence") {
		t.Fatalf("mismatched operator_input summary must be refused: isErr=%v out=%q", resBad.IsError, resBad.Flatten())
	}
	if st := nodeState(t, s, goal); st != "open" || planner.GoalMet {
		t.Fatalf("goal must stay open after mismatched operator_input summary: state=%q GoalMet=%v", st, planner.GoalMet)
	}

	// 2. Normalized summary contained in verified operator quote (case/whitespace variant) -> ACCEPTED.
	recOK := callGuardTool(t, worker.recordFact(), fmt.Sprintf(`{"summary":"  Maintenance   Window Starts at 02:00 UTC ","evidence":%q,"source":"operator_input","confidence":"observed"}`, benignMsg))
	okID, err := strconv.ParseInt(strings.TrimPrefix(recOK.Flatten(), "fact recorded: "), 10, 64)
	if recOK.IsError || err != nil {
		t.Fatalf("record matching operator_input fact: %s %v", recOK.Flatten(), err)
	}
	resOK := callGuardTool(t, planner.proveGoal(), fmt.Sprintf(`{"goal_id":%d,"evidence_id":%d,"reason":"exact operator confirmation"}`, goal, okID))
	if resOK.IsError || nodeState(t, s, goal) != "met" || !planner.GoalMet {
		t.Fatalf("matching operator_input summary should prove goal: isErr=%v state=%q GoalMet=%v out=%q", resOK.IsError, nodeState(t, s, goal), planner.GoalMet, resOK.Flatten())
	}
}
