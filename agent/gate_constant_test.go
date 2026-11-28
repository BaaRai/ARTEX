package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestGateConstantDetectionAndIntentGuard(t *testing.T) {
	store := testDB(t).Exploration(mustCreateExploration(t))

	// Open an existing hypothesis matching the target route so we verify it closes.
	tsx := NewToolSet(store, "planner")
	hRes, err := tsx.upsertHypothesis().Call(t.Context(), json.RawMessage(`{"hypothesis":"Forge X-Estate-Peer values on /api/v1/upload","direction":"sweep X-Estate-Peer on https://edge.lab.local/api/v1/upload"}`), nil)
	if err != nil || hRes.IsError {
		t.Fatalf("upsertHypothesis: %v %s", err, hRes.Flatten())
	}
	var hOut struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(hRes.Flatten()), &hOut)

	// 1. Single failure must NOT produce a gate_constant fact.
	det := newGateConstantDetector(GateConstantHooks{})
	det.onToolUse("p1", "Bash", `{"command":"curl -si -H 'X-Estate-Peer: peer-1' https://edge.lab.local/api/v1/upload"}`)
	if rec, sweep := det.onToolResult("p1", "HTTP/1.1 403 Forbidden\r\n\r\n{\"error\":\"forbidden\"}", false); rec != nil || sweep {
		t.Fatalf("single 403 must not emit gate_constant, got rec=%+v sweep=%v", rec, sweep)
	}

	// 2. Five distinct Bash curl -i probes returning identical 403 produce gate_constant and close hypothesis.
	var emitted *GateConstantRecord
	for i := 2; i <= 5; i++ {
		id := "p" + string(rune('0'+i))
		det.onToolUse(id, "Bash", `{"command":"curl -si -H 'X-Estate-Peer: peer-`+id+`' https://edge.lab.local/api/v1/upload"}`)
		rec, sweep := det.onToolResult(id, "<<<UNTRUSTED_OUTPUT "+id+">>>>\nHTTP/1.1 403 Forbidden\r\nContent-Type: application/json\r\n\r\n{\"error\":\"forbidden\"}\n<<<END_UNTRUSTED_OUTPUT "+id+">>>>", false)
		if sweep {
			t.Fatalf("unexpected sweep error at probe %d", i)
		}
		if i < 5 && rec != nil {
			t.Fatalf("unexpected early gate_constant at probe %d", i)
		}
		if i == 5 {
			emitted = rec
		}
	}
	if emitted == nil || emitted.SampleCount != 5 || emitted.Status != 403 || emitted.Host != "edge.lab.local" || emitted.Path != "/api/v1/upload" {
		t.Fatalf("expected 5-sample 403 Bash gate_constant record, got %+v", emitted)
	}

	factID, err := RecordGateConstantFact(store, 0, *emitted)
	if err != nil || factID <= 0 {
		t.Fatalf("RecordGateConstantFact: id=%d err=%v", factID, err)
	}

	// Verify hypothesis was automatically closed citing factID.
	_, closedHyps := tsx.hypothesisOverview()
	if len(closedHyps) != 1 || closedHyps[0]["id"] != hOut.ID || !strings.Contains(closedHyps[0]["close_reason"].(string), "gate_constant") {
		t.Fatalf("expected hypothesis #%d closed citing gate_constant, got %+v", hOut.ID, closedHyps)
	}

	// 3. Strict mode rejects sweep on gated route, rejects sweep without hypothesis_id, allows non-sweep without hypothesis_id, allows different route.
	SetGateConstantStrictMode(true)
	t.Cleanup(ResetGateConstantStrictMode)

	openHypRes, err := tsx.upsertHypothesis().Call(t.Context(), json.RawMessage(`{"hypothesis":"Alternative path hypothesis"}`), nil)
	if err != nil || openHypRes.IsError {
		t.Fatalf("open hypothesis failed: %v %s", err, openHypRes.Flatten())
	}
	var openHyp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(openHypRes.Flatten()), &openHyp)

	badSweep := json.RawMessage(`{"summary":"Sweep more X-Estate-Peer candidate values against https://edge.lab.local/api/v1/upload","hypothesis_id":` + itoa(openHyp.ID) + `}`)
	res, err := tsx.addIntent().Call(t.Context(), badSweep, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "gate_constant") {
		t.Fatalf("expected strict rejection for sweep on gated route, got err=%v res=%s", err, res.Flatten())
	}

	// Non-sweep intent in strict mode WITHOUT hypothesis_id MUST be allowed.
	nonSweep := json.RawMessage(`{"summary":"Inspect https://edge.lab.local/api/v1/contacts endpoint documentation"}`)
	res, err = tsx.addIntent().Call(t.Context(), nonSweep, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected non-sweep intent without hypothesis_id allowed in strict mode, got err=%v res=%s", err, res.Flatten())
	}

	// Sweep intent WITHOUT hypothesis_id in strict mode MUST be rejected.
	sweepNoHyp := json.RawMessage(`{"summary":"Brute-force candidate tokens on https://edge.lab.local/api/v1/other"}`)
	res, err = tsx.addIntent().Call(t.Context(), sweepNoHyp, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "hypothesis_id") {
		t.Fatalf("expected strict mode to require hypothesis_id on sweep intent, got err=%v res=%s", err, res.Flatten())
	}

	// Reopen with reopen_reason + new_evidence allows gated route.
	reopenIntent := json.RawMessage(`{"summary":"Sweep X-Estate-Peer values on https://edge.lab.local/api/v1/upload","hypothesis_id":` + itoa(openHyp.ID) + `,"reopen_reason":"Header format changed in v2 docs","new_evidence":"Found enrolment prefix in JS bundle"}`)
	res, err = tsx.addIntent().Call(t.Context(), reopenIntent, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected reopen_reason+new_evidence to allow intent, got err=%v res=%s", err, res.Flatten())
	}

	// 4. Shadow mode flags rather than rejects.
	SetGateConstantStrictMode(false)
	shadowRes, err := tsx.addIntent().Call(t.Context(), badSweep, nil)
	if err != nil || shadowRes.IsError {
		t.Fatalf("expected shadow mode to flag rather than reject, got err=%v res=%s", err, shadowRes.Flatten())
	}
	var createdID int64
	_ = json.Unmarshal([]byte(strings.TrimPrefix(shadowRes.Flatten(), "intent created: ")), &createdID)
	node, _ := store.GetNode(createdID)
	if node == nil || !strings.Contains(string(node.Payload), "gate_constant_shadow_warning") {
		t.Fatalf("expected gate_constant_shadow_warning on intent payload, got %s", string(node.Payload))
	}

	// 5. Higher threshold (8) triggers ErrSweepLoopDetected.
	for i := 6; i <= 8; i++ {
		id := "p" + itoa(int64(i))
		det.onToolUse(id, "Bash", `{"command":"curl -si -H 'X-Estate-Peer: peer-`+id+`' https://edge.lab.local/api/v1/upload"}`)
		_, sweep := det.onToolResult(id, "HTTP/1.1 403 Forbidden\r\nContent-Type: application/json\r\n\r\n{\"error\":\"forbidden\"}", false)
		if i < 8 && sweep {
			t.Fatalf("did not expect sweep loop before 8, hit at %d", i)
		}
		if i == 8 && !sweep {
			t.Fatalf("expected sweep loop at 8 probes")
		}
	}
}

