package db

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Coverage ledger (modeled on Strix strix/report/coverage.py, skill_coverage_gaps).
// Two layers: agent_reported = the entries workers write with record_coverage;
// machine_observed = execution facts (asset coverage from TaskCoverageWithSources,
// findings). The ledger adds the risk-class dimension and the negative results; it
// never recomputes tested assets. A risk class with no entry is unexamined, never
// clean, and a no_issue_found is never derived from the absence of a finding.

// Ledger outcomes.
const (
	OutcomeReported      = "reported"
	OutcomeNoIssueFound  = "no_issue_found"
	OutcomeRuledOut      = "ruled_out"
	OutcomeNotApplicable = "not_applicable"
	OutcomeNeedsFollowUp = "needs_follow_up"
)

// Gap kinds.
const (
	GapNeedsFollowUp       = "needs_follow_up"
	GapUnrecordedRiskClass = "unrecorded_risk_class"
	GapMissingEvidence     = "missing_evidence"
	GapMissingReason       = "missing_reason"
)

// EndReasonBudget is the activity metadata end_reason of a task stopped by the
// run-budget wrap-up (server/engine_timeout.go); completeness reads it back.
const EndReasonBudget = "budget"

// ValidCoverageOutcome reports whether o is one of the five ledger outcomes.
func ValidCoverageOutcome(o string) bool {
	switch o {
	case OutcomeReported, OutcomeNoIssueFound, OutcomeRuledOut, OutcomeNotApplicable, OutcomeNeedsFollowUp:
		return true
	}
	return false
}

