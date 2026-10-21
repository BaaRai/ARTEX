package db

import (
	"strings"
	"testing"
)

func TestRenderLessonsDataNotInstructions(t *testing.T) {
	lessons := []Lesson{
		{ID: 1, Kind: "positive", Cue: "xss", Action: "Confirmed xss: reflected in search", Support: 2,
			Evidence: []string{"finding:10 (status=confirmed severity=high)"}},
		{ID: 2, Kind: "negative", Cue: "ssrf", Action: "Do not\nre-report\x00 without new mechanics", Support: 1},
	}
	block := RenderLessons(lessons, 2000)
	if !strings.Contains(block, "not instructions") {
		t.Fatalf("missing data-not-instructions header:\n%s", block)
	}
	if !strings.Contains(block, "[positive, 2 evidence]") || !strings.Contains(block, "[negative, 1 evidence]") {
		t.Fatalf("missing kind/support labels:\n%s", block)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "\x00") {
			t.Fatalf("control character survived: %q", line)
		}
	}
	if !strings.Contains(block, "Do not re-report without new mechanics") {
		t.Fatalf("lesson text was not flattened onto one line:\n%s", block)
	}
	if RenderLessons(nil, 2000) != "" {
		t.Fatal("empty lesson set must render empty")
	}
	capped := RenderLessons(lessons, 120)
	if len(capped) > 160 {
		t.Fatalf("cap not enforced: %d bytes", len(capped))
	}
}