func TestGateConstantExcludesTransient429And5xxAndErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		output   string
		isError  bool
		toolName string
	}{
		{
			name:     "429_rate_limit_burst_never_records_gate_constant",
			toolName: "Bash",
			output:   "HTTP/1.1 429 Too Many Requests\r\n\r\n{\"error\":\"slow down\"}",
		},
		{
			name:     "502_5xx_burst_never_records_gate_constant",
			toolName: "custom_http",
			output:   `{"status":502,"body":"bad gateway"}`,
		},
		{
			name:     "timeout_error_burst_never_records_gate_constant",
			toolName: "Bash",
			output:   "curl: (28) Operation timed out after 5000 milliseconds",
			isError:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			det := newGateConstantDetector(GateConstantHooks{})
			for i := 1; i <= 8; i++ {
				id := "x" + itoa(int64(i))
				if tc.toolName == "Bash" {
					det.onToolUse(id, "Bash", `{"command":"curl -si -H 'X-Token: t`+id+`' https://edge.lab.local/api/v1/check"}`)
				} else {
					det.onToolUse(id, tc.toolName, `{"url":"https://edge.lab.local/api/v1/check","tok":"`+id+`"}`)
				}
				rec, sweep := det.onToolResult(id, tc.output, tc.isError)
				if rec != nil || sweep {
					t.Fatalf("transient/error burst %q produced rec=%+v sweep=%v", tc.name, rec, sweep)
				}
			}
		})
	}
}

func TestGateConstantCTFGoalAutoEnablesStrictWithoutEnv(t *testing.T) {
	ResetGateConstantStrictMode()
	t.Setenv("ARTEX_CTF_STRICT", "")
	t.Setenv("ARTEX_GATE_CONSTANT_STRICT", "")

	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })

	ctfTask, err := d.CreateTaskWithOptions("Gablewrick CTF", "Capture 4 flags in order on https://edge.lab.local", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(ctfTask.ID) })
	ctfStore := d.Exploration(ctfTask.ExplorationID)
	ctfTS := NewToolSet(ctfStore, "planner")
	if !ctfTS.isGateConstantStrict() {
		t.Fatalf("expected CTF goal task to auto-enable strict gate_constant mode")
	}
	// Sweep without hypothesis_id rejected; non-sweep allowed.
	res, err := ctfTS.addIntent().Call(t.Context(), json.RawMessage(`{"summary":"Sweep X-Estate-Peer values on https://edge.lab.local/api/v1/upload"}`), nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "hypothesis_id") {
		t.Fatalf("expected CTF auto-strict rejection for sweep without hypothesis_id, got err=%v res=%s", err, res.Flatten())
	}
	res, err = ctfTS.addIntent().Call(t.Context(), json.RawMessage(`{"summary":"Inspect https://edge.lab.local/docs"}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("expected non-sweep allowed in CTF auto-strict mode, got err=%v res=%s", err, res.Flatten())
	}

	normalTask, err := d.CreateTaskWithOptions("Standard audit", "Audit https://corp.example.test", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(normalTask.ID) })
	normalTS := NewToolSet(d.Exploration(normalTask.ExplorationID), "planner")
	if normalTS.isGateConstantStrict() {
		t.Fatalf("expected normal non-CTF task to stay in shadow mode by default")
	}
	normalTS.SetGateConstantStrict(true)
	if !normalTS.isGateConstantStrict() {
		t.Fatalf("expected per-task SetGateConstantStrict(true) to enable strict mode")
	}
}

func mustCreateExploration(t *testing.T) int64 {
	t.Helper()
	d := testDB(t)
	id, err := d.CreateExploration("gate-constant-test", "test constant gate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, id)
		_ = d.Close()
	})
	return id
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
