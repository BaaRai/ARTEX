package db

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBuildCoverageLedgerGaps(t *testing.T) {
	e := func(surface, risk, outcome, evidence, reason string) CoverageEntry {
		return CoverageEntry{Surface: surface, RiskArea: risk, Outcome: outcome, Evidence: evidence, Reason: reason}
	}
	cases := []struct {
		name       string
		entries    []CoverageEntry
		mandatory  []string
		want       []string // CoverageGap.String()
		unexamined []string
	}{
		{"mandatory class with no entry is unexamined, never clean", nil, []string{"SQLi"},
			[]string{"unrecorded_risk_class sqli"}, []string{"sqli"}},
		{"needs_follow_up is a gap", []CoverageEntry{e("/login", "idor", OutcomeNeedsFollowUp, "", "")}, nil,
			[]string{"needs_follow_up /login idor"}, nil},
		{"no_issue_found without evidence is a gap", []CoverageEntry{e("/q", "sqli", OutcomeNoIssueFound, " ", "")}, []string{"sqli"},
			[]string{"missing_evidence /q sqli"}, nil},
		{"no_issue_found with evidence closes the class", []CoverageEntry{e("/q", "sqli", OutcomeNoIssueFound, "fact 12", "")}, []string{"SQLi "},
			nil, nil},
		{"ruled_out without reason is a gap", []CoverageEntry{e("/a", "xss", OutcomeRuledOut, "", "")}, nil,
			[]string{"missing_reason /a xss"}, nil},
		{"not_applicable without reason is a gap", []CoverageEntry{e("/a", "ssrf", OutcomeNotApplicable, "", "")}, nil,
			[]string{"missing_reason /a ssrf"}, nil},
		{"all three sources, unrecorded first", []CoverageEntry{e("/b", "xss", OutcomeNeedsFollowUp, "", ""), e("/c", "rce", OutcomeNoIssueFound, "", "")}, []string{"xss", "idor"},
			[]string{"unrecorded_risk_class idor", "needs_follow_up /b xss", "missing_evidence /c rce"}, []string{"idor"}},
		{"no goals and no entries: no obligation, no gap", nil, nil, nil, nil},
		{"reported closes the class", []CoverageEntry{e("/r", "sqli", OutcomeReported, "", "")}, []string{"sqli"}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := BuildCoverageLedger(c.entries, c.mandatory, CoverageCompleteness{})
			got := []string{}
			for _, g := range l.Gaps {
				got = append(got, g.String())
			}
			if c.want == nil {
				c.want = []string{}
			}
			if c.unexamined == nil {
				c.unexamined = []string{}
			}
			if !reflect.DeepEqual(got, c.want) || !reflect.DeepEqual(l.Unexamined, c.unexamined) {
				t.Fatalf("gaps=%v unexamined=%v, want %v %v", got, l.Unexamined, c.want, c.unexamined)
			}
		})
	}
}

func TestCoverageCompletenessFor(t *testing.T) {
	cases := []struct {
		status               string
		paused, budget, past bool
		state                string
	}{
		{"done", false, false, false, "complete"},
		{"timeout", false, false, true, "timeout"},
		{"done", false, false, true, "timeout"}, // goals met on the final timeout round
		{"timeout", false, true, false, "budget"},
		{"done", false, true, false, "budget"}, // budget wrap-up that met the goals
		{"failed", false, false, false, "aborted"},
		{"running", true, false, false, "paused"},
		{"running", false, false, false, "in_progress"},
	}
	for _, c := range cases {
		got := CoverageCompletenessFor(c.status, c.paused, c.budget, c.past)
		if got.State != c.state || got.Complete != (c.state == "complete") || got.Caveat == "" {
			t.Errorf("%+v: got %+v", c, got)
		}
	}
}

// The GET coverage document carries entries, gaps and completeness at top level.
func TestCoverageJSONCarriesLedger(t *testing.T) {
	l := BuildCoverageLedger(nil, []string{"sqli"}, CoverageCompletenessFor("timeout", false, false, true))
	b, _ := json.Marshal(&Coverage{Enabled: true, ByType: []CoverageByType{}, CoverageLedger: l})
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"entries", "gaps", "unexamined", "completeness", "by_type"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %q in %s", k, b)
		}
	}
	if !strings.Contains(string(m["gaps"]), "unrecorded_risk_class") {
		t.Fatalf("gaps=%s", m["gaps"])
	}
}

// PG: upsert keeps one current row per (task, surface, risk_area) and appends history;
// the loaded ledger reads goal vulnclass and the budget end marker.
func TestRecordCoverageUpsertHistory(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("coverage ledger", "g", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	as := d.Assets()
	store := d.Exploration(task.ExplorationID)
	for _, vc := range []string{"SQLi", "XSS"} {
		if _, err := store.AddGoal(map[string]any{"text": "g " + vc, "vulnclass": vc}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []CoverageEntry{
		{Surface: "/q", RiskArea: "SQLi", Outcome: OutcomeNeedsFollowUp},
		{Surface: "/q", RiskArea: "sqli", Outcome: OutcomeNoIssueFound, Evidence: "fact 7"},
	} {
		if err := as.RecordCoverage(task.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := as.RecordCoverage(task.ID, CoverageEntry{Surface: "/q", RiskArea: "x", Outcome: "clean"}); err == nil {
		t.Fatal("invalid outcome accepted")
	}
	var current, history int
	_ = d.QueryRow(`SELECT count(*) FROM coverage_ledger WHERE task_id=$1`, task.ID).Scan(&current)
	_ = d.QueryRow(`SELECT count(*) FROM coverage_ledger_history WHERE task_id=$1`, task.ID).Scan(&history)
	if current != 1 || history != 2 {
		t.Fatalf("current=%d history=%d, want 1 and 2", current, history)
	}
	l, err := as.CoverageLedger(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Outcome != OutcomeNoIssueFound || len(l.Gaps) != 1 || l.Gaps[0].String() != "unrecorded_risk_class xss" {
		t.Fatalf("ledger=%+v", l)
	}
	if l.Completeness.State != "in_progress" {
		t.Fatalf("completeness=%+v", l.Completeness)
	}
	if _, err := store.AppendActivity(Activity{Worker: "system", Kind: "text", Metadata: json.RawMessage(`{"end_reason":"budget"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if l, _ = as.CoverageLedger(task.ID); l.Completeness.Complete || l.Completeness.State != "budget" {
		t.Fatalf("completeness after budget=%+v", l.Completeness)
	}
}
