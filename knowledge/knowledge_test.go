package knowledge

import (
	"strings"
	"testing"
)

func load(t *testing.T) *Pack {
	t.Helper()
	p, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return p
}

func TestLoadSources(t *testing.T) {
	p := load(t)
	names := p.SourceNames()
	if len(names) != 3 {
		t.Fatalf("sources = %v, want attack, cwe, wstg", names)
	}
	want := map[string]int{"attack": 600, "wstg": 100, "cwe": 900}
	for _, name := range names {
		info, ok := p.SourceInfo(name)
		if !ok {
			t.Fatalf("SourceInfo(%q) missing", name)
		}
		if info.Entries < want[name] {
			t.Errorf("%s entries = %d, want at least %d", name, info.Entries, want[name])
		}
		if info.License == "" || info.SourceURL == "" || info.RetrievedAt == "" {
			t.Errorf("%s metadata incomplete: %+v", name, info)
		}
	}
}

func TestLookupExact(t *testing.T) {
	p := load(t)
	cases := []struct {
		source, id, name string
	}{
		{"attack", "T1190", "Exploit Public-Facing Application"},
		{"wstg", "WSTG-ATHN-01", "Default Credentials"},
		{"cwe", "CWE-79", "Improper Neutralization of Input During Web Page Generation"},
	}
	for _, c := range cases {
		e, ok := p.Lookup(c.source, c.id)
		if !ok {
			t.Fatalf("%s %s not found", c.source, c.id)
		}
		if e.Name != c.name {
			t.Errorf("%s name = %q, want %q", c.id, e.Name, c.name)
		}
		if e.URL == "" {
			t.Errorf("%s has no url", c.id)
		}
	}
}

func TestLookupIsCaseInsensitiveAndTrimmed(t *testing.T) {
	p := load(t)
	for _, id := range []string{"t1190", " T1190 ", "T1190"} {
		if _, ok := p.Lookup("ATTACK", id); !ok {
			t.Errorf("Lookup(ATTACK, %q) not found", id)
		}
	}
}

func TestSearchKeyword(t *testing.T) {
	p := load(t)
	matches, total, err := p.Search("wstg", "default credentials", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total == 0 || len(matches) == 0 {
		t.Fatal("no matches for default credentials")
	}
	if matches[0].ID != "WSTG-ATHN-01" {
		t.Errorf("first match = %s, want WSTG-ATHN-01", matches[0].ID)
	}
}

func TestSearchExactIDWins(t *testing.T) {
	p := load(t)
	matches, total, err := p.Search("cwe", "cwe-79", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 1 || len(matches) != 1 || matches[0].ID != "CWE-79" {
		t.Fatalf("matches=%v total=%d, want one CWE-79", matches, total)
	}
}

func TestSearchLimit(t *testing.T) {
	p := load(t)
	matches, total, err := p.Search("cwe", "data", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	if total <= 2 {
		t.Fatalf("total = %d, want more than the limit", total)
	}
	matches, _, err = p.Search("cwe", "data", 100)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) > 20 {
		t.Fatalf("limit was not clamped: %d", len(matches))
	}
}

func TestSearchErrors(t *testing.T) {
	p := load(t)
	if _, _, err := p.Search("nope", "x", 5); err == nil || !strings.Contains(err.Error(), "unknown knowledge source") {
		t.Fatalf("unknown source error = %v", err)
	}
	if _, _, err := p.Search("attack", "  ", 5); err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("empty query error = %v", err)
	}
}

func TestEveryEntryHasNameAndURL(t *testing.T) {
	p := load(t)
	for _, name := range p.SourceNames() {
		for _, e := range p.sources[name].entries {
			if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Name) == "" {
				t.Fatalf("%s has an entry without id or name: %+v", name, e)
			}
			if strings.TrimSpace(e.URL) == "" {
				t.Fatalf("%s %s has no url", name, e.ID)
			}
		}
	}
}

func TestSearchRanksIDPrefixHigher(t *testing.T) {
	p, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Construct a small pack where one entry's ID has prefix "t105" and another
	// only mentions "t105" in its description, with an alphabetically earlier ID.
	// Without the lowerQuery fix, both get score 1 and "A000" sorts first.
	custom := &Pack{
		sources: map[string]*source{
			"attack": {
				entries: []Entry{
					{ID: "A000", Name: "Unrelated Alpha", Description: "mentions t105 in text"},
					{ID: "T1059", Name: "Command and Scripting Interpreter", Description: "shell execution"},
				},
				byID: map[string]int{"A000": 0, "T1059": 1},
			},
		},
		names: []string{"attack"},
	}
	_ = p
	got, total, err := custom.Search("attack", "T105", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("got %d (total %d), want 2", len(got), total)
	}
	if got[0].ID != "T1059" {
		t.Fatalf("first match = %s, want T1059 ranked ahead of description-only match A000", got[0].ID)
	}
}
