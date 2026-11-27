package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestFindingFingerprintDeterminismAndDistinctness(t *testing.T) {
	base := db.RecordFindingInput{
		VulnClass: "SQLi",
		Name:      "SQL Injection on search",
		Summary:   "GET https://app.example.test/api/v1/products/search?q=1&sort=asc is injectable",
		AssetIDs:  []int64{42, 10},
	}
	sameReordered := db.RecordFindingInput{
		VulnClass: "sqli",
		Name:      "Re-verified SQLi",
		Summary:   "Confirmed again on https://APP.EXAMPLE.TEST/api/v1/products/search/?sort=desc&q=2",
		AssetIDs:  []int64{10, 42, 10},
	}
	fp1 := FindingFingerprint(base)
	fp2 := FindingFingerprint(sameReordered)
	if fp1 == "" || fp1 != fp2 {
		t.Fatalf("equivalent findings must yield identical non-empty fingerprint: fp1=%q fp2=%q", fp1, fp2)
	}

	// Distinct endpoint on same host/asset must produce a different fingerprint.
	diffEndpoint := base
	diffEndpoint.Summary = "GET https://app.example.test/api/v1/users/search?q=1 is injectable"
	if fp3 := FindingFingerprint(diffEndpoint); fp3 == "" || fp3 == fp1 {
		t.Fatalf("different endpoint must yield different fingerprint: fp1=%q fp3=%q", fp1, fp3)
	}

	// Distinct parameter on same path must produce a different fingerprint.
	diffParam := base
	diffParam.Summary = "GET https://app.example.test/api/v1/products/search?category=1 is injectable"
	if fp4 := FindingFingerprint(diffParam); fp4 == "" || fp4 == fp1 {
		t.Fatalf("different parameter must yield different fingerprint: fp1=%q fp4=%q", fp1, fp4)
	}

	// Distinct vulnerability class on same endpoint must produce a different fingerprint.
	diffClass := base
	diffClass.VulnClass = "XSS"
	if fp5 := FindingFingerprint(diffClass); fp5 == "" || fp5 == fp1 {
		t.Fatalf("different vulnclass must yield different fingerprint: fp1=%q fp5=%q", fp1, fp5)
	}

	// Missing endpoint or missing asset/host returns "" (no false collision).
	noEndpoint := db.RecordFindingInput{VulnClass: "SQLi", Summary: "generic database error", AssetIDs: []int64{10}}
	if fp := FindingFingerprint(noEndpoint); fp != "" {
		t.Fatalf("missing endpoint must return empty fingerprint, got %q", fp)
	}
	noAssetOrHost := db.RecordFindingInput{VulnClass: "SQLi", Summary: "GET /login?user=a is injectable"}
	if fp := FindingFingerprint(noAssetOrHost); fp != "" {
		t.Fatalf("missing asset/host must return empty fingerprint, got %q", fp)
	}
}

type countingTriageClient struct {
	calls atomic.Int64
	ans   DuplicateAnswer
}

func (c *countingTriageClient) Duplicate(_ context.Context, _ TriageCandidate, _ []TriageCandidate) (DuplicateAnswer, error) {
	c.calls.Add(1)
	return c.ans, nil
}

