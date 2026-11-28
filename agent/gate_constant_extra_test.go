package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGateConstantBashWriteOutParserAndTransientZero(t *testing.T) {
	// Without -w context, "403" / "403:31" stay non-HTTP status=0; "000" is always transient.
	st, _, _, hasHTTP, tr := parseToolHTTPResponse("403")
	if st != 0 || hasHTTP || tr {
		t.Fatalf("plain 403 without -w got st=%d hasHTTP=%v tr=%v, want 0/false/false", st, hasHTTP, tr)
	}
	st, _, _, hasHTTP, tr = parseToolHTTPResponse("403:31")
	if st != 0 || hasHTTP || tr {
		t.Fatalf("plain 403:31 without -w got st=%d hasHTTP=%v tr=%v, want 0/false/false", st, hasHTTP, tr)
	}
	st, _, _, _, tr = parseToolHTTPResponse("000")
	if st != 0 || !tr {
		t.Fatalf("000 must return status=0 transient=true, got st=%d tr=%v", st, tr)
	}

	// Exact live Bash curl -w command parses 403 and 403:31 and treats 000 as transient.
	liveCmd := `curl -sk -o /dev/null -w '%{http_code}:%{size_download}' -H 'X-Estate-Peer: peer-1' https://edge.lab.local/api/v1/upload`
	st, ln, _, hasHTTP, tr := parseToolHTTPResponseForCommand(liveCmd, "403")
	if st != 403 || !hasHTTP || tr {
		t.Fatalf("-w 403 got st=%d hasHTTP=%v tr=%v, want 403/true/false", st, hasHTTP, tr)
	}
	st, ln, bh, hasHTTP, tr := parseToolHTTPResponseForCommand(liveCmd, "403:31")
	if st != 403 || ln != 31 || !hasHTTP || tr || bh == "" {
		t.Fatalf("-w 403:31 got st=%d ln=%d hasHTTP=%v tr=%v bh=%q", st, ln, hasHTTP, tr, bh)
	}
	st, _, _, _, tr = parseToolHTTPResponseForCommand(liveCmd, "000:0")
	if st != 0 || !tr {
		t.Fatalf("-w 000:0 must return status=0 transient=true, got st=%d tr=%v", st, tr)
	}

	// 1. Live -w sweep of 403:31 records gate_constant with Status=403 (NEVER Status=0).
	det := newGateConstantDetector(GateConstantHooks{})
	var emitted *GateConstantRecord
	for i := 1; i <= 5; i++ {
		id := "w" + itoa(int64(i))
		cmdJSON := `{"command":"curl -sk -o /dev/null -w '%{http_code}:%{size_download}' -H 'X-Estate-Peer: peer-` + id + `' https://edge.lab.local/api/v1/upload"}`
		det.onToolUse(id, "Bash", cmdJSON)
		rec, _ := det.onToolResult(id, "403:31\n[exit code 0]", false)
		if i == 5 {
			emitted = rec
		}
	}
	if emitted == nil || emitted.Status != 403 || emitted.Length != 31 || emitted.Host != "edge.lab.local" || emitted.Path != "/api/v1/upload" {
		t.Fatalf("expected HTTP 403 gate_constant from -w sweep, got %+v", emitted)
	}

	// 2. Dead-connection 000 burst MUST NEVER record a gate_constant.
	detDead := newGateConstantDetector(GateConstantHooks{})
	for i := 1; i <= 8; i++ {
		id := "d" + itoa(int64(i))
		cmdJSON := `{"command":"curl -sk -o /dev/null -w '%{http_code}' -H 'X-Estate-Peer: peer-` + id + `' https://edge.lab.local/api/v1/upload"}`
		detDead.onToolUse(id, "Bash", cmdJSON)
		rec, sweep := detDead.onToolResult(id, "000", false)
		if rec != nil || sweep {
			t.Fatalf("dead-connection 000 burst must never record gate_constant, got rec=%+v sweep=%v", rec, sweep)
		}
	}
}

