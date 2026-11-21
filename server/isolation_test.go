package server

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
)

// captureLog collects what the standard logger writes while fn runs.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(prev); log.SetFlags(flags) }()
	fn()
	return buf.String()
}

// A failed self-test stops the start; the escape, off by default, lets it go on
// with the warning banner; a passing self-test starts either way.
func TestEnforceIsolation(t *testing.T) {
	cause := errors.New("Bash runs as uid 0, not the executor uid 10001")

	var err error
	out := captureLog(t, func() { err = EnforceIsolation(cause, false) })
	if err == nil || !strings.Contains(err.Error(), cause.Error()) || !strings.Contains(err.Error(), "-"+DevNoIsolationFlag) {
		t.Fatalf("without the escape: err = %v, want the cause and the flag named", err)
	}
	if strings.Contains(out, "AGENT ISOLATION IS OFF") {
		t.Fatalf("the banner belongs to the escape only:\n%s", out)
	}

	out = captureLog(t, func() { err = EnforceIsolation(cause, true) })
	if err != nil {
		t.Fatalf("with the escape the start goes on: %v", err)
	}
	for _, want := range []string{"AGENT ISOLATION IS OFF", "-" + DevNoIsolationFlag, "DEVELOPMENT ONLY", cause.Error(), "keys", "database"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner lacks %q:\n%s", want, out)
		}
	}

	for _, escape := range []bool{false, true} {
		out = captureLog(t, func() { err = EnforceIsolation(nil, escape) })
		if err != nil || !strings.Contains(out, "self-test passed") || strings.Contains(out, "AGENT ISOLATION IS OFF") {
			t.Fatalf("passing self-test (escape %v): err=%v\n%s", escape, err, out)
		}
	}
}

// With the drop disabled (the server is not root, -exec-uid=-1, or no credentials
// on this platform) the self-test reports isolation as unavailable.
func TestIsolationSelfTestUnavailableWithoutDrop(t *testing.T) {
	if err := IsolationSelfTest(ExecPrivilege{}, IsolationTargets{}); err == nil {
		t.Fatal("a disabled drop must fail the self-test")
	}
}
