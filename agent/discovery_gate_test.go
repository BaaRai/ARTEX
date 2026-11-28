package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestDiscoveryQualityGateTableDriven(t *testing.T) {
	// 1. Curated wordlist exists in skills/api-recon/wordlists/api.txt and NO fake skills/wordlists skill exists.
	if _, err := os.Stat(filepath.Join("..", "skills", "wordlists")); !os.IsNotExist(err) {
		t.Fatalf("unexpected fake skills/wordlists directory; expected wordlist under skills/api-recon/wordlists/api.txt (err=%v)", err)
	}
	wlPath := filepath.Join("..", "skills", "api-recon", "wordlists", "api.txt")
	rawWL, err := os.ReadFile(wlPath)
	if err != nil {
		t.Fatalf("expected curated wordlist at %s: %v", wlPath, err)
	}
	var routes int
	for _, line := range strings.Split(string(rawWL), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			routes++
		}
	}
	if routes < 50 {
		t.Fatalf("expected >= 50 curated API routes in %s, got %d", wlPath, routes)
	}

	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("discovery-gate", "Map all API routes", db.TaskCreateOptions{})
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

	// Normal facts, normal hypotheses, non-route coverage, and captured flags MUST NOT require discovery_artifact.
	normalFact := json.RawMessage(`{"facts":[{"summary":"POST /api/v1/certificates/upload returns flag in proof","detail":"verified with tenant B cert ID","evidence":"HTTP 200 proof=flag1","confidence":"observed","source":"tool_output"}]}`)
	if res, err := workerTS.recordFact().Call(t.Context(), normalFact, nil); err != nil || res.IsError {
		t.Fatalf("expected normal record_fact to succeed without discovery_artifact, got err=%v res=%s", err, res.Flatten())
	}
	normalHyp := json.RawMessage(`{"hypothesis":"Check SSRF on webhook URL","status":"closed","close_reason":"Server rejects private CIDR ranges"}`)
	if res, err := plannerTS.upsertHypothesis().Call(t.Context(), normalHyp, nil); err != nil || res.IsError {
		t.Fatalf("expected normal upsert_hypothesis to succeed without discovery_artifact, got err=%v res=%s", err, res.Flatten())
	}
	normalCov := json.RawMessage(`{"surface":"https://api.lab.local/api/v1/contacts","risk_area":"idor","outcome":"no_issue_found","evidence":"cross-tenant GET returned 403"}`)
	if res, err := workerTS.recordCoverage().Call(t.Context(), normalCov, nil); err != nil || res.IsError {
		t.Fatalf("expected non-route record_coverage to succeed without discovery_artifact, got err=%v res=%s", err, res.Flatten())
	}
	if _, err := store.AddNode(db.KindFinding, map[string]any{
		"summary":    "Captured flag 2",
		"evidence":   "HTTP 200 flag2",
		"source":     "tool_output",
		"confidence": "observed",
		"verified":   true,
	}, 0, "confirmed", "worker#1", nil); err != nil {
		t.Fatal(err)
	}
	flagGoalMet := json.RawMessage(`{"reason":"Captured flag CTF{gablewrick_flag_2} from /api/v1/certificates/upload"}`)
	if res, err := plannerTS.goalMet().Call(t.Context(), flagGoalMet, nil); err != nil || res.IsError {
		t.Fatalf("expected goal_met for captured flag to succeed without discovery_artifact, got err=%v res=%s", err, res.Flatten())
	}

	// 2. Shallow exhaustion claims MUST be rejected before complete coverage exists.
	shallowHyp := json.RawMessage(`{"hypothesis":"Discover API routes","status":"closed","close_reason":"route space exhausted after 404 on 10 root nouns"}`)
	res, err := plannerTS.upsertHypothesis().Call(t.Context(), shallowHyp, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "discovery_artifact") {
		t.Fatalf("expected upsert_hypothesis exhaustion rejected without coverage, got err=%v res=%s", err, res.Flatten())
	}

	shallowFact := json.RawMessage(`{"facts":[{"summary":"Route space exhausted: all 20 root nouns returned 404","detail":"tested 20 root paths","evidence":"curl 404","confidence":"observed","source":"tool_output"}]}`)
	res, err = workerTS.recordFact().Call(t.Context(), shallowFact, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "discovery_artifact") {
		t.Fatalf("expected record_fact exhaustion rejected without coverage, got err=%v res=%s", err, res.Flatten())
	}

	exhaustGoalMet := json.RawMessage(`{"reason":"Route space exhausted on target; no more API routes exist"}`)
	res, err = plannerTS.goalMet().Call(t.Context(), exhaustGoalMet, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "discovery_artifact") {
		t.Fatalf("expected goal_met route-exhaustion claim rejected without coverage, got err=%v res=%s", err, res.Flatten())
	}

	partialCov := json.RawMessage(`{
		"surface": "https://api.lab.local/",
		"risk_area": "route_discovery",
		"outcome": "no_issue_found",
		"evidence": "ran partial wordlist only",
		"discovery_artifact": {
			"wordlist_run": {"wordlist": "skills/api-recon/wordlists/api.txt", "entries_tested": 120},
			"nested_paths": []
		}
	}`)
	res, err = workerTS.recordCoverage().Call(t.Context(), partialCov, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "(b)") {
		t.Fatalf("expected partial record_coverage rejected citing missing checks, got err=%v res=%s", err, res.Flatten())
	}

	// 3. Complete 5-part record_coverage artifact succeeds and unlocks exhaustion claim.
	fullCov := json.RawMessage(`{
		"surface": "https://api.lab.local/",
		"risk_area": "route_discovery",
		"outcome": "no_issue_found",
		"evidence": "full 5-part discovery complete",
		"discovery_artifact": {
			"wordlist_run": {"wordlist": "skills/api-recon/wordlists/api.txt", "entries_tested": 120, "discovered": ["/api/v1/users"]},
			"nested_paths": ["/api/v1/users/profile", "/api/v1/users/keys"],
			"parameters_tested": ["id", "role", "page"],
			"method_matrix": ["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"],
			"static_docs_js_reviewed": ["/docs", "/static/app.js"]
		}
	}`)
	res, err = workerTS.recordCoverage().Call(t.Context(), fullCov, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected complete record_coverage accepted, got err=%v res=%s", err, res.Flatten())
	}

	// Now closing hypothesis as exhausted succeeds.
	res, err = plannerTS.upsertHypothesis().Call(t.Context(), shallowHyp, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected exhaustion claim accepted after full discovery_artifact, got err=%v res=%s", err, res.Flatten())
	}
	res, err = plannerTS.goalMet().Call(t.Context(), exhaustGoalMet, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected exhaustion goal_met accepted after full discovery_artifact, got err=%v res=%s", err, res.Flatten())
	}
}
