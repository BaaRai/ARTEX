package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAssessmentIdentification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("ARTEX_CONFIG", path)
	cfg, err := Load()
	if err != nil || cfg.AssessmentIdentification.UserAgent("42") != "" {
		t.Fatalf("missing file: config = %+v, error = %v", cfg, err)
	}

	const maxReference = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz._"
	body := `{"assessment_identification":{"42":"FS-17","43":"","44":"` + maxReference + `"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ taskID, want string }{
		{"42", "ARTEX-Assessment/1.0 (engagement=FS-17)"},
		{"43", "ARTEX-Assessment/1.0"},
		{"44", "ARTEX-Assessment/1.0 (engagement=" + maxReference + ")"},
		{"45", ""},
		{"", ""},
	} {
		if got := cfg.AssessmentIdentification.UserAgent(tc.taskID); got != tc.want {
			t.Errorf("task %q: User-Agent = %q, want %q", tc.taskID, got, tc.want)
		}
	}
}

func TestLoadRejectsInvalidAssessmentIdentification(t *testing.T) {
	for _, tc := range []struct {
		name, entries string
	}{
		{"empty task ID", `{"":"FS-17"}`},
		{"noncanonical task ID", `{"042":"FS-17"}`},
		{"zero task ID", `{"0":"FS-17"}`},
		{"non-task key", `{"*":"FS-17"}`},
		{"invalid shape", `["FS-17"]`},
		{"null engagement", `{"42":null}`},
		{"embedded line breaks", `{"42":"FS-17\r\nX-Other: value"}`},
		{"comment delimiters", `{"42":"FS-(17)"}`},
		{"non-ASCII label", `{"42":"équipe"}`},
		{"oversize label", `{"42":"` + strings.Repeat("A", 65) + `"}`},
		{"invalid entry alongside valid entry", `{"42":"FS-17","43":"FS 18"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"assessment_identification":`+tc.entries+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ARTEX_CONFIG", path)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "assessment_identification") {
				t.Fatalf("Load() error = %v, want assessment_identification validation error", err)
			}
		})
	}
}
