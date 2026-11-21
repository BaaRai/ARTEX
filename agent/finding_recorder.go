package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
)

// FindingRecorder is injected by the host; agents never synthesize or copy
// evidence bodies themselves. Its implementation owns the atomic write.
type FindingRecorder interface {
	Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

// TrafficCaptureChecker is implemented by a FindingRecorder that owns the live
// traffic capture (the host's evidence store). Plan() hands it to prove_goal so
// a finding's bound traffic_ref must still exist in that capture. Without it,
// finding proofs are refused (#124, P4.3).
type TrafficCaptureChecker interface {
	TrafficExists(ctx context.Context, trafficID string) (bool, error)
}

// Tool-use guidance is appended without replacing the user's editable prompt.
// It does not require capture or claim that unavailable traffic tools exist.
const findingTrafficGuidance = "\n\n**Vulnerability traffic evidence**: when reporting a vulnerability via report_finding, bind real HTTP request/response IDs via traffic_refs in reproduction order; domains and times are only candidate filters and do not imply association. A finding without bound traffic stays recorded and visible, but cannot close a goal via prove_goal until verified traffic matching its asset host is bound (via report_finding or bind_finding_traffic). For TCP and other non-HTTP vulnerabilities, when nothing was captured, or when there is no exact match, omit traffic_refs or pass [], keep verifiable command output and logs in evidence, and close non-HTTP goals with an observed tool_output fact. Do not guess IDs, and do not re-probe just to capture packets."

func (t *ToolSet) SetFindingRecorder(r FindingRecorder)   { t.findingRecorder = r }
func (w *Worker) SetFindingRecorder(r FindingRecorder)    { w.findingRecorder = r }
func (p *Planner) SetFindingRecorder(r FindingRecorder)   { p.findingRecorder = r }
func (m *MainAgent) SetFindingRecorder(r FindingRecorder) { m.findingRecorder = r }
