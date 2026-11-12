package report

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// The exported report carries the coverage gaps and the completeness caveat;
// nothing is added when the task's coverage feature is off (nil ledger).
func TestMarkdownCoverageGaps(t *testing.T) {
	l := db.BuildCoverageLedger([]db.CoverageEntry{{Surface: "/a", RiskArea: "xss", Outcome: db.OutcomeNeedsFollowUp}},
		[]string{"sqli"}, db.CoverageCompletenessFor("timeout", false, false, true))
	cases := []struct {
		name string
		l    *db.CoverageLedger
		want []string
	}{
		{"ledger", l, []string{"## Coverage gaps", "timeout", "Incomplete scan", "open gaps: 2", "- unrecorded_risk_class sqli", "- needs_follow_up /a xss"}},
		{"coverage off", nil, nil},
	}
	for _, c := range cases {
		md := Markdown(Input{Title: "t", Coverage: c.l})
		if c.want == nil && strings.Contains(md, "Coverage gaps") {
			t.Fatalf("%s: unexpected coverage section", c.name)
		}
		for _, w := range c.want {
			if !strings.Contains(md, w) {
				t.Errorf("%s: missing %q in\n%s", c.name, w, md)
			}
		}
	}
}

// The report lists at most reportMaxGaps gaps, then a count line.
func TestMarkdownCoverageGapsCap(t *testing.T) {
	var entries []db.CoverageEntry
	for i := 0; i < reportMaxGaps+30; i++ {
		entries = append(entries, db.CoverageEntry{Surface: "/p", RiskArea: "xss", Outcome: db.OutcomeNeedsFollowUp})
	}
	md := Markdown(Input{Title: "t", Coverage: db.BuildCoverageLedger(entries, nil, db.CoverageCompleteness{})})
	if n := strings.Count(md, "\n- needs_follow_up /p xss"); n != reportMaxGaps {
		t.Fatalf("listed %d gaps, want %d", n, reportMaxGaps)
	}
	if !strings.Contains(md, "- and 30 more (see the coverage API)") {
		t.Fatalf("missing count line in\n%s", md)
	}
}
