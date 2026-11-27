package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// capturingRecorder stands in for the host evidence store and hands the refs it
// was given straight back, so a test can assert what reached storage.
type capturingRecorder struct{ got []db.TrafficRef }

func (r *capturingRecorder) Record(_ context.Context, in db.RecordFindingInput, refs []db.TrafficRef) (*db.RecordedFinding, error) {
	r.got = refs
	traffic := &db.FindingTraffic{FindingID: 1}
	for i, ref := range refs {
		traffic.Bindings = append(traffic.Bindings, db.FindingTrafficBinding{ID: int64(i + 1), FindingID: 1, Role: ref.Role, Note: ref.Note})
	}
	return &db.RecordedFinding{FindingID: 1, NodeID: 1, Traffic: traffic}, nil
}

// Regression for the lab finding where a worker had a real proof traffic id and
// still could not register the finding, because it sent "evidence_hint_id": 0
// for the absent optional field. 0 must mean "no hint", never an error.
func TestReportFindingZeroHintIDIsNotAnError(t *testing.T) {
	prev := FindingTrafficBindingEnabled()
	t.Cleanup(func() { SetFindingTrafficBindingEnabled(prev) })
	SetFindingTrafficBindingEnabled(func() bool { return true })

	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("zero hint id", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.DeleteFindingsByTask(task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	ts := NewToolSet(nil, "")
	ts.ts, ts.taskID = d.Exploration(task.ExplorationID), task.ID
	rec := &capturingRecorder{}
	ts.SetFindingRecorder(rec)

	body := `{"vulnclass":"SQLI","severity":"high","summary":"zero hint","evidence":"UNION returns rows","evidence_hint_id":0,` +
		`"traffic_refs":[{"traffic_id":"t-zero-hint","role":"proof"}]}`
	res, err := ts.addFinding().Call(t.Context(), json.RawMessage(body), nil)
	if err != nil {
		t.Fatalf("report_finding returned error: %v", err)
	}
	if got := res.Flatten(); !strings.HasPrefix(got, "finding recorded: ") {
		t.Fatalf("zero hint id rejected: %q", got)
	}
	if len(rec.got) != 1 || rec.got[0].TrafficID != "t-zero-hint" {
		t.Fatalf("explicit proof ref lost: %+v", rec.got)
	}
}

// Regression for the same lab run: the model passed the id of a fact node as
// evidence_hint_id. That must degrade to the explicit traffic_refs the caller
// already supplied instead of discarding an otherwise complete finding.
func TestReportFindingNonHintNodeDegradesToExplicitRefs(t *testing.T) {
	prev := FindingTrafficBindingEnabled()
	t.Cleanup(func() { SetFindingTrafficBindingEnabled(prev) })
	SetFindingTrafficBindingEnabled(func() bool { return true })

	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("fact id as hint", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.DeleteFindingsByTask(task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	exp := d.Exploration(task.ExplorationID)

	// A fact node in the task: exactly what a worker names when it mistakes a
	// fact for a hint.
	fact, err := exp.AddNode(db.KindFact, map[string]any{"summary": "captured UNION proof", "detail": "traffic 123"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := &capturingRecorder{}
	ts := NewToolSet(nil, "")
	ts.ts, ts.taskID = exp, task.ID
	ts.SetFindingRecorder(rec)

	body, _ := json.Marshal(map[string]any{
		"vulnclass": "SQLI", "severity": "high", "summary": "fact id as hint", "evidence": "rows",
		"evidence_hint_id": fact,
		"traffic_refs":     []map[string]any{{"traffic_id": "t-fact-hint", "role": "proof"}},
	})
	res, err := ts.addFinding().Call(t.Context(), json.RawMessage(body), nil)
	if err != nil {
		t.Fatalf("report_finding returned error: %v", err)
	}
	if got := res.Flatten(); !strings.HasPrefix(got, "finding recorded: ") {
		t.Fatalf("fact-node hint discarded the finding: %q", got)
	}
	if len(rec.got) != 1 || rec.got[0].Role != "proof" || rec.got[0].TrafficID != "t-fact-hint" {
		t.Fatalf("explicit proof ref did not survive: %+v", rec.got)
	}
}

// Regression for #550: absent, null, empty, zero, or negative evidence_hint_id
// must succeed even when no explicit traffic_refs are supplied (the default
// workflow where the report agent binds traffic afterward).
func TestReportFindingAbsentOrZeroHintIDWithoutTrafficRefs(t *testing.T) {
	prev := FindingTrafficBindingEnabled()
	t.Cleanup(func() { SetFindingTrafficBindingEnabled(prev) })
	SetFindingTrafficBindingEnabled(func() bool { return true })

	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("absent hint id", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.DeleteFindingsByTask(task.ID)
		_ = d.DeleteTask(task.ID)
	}()

	cases := []struct {
		name string
		body string
	}{
		{"omitted", `{"vulnclass":"SQLI","severity":"high","summary":"omitted hint","evidence":"UNION rows"}`},
		{"zero", `{"vulnclass":"SQLI","severity":"high","summary":"zero hint","evidence":"UNION rows","evidence_hint_id":0}`},
		{"null", `{"vulnclass":"SQLI","severity":"high","summary":"null hint","evidence":"UNION rows","evidence_hint_id":null}`},
		{"empty_string", `{"vulnclass":"SQLI","severity":"high","summary":"empty hint","evidence":"UNION rows","evidence_hint_id":""}`},
		{"negative", `{"vulnclass":"SQLI","severity":"high","summary":"negative hint","evidence":"UNION rows","evidence_hint_id":-5}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := NewToolSet(nil, "")
			ts.ts, ts.taskID = d.Exploration(task.ExplorationID), task.ID
			rec := &capturingRecorder{}
			ts.SetFindingRecorder(rec)

			res, err := ts.addFinding().Call(t.Context(), json.RawMessage(tc.body), nil)
			if err != nil {
				t.Fatalf("report_finding returned error: %v", err)
			}
			if got := res.Flatten(); !strings.HasPrefix(got, "finding recorded: ") {
				t.Fatalf("%s rejected: %q", tc.name, got)
			}
			if len(rec.got) != 0 {
				t.Fatalf("%s unexpectedly bound refs: %+v", tc.name, rec.got)
			}
		})
	}
}

// Regression for #550: a stale/deleted or non-hint evidence_hint_id passed
// without explicit traffic_refs must degrade cleanly (record the finding as
// not_bound with an explanatory evidence_note) instead of discarding it.
func TestReportFindingStaleOrNonHintIDWithoutTrafficRefsDegradesCleanly(t *testing.T) {
	prev := FindingTrafficBindingEnabled()
	t.Cleanup(func() { SetFindingTrafficBindingEnabled(prev) })
	SetFindingTrafficBindingEnabled(func() bool { return true })

	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("stale hint id", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.DeleteFindingsByTask(task.ID)
		_ = d.DeleteTask(task.ID)
	}()
	exp := d.Exploration(task.ExplorationID)
	factID, err := exp.AddNode(db.KindFact, map[string]any{"summary": "not a hint"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		hintID int64
	}{
		{"stale_nonexistent", 999999},
		{"non_hint_fact_node", factID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &capturingRecorder{}
			ts := NewToolSet(nil, "")
			ts.ts, ts.taskID = exp, task.ID
			ts.SetFindingRecorder(rec)

			body, _ := json.Marshal(map[string]any{
				"vulnclass":        "XSS",
				"severity":         "medium",
				"summary":          "stale hint finding",
				"evidence":         "reflected payload",
				"evidence_hint_id": tc.hintID,
			})
			res, err := ts.addFinding().Call(t.Context(), json.RawMessage(body), nil)
			if err != nil {
				t.Fatalf("report_finding returned error: %v", err)
			}
			got := res.Flatten()
			if !strings.HasPrefix(got, "finding recorded: ") {
				t.Fatalf("stale/non-hint evidence_hint_id discarded finding: %q", got)
			}
			if !strings.Contains(got, "Ignored stale or non-local evidence_hint_id=") {
				t.Fatalf("expected explanatory evidence_note, got: %q", got)
			}
			if len(rec.got) != 0 {
				t.Fatalf("expected 0 refs on degraded stale hint, got %+v", rec.got)
			}
		})
	}
}
