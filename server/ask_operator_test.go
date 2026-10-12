package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageAskOperatorImage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(src, []byte("fakepng"), 0o644); err != nil {
		t.Fatal(err)
	}

	// no image -> no attachment
	att, err := stageAskOperatorImage(dir, "7", "")
	if err != nil || att != nil {
		t.Fatalf("empty src: att=%v err=%v", att, err)
	}

	// valid image -> copied under the task uploads, descriptor matches the chat format
	att, err = stageAskOperatorImage(dir, "7", src)
	if err != nil || len(att) != 1 {
		t.Fatalf("valid src: att=%v err=%v", att, err)
	}
	if !strings.HasPrefix(att[0].Path, "uploads/ask-operator-") || !strings.HasSuffix(att[0].Path, ".png") {
		t.Fatalf("unexpected attachment path %q", att[0].Path)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "7", "uploads", att[0].Name)); err != nil {
		t.Fatalf("copy missing: %v", err)
	}

	// wrong extension rejected
	bad := filepath.Join(dir, "payload.exe")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageAskOperatorImage(dir, "7", bad); err == nil || !strings.Contains(err.Error(), "png/jpg") {
		t.Fatalf("expected extension rejection, got %v", err)
	}

	// missing file rejected
	if _, err := stageAskOperatorImage(dir, "7", filepath.Join(dir, "nope.png")); err == nil {
		t.Fatal("expected missing-file rejection")
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