// CoverageEntry is one agent_reported row: surface + risk_area + outcome + evidence + reason.
type CoverageEntry struct {
	Surface   string    `json:"surface"`
	RiskArea  string    `json:"risk_area"`
	Outcome   string    `json:"outcome"`
	Evidence  string    `json:"evidence"`
	Reason    string    `json:"reason"`
	Worker    string    `json:"worker,omitempty"`
	IntentID  int64     `json:"intent_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CoverageGap is one open item: a follow-up, an unrecorded mandatory class, or an
// entry missing its required evidence/reason.
type CoverageGap struct {
	Kind     string `json:"kind"`
	Surface  string `json:"surface,omitempty"`
	RiskArea string `json:"risk_area"`
}

func (g CoverageGap) String() string {
	if g.Surface == "" {
		return g.Kind + " " + g.RiskArea
	}
	return g.Kind + " " + g.Surface + " " + g.RiskArea
}

// CoverageCompleteness says whether the run ended as a complete scan. Caveat is
// always set: an incomplete run is never reported silently.
type CoverageCompleteness struct {
	Complete bool   `json:"complete"`
	State    string `json:"state"` // complete | in_progress | paused | timeout | budget | aborted
	Caveat   string `json:"caveat"`
}

// CoverageLedger is the ledger document of one task.
type CoverageLedger struct {
	Entries         []CoverageEntry       `json:"entries"`
	Gaps            []CoverageGap         `json:"gaps"`
	Unexamined      []string              `json:"unexamined"`        // mandatory classes with no entry
	FindingsByClass map[string]int        `json:"findings_by_class"` // machine_observed
	Completeness    *CoverageCompleteness `json:"completeness"`
}

func normRiskArea(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// RecordCoverage upserts the current row of (task, surface, risk_area) and appends
// the write to the history, in one transaction.
func (s *AssetStore) RecordCoverage(taskID int64, e CoverageEntry) error {
	e.Surface, e.RiskArea = strings.TrimSpace(e.Surface), normRiskArea(e.RiskArea)
	if taskID <= 0 || e.Surface == "" || e.RiskArea == "" {
		return fmt.Errorf("task, surface and risk_area are required")
	}
	if !ValidCoverageOutcome(e.Outcome) {
		return fmt.Errorf("outcome must be one of reported, no_issue_found, ruled_out, not_applicable, needs_follow_up")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	args := []any{taskID, e.Surface, e.RiskArea, e.Outcome, strings.TrimSpace(e.Evidence), strings.TrimSpace(e.Reason), e.Worker, nullID(e.IntentID)}
	if _, err := tx.Exec(`INSERT INTO coverage_ledger_history(task_id, surface, risk_area, outcome, evidence, reason, worker, intent_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO coverage_ledger(task_id, surface, risk_area, outcome, evidence, reason, worker, intent_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (task_id, surface, risk_area) DO UPDATE SET outcome=EXCLUDED.outcome, evidence=EXCLUDED.evidence,
  reason=EXCLUDED.reason, worker=EXCLUDED.worker, intent_id=EXCLUDED.intent_id, updated_at=now()`, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func nullID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

// CoverageLedger loads the ledger document of a task: its entries, the mandatory
// classes (goal vulnclass), the findings per class and the task's end state.
func (s *AssetStore) CoverageLedger(taskID int64) (*CoverageLedger, error) {
	var entries []CoverageEntry
	rows, err := s.db.Query(`SELECT surface, risk_area, outcome, evidence, reason, worker, COALESCE(intent_id,0), updated_at
FROM coverage_ledger WHERE task_id=$1 ORDER BY risk_area, surface`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e CoverageEntry
		if err := rows.Scan(&e.Surface, &e.RiskArea, &e.Outcome, &e.Evidence, &e.Reason, &e.Worker, &e.IntentID, &e.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var classes []string
	if err := s.scanStrings(&classes, `SELECT DISTINCT lower(btrim(n.payload->>'vulnclass')) FROM exploration_nodes n
JOIN tasks t ON t.exploration_id=n.exploration_id
WHERE t.id=$1 AND n.kind='goal' AND COALESCE(btrim(n.payload->>'vulnclass'),'')<>'' ORDER BY 1`, taskID); err != nil {
		return nil, err
	}
	byClass := map[string]int{}
	frows, err := s.db.Query(`SELECT lower(btrim(vulnclass)), COUNT(*) FROM findings WHERE task_id=$1 AND btrim(vulnclass)<>'' GROUP BY 1`, taskID)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var c string
		var n int
		if err := frows.Scan(&c, &n); err != nil {
			return nil, err
		}
		byClass[c] = n
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}
	var status string
	var paused, budget, pastDeadline bool
	if err := s.db.QueryRow(`SELECT t.status, COALESCE(t.paused,false),
  EXISTS (SELECT 1 FROM activity a WHERE a.exploration_id=t.exploration_id AND a.worker='system' AND a.metadata->>'end_reason'=$2),
  COALESCE(t.deadline_at IS NOT NULL AND t.completed_at IS NOT NULL AND t.completed_at >= t.deadline_at, false)
FROM tasks t WHERE t.id=$1`, taskID, EndReasonBudget).Scan(&status, &paused, &budget, &pastDeadline); err != nil {
		return nil, err
	}
	l := BuildCoverageLedger(entries, classes, CoverageCompletenessFor(status, paused, budget, pastDeadline))
	l.FindingsByClass = byClass
	return l, nil
}

func (s *AssetStore) scanStrings(out *[]string, q string, args ...any) error {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return rows.Err()
}

// BuildCoverageLedger derives the gaps from the entries and the mandatory classes.
// Gap sources: (a) needs_follow_up entries; (b) a mandatory class with no entry at
// all (unrecorded_risk_class, also listed as unexamined); (c) an entry missing its
// required evidence (no_issue_found) or reason (ruled_out, not_applicable). Nothing
// here reads findings: an absent finding never closes a class.
func BuildCoverageLedger(entries []CoverageEntry, mandatory []string, c CoverageCompleteness) *CoverageLedger {
	l := &CoverageLedger{Entries: entries, Gaps: []CoverageGap{}, Unexamined: []string{}, FindingsByClass: map[string]int{}, Completeness: &c}
	if l.Entries == nil {
		l.Entries = []CoverageEntry{}
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[normRiskArea(e.RiskArea)] = true
		switch {
		case e.Outcome == OutcomeNeedsFollowUp:
			l.Gaps = append(l.Gaps, CoverageGap{GapNeedsFollowUp, e.Surface, e.RiskArea})
		case e.Outcome == OutcomeNoIssueFound && strings.TrimSpace(e.Evidence) == "":
			l.Gaps = append(l.Gaps, CoverageGap{GapMissingEvidence, e.Surface, e.RiskArea})
		case (e.Outcome == OutcomeRuledOut || e.Outcome == OutcomeNotApplicable) && strings.TrimSpace(e.Reason) == "":
			l.Gaps = append(l.Gaps, CoverageGap{GapMissingReason, e.Surface, e.RiskArea})
		}
	}
	classes := map[string]bool{}
	for _, m := range mandatory {
		if m = normRiskArea(m); m != "" && !seen[m] && !classes[m] {
			classes[m] = true
			l.Unexamined = append(l.Unexamined, m)
		}
	}
	sort.Strings(l.Unexamined)
	unrecorded := make([]CoverageGap, 0, len(l.Unexamined))
	for _, m := range l.Unexamined {
		unrecorded = append(unrecorded, CoverageGap{Kind: GapUnrecordedRiskClass, RiskArea: m})
	}
	l.Gaps = append(unrecorded, l.Gaps...) // the central invariant first
	return l
}

// CoverageCompletenessFor maps a task's end state to completeness. Only a task that
// reached done on its own is complete; timeout, budget wrap-up (even when the final
// round met the goals), pause, abort (failed) and a still-running task are not.
func CoverageCompletenessFor(status string, paused, budget, pastDeadline bool) CoverageCompleteness {
	state := "complete"
	switch {
	case budget:
		state = "budget"
	case status == "timeout" || (status == "done" && pastDeadline):
		state = "timeout"
	case status == "failed":
		state = "aborted"
	case paused || status == "paused":
		state = "paused"
	case status != "done":
		state = "in_progress"
	}
	if state == "complete" {
		return CoverageCompleteness{true, state, "Run ended normally. Classes listed as unexamined were not tested and are not clean."}
	}
	return CoverageCompleteness{false, state, "Incomplete scan (" + state + "): the absence of a finding does not mean the target is clean; unexamined classes and open gaps were not covered."}
}
