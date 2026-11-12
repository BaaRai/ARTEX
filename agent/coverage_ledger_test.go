package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func hasTool(ts *ToolSet, name string) bool {
	for _, tool := range ts.DropCoverageTools(ts.WorkerTools()) {
		if tool.Name() == name {
			return true
		}
	}
	return false
}

// coverage_enabled=false: no tool, no write, no planner line.
func TestRecordCoverageDisabledPassthrough(t *testing.T) {
	ts := NewToolSet(nil, "worker-1")
	ts.SetTaskID(1)
	if !hasTool(ts, "record_coverage") {
		t.Fatal("record_coverage missing with coverage on")
	}
	ts.SetCoverageEnabled(false)
	if hasTool(ts, "record_coverage") {
		t.Fatal("record_coverage exposed with coverage off")
	}
	res := callGuardTool(t, ts.recordCoverage(), `{"surface":"/q","risk_area":"sqli","outcome":"no_issue_found"}`)
	if !res.IsError {
		t.Fatal("record_coverage wrote with coverage off")
	}
	if s := ts.coverageLedgerBlock(true); s != "" {
		t.Fatalf("planner line with coverage off: %q", s)
	}
}

func TestRenderCoverageLedger(t *testing.T) {
	gapsOnly := db.BuildCoverageLedger(nil, []string{"sqli"}, db.CoverageCompleteness{})
	mixed := db.BuildCoverageLedger([]db.CoverageEntry{
		{Surface: "/a", RiskArea: "xss", Outcome: db.OutcomeNeedsFollowUp},
		{Surface: "/b", RiskArea: "idor", Outcome: db.OutcomeRuledOut},
		{Surface: "/c", RiskArea: "rce", Outcome: db.OutcomeNoIssueFound},
		{Surface: "/d", RiskArea: "ssrf", Outcome: db.OutcomeNoIssueFound, Evidence: "fact 3"},
	}, []string{"sqli"}, db.CoverageCompleteness{})
	cases := []struct {
		name    string
		l       *db.CoverageLedger
		wrapup  bool
		want    []string
		notWant []string
	}{
		{"no entries: no planner line", gapsOnly, false, nil, []string{"Coverage ledger"}},
		{"no ledger", nil, true, nil, []string{"Coverage ledger"}},
		{"counters and top gaps", mixed, false,
			[]string{"entries 4", "no_issue_found 2", "gaps 4", "unrecorded_risk_class sqli", "…"}, []string{"rce"}},
		{"wrap-up lists every gap", mixed, true,
			[]string{"remaining gaps at wrap-up (4)", "- unrecorded_risk_class sqli", "- missing_evidence /c rce"}, nil},
		{"wrap-up lists unrecorded classes without entries", gapsOnly, true, []string{"- unrecorded_risk_class sqli"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderCoverageLedger(c.l, c.wrapup)
			if c.want == nil && got != "" {
				t.Fatalf("want empty, got %q", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in %q", w, got)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in %q", w, got)
				}
			}
		})
	}
}

// PG: record_coverage is a new seeded key; on an existing database the seed
// inserts it and leaves already-seeded (possibly edited) rows untouched.
func TestRecordCoverageSeededOnExistingDB(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	seed := func() {
		for _, s := range BuiltinToolSeeds() {
			schema, _ := json.Marshal(s.Schema)
			agents, _ := json.Marshal(s.Agents)
			if err := d.SeedTool(s.Key, s.Desc, schema, agents); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed()
	var desc string
	if err := d.QueryRow(`SELECT description FROM tools WHERE key='record_fact'`).Scan(&desc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`UPDATE tools SET description=$1 WHERE key='record_fact'`, desc) })
	if _, err := d.Exec(`UPDATE tools SET description='operator edit' WHERE key='record_fact'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM tools WHERE key='record_coverage'`); err != nil { // a database from before this key
		t.Fatal(err)
	}
	seed()
	tool, err := d.GetTool("record_coverage")
	if err != nil || tool == nil || !tool.System || strings.Join(tool.Agents, ",") != "worker" {
		t.Fatalf("record_coverage row=%+v err=%v", tool, err)
	}
	if fact, _ := d.GetTool("record_fact"); fact == nil || fact.Description != "operator edit" {
		t.Fatalf("existing tool overwritten: %+v", fact)
	}
}

// PG: the tool writes the entry, flags a missing evidence as a gap, and the planner
// line appears once an entry exists.
func TestRecordCoverageToolWritesEntry(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("record_coverage", "g", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := NewToolSet(d.Exploration(task.ExplorationID), "worker-1")
	ts.SetTaskID(task.ID)
	ts.SetAssetStore(d.Assets(), d.Companies())
	if s := ts.coverageLedgerBlock(false); s != "" {
		t.Fatalf("planner line before any entry: %q", s)
	}
	res := callGuardTool(t, ts.recordCoverage(), `{"surface":"/q","risk_area":"SQLi","outcome":"no_issue_found"}`)
	if res.IsError || !strings.Contains(res.Flatten(), "missing_evidence") {
		t.Fatalf("result=%+v", res)
	}
	if s := ts.coverageLedgerBlock(false); !strings.Contains(s, "missing_evidence /q sqli") {
		t.Fatalf("planner line=%q", s)
	}
}