func TestGateConstantMultiURLLoopAndExplicitForms(t *testing.T) {
	// 1. Shell loop form must expand $p into concrete paths instead of host="host$p", path="/".
	loopCmd := `{"command":"for p in /configuration /configuration/index; do curl -sk -o /dev/null -w '%{http_code}:%{size_download}\n' https://host$p; done"}`
	targets := extractProbeTargets("Bash", loopCmd)
	if len(targets) != 2 {
		t.Fatalf("expected 2 expanded loop targets, got %d (%+v)", len(targets), targets)
	}
	if targets[0].host != "host" || targets[0].path != "/configuration" || targets[1].host != "host" || targets[1].path != "/configuration/index" {
		t.Fatalf("unexpected loop targets: %+v", targets)
	}
	h0, p0, _, ok := extractProbeTarget("Bash", loopCmd)
	if !ok || h0 != "host" || p0 != "/configuration" {
		t.Fatalf("extractProbeTarget returned (%q, %q, %v), want (host, /configuration, true)", h0, p0, ok)
	}

	// Repeated loop runs with 403:31 on /configuration and 200:512.. (varying) on /configuration/index
	// must record gate_constant ONLY on /configuration without cross-path contamination.
	det := newGateConstantDetector(GateConstantHooks{})
	var recs []GateConstantRecord
	for i := 1; i <= 5; i++ {
		id := "l" + itoa(int64(i))
		cmd := `{"command":"for p in /configuration /configuration/index; do curl -sk -o /dev/null -w '%{http_code}:%{size_download}\n' -H 'X-Try: ` + id + `' https://host$p; done"}`
		det.onToolUse(id, "Bash", cmd)
		out := "403:31\n200:" + itoa(int64(500+i))
		got, _ := det.onToolResultMulti(id, out, false)
		recs = append(recs, got...)
	}
	if len(recs) != 1 || recs[0].Host != "host" || recs[0].Path != "/configuration" || recs[0].Status != 403 {
		t.Fatalf("expected single gate_constant on /configuration, got %+v", recs)
	}

	// 2. Two explicit URLs in one Bash command keep both paths and attribute responses separately.
	twoCmd := `{"command":"curl -si https://api.lab.local/first && curl -si https://api.lab.local/second"}`
	twoTargets := extractProbeTargets("Bash", twoCmd)
	if len(twoTargets) != 2 || twoTargets[0].path != "/first" || twoTargets[1].path != "/second" {
		t.Fatalf("expected /first and /second targets, got %+v", twoTargets)
	}

	det2 := newGateConstantDetector(GateConstantHooks{})
	var recs2 []GateConstantRecord
	for i := 1; i <= 5; i++ {
		id := "t" + itoa(int64(i))
		cmd := `{"command":"curl -si 'https://api.lab.local/first?k=` + id + `' && curl -si 'https://api.lab.local/second?k=` + id + `'"}`
		det2.onToolUse(id, "Bash", cmd)
		out := "HTTP/1.1 403 Forbidden\r\n\r\nlocked\nHTTP/1.1 404 Not Found\r\n\r\nmissing-" + id
		got, _ := det2.onToolResultMulti(id, out, false)
		recs2 = append(recs2, got...)
	}
	if len(recs2) != 1 || recs2[0].Path != "/first" || recs2[0].Status != 403 {
		t.Fatalf("expected two-URL form to attribute gate_constant strictly to /first (403), got %+v", recs2)
	}
}

func TestGateConstantParaphraseAndRouteBlockScope(t *testing.T) {
	store := testDB(t).Exploration(mustCreateExploration(t))
	tsx := NewToolSet(store, "planner")
	SetGateConstantStrictMode(true)
	t.Cleanup(ResetGateConstantStrictMode)

	// Create an open hypothesis so hypothesis_id alone cannot bypass gate_constant.
	openHypRes, err := tsx.upsertHypothesis().Call(t.Context(), json.RawMessage(`{"hypothesis":"Check peer enrolment header behavior"}`), nil)
	if err != nil || openHypRes.IsError {
		t.Fatalf("upsertHypothesis: %v %s", err, openHypRes.Flatten())
	}
	var hyp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(openHypRes.Flatten()), &hyp)

	// Even before gate_constant is recorded, quantified/peer paraphrases must still require hypothesis_id in strict mode.
	for _, para := range []string{
		"Test 150 peer names against https://edge.lab.local/api/v1/other",
		"Probe 150 peer names on https://edge.lab.local/api/v1/other",
	} {
		res, err := tsx.addIntent().Call(t.Context(), json.RawMessage(`{"summary":"`+para+`"}`), nil)
		if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "hypothesis_id") {
			t.Fatalf("expected paraphrase %q without hypothesis_id rejected in strict mode, got err=%v res=%s", para, err, res.Flatten())
		}
	}

	// Record gate_constant on edge.lab.local/api/v1/upload.
	if _, err := RecordGateConstantFact(store, 0, GateConstantRecord{
		Host:        "edge.lab.local",
		Path:        "/api/v1/upload",
		Status:      403,
		Length:      31,
		BodyHash:    "deadbeef1234",
		SampleCount: 5,
		InputsTried: []string{"p1", "p2", "p3", "p4", "p5"},
	}); err != nil {
		t.Fatal(err)
	}

	// Paraphrased / non-sweep non-read-only intents on the gated route MUST be blocked without reopen_reason+new_evidence.
	for _, blocked := range []string{
		"Test 150 peer names on https://edge.lab.local/api/v1/upload",
		"Probe 150 peer names against https://edge.lab.local/api/v1/upload",
		"Send alternative X-Estate-Peer header to https://edge.lab.local/api/v1/upload",
	} {
		payload, _ := json.Marshal(map[string]any{"summary": blocked, "hypothesis_id": hyp.ID})
		res, err := tsx.addIntent().Call(t.Context(), payload, nil)
		if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "gate_constant") {
			t.Fatalf("expected gated route block for paraphrase %q, got err=%v res=%s", blocked, err, res.Flatten())
		}
	}

	// Explicitly read-only intent on the same route IS allowed without reopen_reason.
	roPayload, _ := json.Marshal(map[string]any{"summary": "Read static OpenAPI documentation describing https://edge.lab.local/api/v1/upload"})
	if res, err := tsx.addIntent().Call(t.Context(), roPayload, nil); err != nil || res.IsError {
		t.Fatalf("expected explicitly read-only intent allowed despite gate_constant, got err=%v res=%s", err, res.Flatten())
	}

	// Providing both reopen_reason and new_evidence allows re-probing the gated route.
	reopenPayload, _ := json.Marshal(map[string]any{
		"summary":       "Test 150 peer names on https://edge.lab.local/api/v1/upload",
		"hypothesis_id": hyp.ID,
		"reopen_reason": "Discovered signed peer token requirement in client bundle",
		"new_evidence":  "Extracted signing key format from app.js",
	})
	if res, err := tsx.addIntent().Call(t.Context(), reopenPayload, nil); err != nil || res.IsError {
		t.Fatalf("expected reopen_reason+new_evidence to allow intent, got err=%v res=%s", err, res.Flatten())
	}
}
