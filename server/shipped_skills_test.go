package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/norma/skill"
)

// Every skill shipped in the repository's skills/ directory must parse cleanly
// with the real frontmatter parser and carry a description: a broken YAML
// scalar silently empties the metadata and the skill stops being discoverable
// by the agents (see the skills-YAML fix for the same failure mode).
func TestShippedSkillsParseCleanly(t *testing.T) {
	dir := filepath.Join("..", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no shipped skills dir: %v", err)
	}
	reg, err := skill.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir on shipped skills: %v", err)
	}
	byDir := map[string]skill.Skill{}
	if reg != nil {
		for _, sk := range reg.List() {
			if sk.Dir != "" {
				byDir[filepath.Base(sk.Dir)] = sk
			}
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sk, ok := byDir[e.Name()]
		if !ok {
			t.Errorf("skill %s: not parsed by LoadDir (frontmatter broken?)", e.Name())
			continue
		}
		if sk.Description == "" {
			t.Errorf("skill %s: empty description after parse", e.Name())
		}
	}
	if _, ok := byDir["codeaudit"]; !ok {
		t.Error("codeaudit skill missing from shipped skills")
	}
}