func TestReportFindingFingerprintDedupWithoutTriageClientAndComplementsTriage(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("fingerprint dedup", "verify deterministic dedup", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.DeleteFindingsByTask(task.ID)
		_ = d.DeleteTask(task.ID)
	})

	ts := NewToolSet(d.Exploration(task.ExplorationID), "worker-1")
	ts.SetTaskID(task.ID)

	// 1. First registration with URL host + vulnclass + endpoint succeeds.
	first := `{"vulnclass":"SQLi","severity":"high","name":"Login SQLi","summary":"POST https://target.lab.local/api/login?user=admin vulnerable to boolean SQLi"}`
	if res := callGuardTool(t, ts.addFinding(), first); res.IsError {
		t.Fatalf("first report_finding failed: %s", res.Flatten())
	}
	nodes := findingNodes(t, ts)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 finding after first report, got %d", len(nodes))
	}
	origFID, origNID := nodes[0].FindingID, nodes[0].ID

	// 2. Re-verification with reworded summary on the same host+endpoint+param+class
	// is suppressed deterministically even with NO triage client configured.
	triageMock := &countingTriageClient{}
	ts.SetTriageClient(triageMock)
	dup := `{"vulnclass":"sqli","severity":"high","name":"Re-verified login SQLi","summary":"Re-confirmed SQL injection on https://TARGET.LAB.LOCAL/api/login/?user=test"}`
	res := callGuardTool(t, ts.addFinding(), dup)
	if res.IsError {
		t.Fatalf("duplicate suppression returned error: %s", res.Flatten())
	}
	text := res.Flatten()
	if !strings.Contains(text, "duplicate_not_registered") || !strings.Contains(text, `"dedup_source":"fingerprint"`) {
		t.Fatalf("expected deterministic fingerprint suppression, got: %s", text)
	}
	if triageMock.calls.Load() != 0 {
		t.Fatalf("fingerprint hit must short-circuit before LLM triage, got %d triage calls", triageMock.calls.Load())
	}
	var ans struct {
		DuplicateOfFindingID int64  `json:"duplicate_of_finding_id"`
		DuplicateOfNodeID    int64  `json:"duplicate_of_finding_node_id"`
		Fingerprint          string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(text[strings.Index(text, "{"):]), &ans); err != nil {
		t.Fatalf("parse suppression json: %v (%s)", err, text)
	}
	if ans.DuplicateOfFindingID != origFID || ans.DuplicateOfNodeID != origNID || !strings.HasPrefix(ans.Fingerprint, "fp:") {
		t.Fatalf("unexpected suppression payload: %+v (want finding=%d node=%d)", ans, origFID, origNID)
	}
	if !strings.Contains(text, "finding_id="+strconv.FormatInt(origNID, 10)) {
		t.Fatalf("suppression note must reference node id %d: %s", origNID, text)
	}
	if got := len(findingNodes(t, ts)); got != 1 {
		t.Fatalf("expected still 1 finding after suppression, got %d", got)
	}

	// 3. A distinct endpoint on the same host is NOT suppressed by fingerprint and
	// falls through to LLM triage (which returns distinct -> registers).
	distinct := `{"vulnclass":"sqli","severity":"high","name":"Search SQLi","summary":"GET https://target.lab.local/api/search?q=abc vulnerable to UNION SQLi"}`
	if res := callGuardTool(t, ts.addFinding(), distinct); res.IsError || strings.Contains(res.Flatten(), "duplicate_not_registered") {
		t.Fatalf("distinct endpoint should register, got: %s", res.Flatten())
	}
	if triageMock.calls.Load() != 1 {
		t.Fatalf("non-matching fingerprint must fall through to LLM triage, got %d calls", triageMock.calls.Load())
	}
	if got := len(findingNodes(t, ts)); got != 2 {
		t.Fatalf("expected 2 findings after distinct endpoint, got %d", got)
	}

	// 4. force_register=true overrides deterministic fingerprint suppression.
	forced := `{"force_register":true,"vulnclass":"sqli","severity":"high","name":"Distinct root cause on login","summary":"POST https://target.lab.local/api/login?user=admin second bug"}`
	if res := callGuardTool(t, ts.addFinding(), forced); res.IsError || strings.Contains(res.Flatten(), "duplicate_not_registered") {
		t.Fatalf("force_register must override fingerprint suppression, got: %s", res.Flatten())
	}
	if got := len(findingNodes(t, ts)); got != 3 {
		t.Fatalf("expected 3 findings after force_register, got %d", got)
	}

	// 5. Dismissed (false_positive) findings must not suppress a new registration.
	if _, err := d.Exec(`UPDATE findings SET status='false_positive' WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	ts.SetTriageClient(nil)
	if res := callGuardTool(t, ts.addFinding(), dup); res.IsError || strings.Contains(res.Flatten(), "duplicate_not_registered") {
		t.Fatalf("false_positive finding must not suppress re-discovery, got: %s", res.Flatten())
	}
	if got := len(findingNodes(t, ts)); got != 4 {
		t.Fatalf("expected 4 findings after false_positive re-discovery, got %d", got)
	}
}
