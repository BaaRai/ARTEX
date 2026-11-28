package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestDiscoveryCoverageCrossHostAndMinimumWordlist(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("discovery-scope", "Enumerate routes on https://main.lab.local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	store := d.Exploration(task.ExplorationID)
	as := d.Assets()
	workerTS := NewToolSet(store, "worker#1")
	workerTS.SetTaskID(task.ID)
	workerTS.SetAssetStore(as, as.Companies())
	plannerTS := NewToolSet(store, "planner")
	plannerTS.SetTaskID(task.ID)
	plannerTS.SetAssetStore(as, as.Companies())

	// 1. A tiny 1-entry wordlist run CANNOT satisfy criterion (a).
	tinyCov := json.RawMessage(`{
		"surface": "https://main.lab.local/",
		"risk_area": "route_discovery",
		"outcome": "no_issue_found",
		"evidence": "tested 1 path",
		"discovery_artifact": {
			"wordlist_run": {"wordlist": "skills/api-recon/wordlists/api.txt", "entries_tested": 1},
			"nested_paths": ["/api/v1"],
			"parameters_tested": ["id"],
			"method_matrix": ["GET", "POST"],
			"static_docs_js_reviewed": ["/docs"]
		}
	}`)
	res, err := workerTS.recordCoverage().Call(t.Context(), tinyCov, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "(a)") {
		t.Fatalf("expected tiny 1-entry wordlist run rejected for (a), got err=%v res=%s", err, res.Flatten())
	}

	// 2. Complete coverage on main.lab.local unlocks exhaustion on main.lab.local ONLY.
	mainCov := json.RawMessage(`{
		"surface": "https://main.lab.local/",
		"risk_area": "route_discovery",
		"outcome": "no_issue_found",
		"evidence": "full 5-part discovery on main",
		"discovery_artifact": {
			"wordlist_run": {"wordlist": "skills/api-recon/wordlists/api.txt", "entries_tested": 126, "discovered": ["/api/v1/users"]},
			"nested_paths": ["/api/v1/users/profile"],
			"parameters_tested": ["id", "role"],
			"method_matrix": ["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
			"static_docs_js_reviewed": ["/docs", "/static/app.js"]
		}
	}`)
	if res, err := workerTS.recordCoverage().Call(t.Context(), mainCov, nil); err != nil || res.IsError {
		t.Fatalf("expected complete main.lab.local discovery coverage accepted, got err=%v res=%s", err, res.Flatten())
	}

	// Cross-host exhaustion claim for estate.lab.local MUST STILL be rejected!
	estateHyp := json.RawMessage(`{"hypothesis":"Discover routes on https://estate.lab.local/","status":"closed","close_reason":"route space exhausted on estate.lab.local; no more API routes"}`)
	res, err = plannerTS.upsertHypothesis().Call(t.Context(), estateHyp, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "estate.lab.local") {
		t.Fatalf("expected main.lab.local coverage NOT to unlock estate.lab.local hypothesis close, got err=%v res=%s", err, res.Flatten())
	}

	estateFact := json.RawMessage(`{"facts":[{"summary":"Route space exhausted on https://estate.lab.local/: all 404","detail":"probed estate.lab.local","evidence":"404","confidence":"observed","source":"tool_output"}]}`)
	res, err = workerTS.recordFact().Call(t.Context(), estateFact, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "estate.lab.local") {
		t.Fatalf("expected main.lab.local coverage NOT to unlock estate.lab.local record_fact exhaustion, got err=%v res=%s", err, res.Flatten())
	}

	estateGoalMet := json.RawMessage(`{"reason":"Route space exhausted on estate.lab.local"}`)
	res, err = plannerTS.goalMet().Call(t.Context(), estateGoalMet, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "estate.lab.local") {
		t.Fatalf("expected main.lab.local coverage NOT to unlock estate.lab.local goal_met, got err=%v res=%s", err, res.Flatten())
	}

	// Same-host exhaustion claim on main.lab.local succeeds.
	mainHyp := json.RawMessage(`{"hypothesis":"Discover routes on https://main.lab.local/","status":"closed","close_reason":"route space exhausted on main.lab.local"}`)
	if res, err := plannerTS.upsertHypothesis().Call(t.Context(), mainHyp, nil); err != nil || res.IsError {
		t.Fatalf("expected same-host main.lab.local exhaustion accepted, got err=%v res=%s", err, res.Flatten())
	}
}
