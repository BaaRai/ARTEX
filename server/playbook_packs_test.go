package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/skill"
)

// The playbook packs ported from AIRecon (see docs/skill-sources.md) are one
// SKILL.md router plus a references/ tree each. A pack whose router fails to
// parse, or that cites a reference which is not on disk, silently degrades into
// a skill the agent can invoke but cannot act on: the loader falls back to the
// directory name, the description goes empty, and every link in the body dangles.
func TestPlaybookPacksResolve(t *testing.T) {
	dir := filepath.Join("..", "skills")
	packs := []string{
		"recon-playbook", "web-vuln", "payload-cookbook", "protocol-playbook",
		"framework-playbook", "tech-playbook", "tool-playbook", "post-exploit",
		"ctf-playbook", "ai-redteam", "web3-playbook",
	}
	// `references/x.md`, `../other-pack/references/y.md`
	linkRe := regexp.MustCompile("`([^`]+\\.md)`")

	for _, pack := range packs {
		packDir := filepath.Join(dir, pack)
		reg, err := skill.LoadDir(packDir)
		if err != nil {
			t.Errorf("%s: LoadDir: %v", pack, err)
			continue
		}
		if len(reg.List()) != 1 {
			t.Errorf("%s: loaded %d skills, want exactly 1 router", pack, len(reg.List()))
			continue
		}
		s := reg.List()[0]
		if s.Name != pack {
			t.Errorf("%s: parsed name %q != directory (frontmatter did not parse)", pack, s.Name)
		}
		if len(s.Description) < 50 {
			t.Errorf("%s: description %d chars, too short to be discoverable", pack, len(s.Description))
		}

		refs, err := os.ReadDir(filepath.Join(packDir, "references"))
		if err != nil {
			t.Errorf("%s: no references/ tree: %v", pack, err)
			continue
		}
		if len(refs) == 0 {
			t.Errorf("%s: references/ is empty", pack)
		}
		onDisk := map[string]bool{}
		for _, r := range refs {
			if !r.IsDir() {
				onDisk[r.Name()] = true
			}
		}

		cited := map[string]bool{}
		for _, m := range linkRe.FindAllStringSubmatch(s.Instructions, -1) {
			link := filepath.ToSlash(m[1])
			cited[filepath.Base(link)] = true
			var target string
			if strings.HasPrefix(link, "../") {
				target = filepath.Join(packDir, filepath.FromSlash(link))
			} else {
				target = filepath.Join(packDir, "references", filepath.Base(link))
			}
			if _, statErr := os.Stat(target); statErr != nil {
				t.Errorf("%s: cites missing file %s", pack, link)
			}
		}

		var uncited []string
		for name := range onDisk {
			if !cited[name] {
				uncited = append(uncited, name)
			}
		}
		sort.Strings(uncited)
		if len(uncited) > 0 {
			t.Errorf("%s: %d reference(s) never cited by the router (unreachable): %v", pack, len(uncited), uncited)
		}
	}
}

// The Skill tool lists every visible skill into every request under a hard
// character budget (norma's maxSkillListingChars, 8000): once the budget is hit,
// the remaining skills degrade to name-only and the model can no longer tell
// what they do. Adding a pack must not silently push the shipped set over.
func TestShippedSkillListingFitsBudget(t *testing.T) {
	const budget = 8000 // mirrors skill.maxSkillListingChars

	dir := filepath.Join("..", "skills")
	reg, err := skill.LoadDir(dir)
	if err != nil {
		t.Skipf("no shipped skills dir: %v", err)
	}

	var b strings.Builder
	for _, s := range reg.List() {
		desc := s.Description
		if desc == "" {
			desc = s.WhenToUse
		}
		b.WriteString("\n- " + s.Name + ": " + desc)
	}
	if got := b.Len(); got > budget {
		t.Errorf("shipped skills render %d chars of listing, over the %d budget; "+
			"the tail degrades to name-only and those skills stop being discoverable",
			got, budget)
	}
}
