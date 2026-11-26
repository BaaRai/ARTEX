// Package knowledge embeds a small, attributed security taxonomy pack used by
// the planner and worker tools. The pack is generated from the official sources
// by scripts/knowledge/build.py; it is not a training corpus and contains no
// scraped lab material.
package knowledge

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed data/attack.json data/wstg.json data/cwe.json
var dataFS embed.FS

// Entry is one taxonomy record. Descriptions are capped by the build script;
// Truncated marks that the full text is at URL.
type Entry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    []string `json:"category,omitempty"`
	Status      string   `json:"status,omitempty"`
	Description string   `json:"description,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	URL         string   `json:"url,omitempty"`
}

// SourceInfo describes one embedded source. The data files keep their own
// licenses; see knowledge/NOTICE and knowledge/licenses.
type SourceInfo struct {
	Source      string `json:"source"`
	Title       string `json:"title"`
	License     string `json:"license"`
	LicenseURL  string `json:"license_url"`
	SourceURL   string `json:"source_url"`
	RetrievedAt string `json:"retrieved_at"`
	Entries     int    `json:"entries"`
}

type source struct {
	info    SourceInfo
	entries []Entry
	byID    map[string]int
}

// Pack is the loaded knowledge pack.
type Pack struct {
	sources map[string]*source
	names   []string
}

var (
	defaultOnce sync.Once
	defaultPack *Pack
	defaultErr  error
)

// Default returns the embedded pack, loading it once. A malformed embedded pack
// is a build error, not a runtime condition; the error is returned so a caller
// can fail at startup.
func Default() (*Pack, error) {
	defaultOnce.Do(func() { defaultPack, defaultErr = Load() })
	return defaultPack, defaultErr
}

// Load parses the embedded sources. It is exported for tests and for callers
// that want to fail before the first lookup.
func Load() (*Pack, error) {
	p := &Pack{sources: map[string]*source{}}
	for _, name := range []string{"attack", "wstg", "cwe"} {
		raw, err := dataFS.ReadFile("data/" + name + ".json")
		if err != nil {
			return nil, fmt.Errorf("knowledge: read %s: %w", name, err)
		}
		var file struct {
			Source      string  `json:"source"`
			Title       string  `json:"title"`
			License     string  `json:"license"`
			LicenseURL  string  `json:"license_url"`
			SourceURL   string  `json:"source_url"`
			RetrievedAt string  `json:"retrieved_at"`
			Entries     []Entry `json:"entries"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("knowledge: parse %s: %w", name, err)
		}
		if file.Source != name {
			return nil, fmt.Errorf("knowledge: %s.json declares source %q", name, file.Source)
		}
		if len(file.Entries) == 0 {
			return nil, fmt.Errorf("knowledge: %s has no entries", name)
		}
		s := &source{
			info: SourceInfo{
				Source:      file.Source,
				Title:       file.Title,
				License:     file.License,
				LicenseURL:  file.LicenseURL,
				SourceURL:   file.SourceURL,
				RetrievedAt: file.RetrievedAt,
				Entries:     len(file.Entries),
			},
			entries: file.Entries,
			byID:    make(map[string]int, len(file.Entries)),
		}
		for i, e := range file.Entries {
			id := strings.ToUpper(strings.TrimSpace(e.ID))
			if id == "" || strings.TrimSpace(e.Name) == "" {
				return nil, fmt.Errorf("knowledge: %s has an entry without id or name", name)
			}
			if _, exists := s.byID[id]; exists {
				return nil, fmt.Errorf("knowledge: %s has duplicate id %s", name, id)
			}
			s.byID[id] = i
		}
		p.sources[name] = s
		p.names = append(p.names, name)
	}
	sort.Strings(p.names)
	return p, nil
}

// SourceNames returns the embedded source names in stable order.
func (p *Pack) SourceNames() []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(p.names))
	copy(out, p.names)
	return out
}

// SourceInfo returns the metadata for one source.
func (p *Pack) SourceInfo(name string) (SourceInfo, bool) {
	if p == nil {
		return SourceInfo{}, false
	}
	s, ok := p.sources[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return SourceInfo{}, false
	}
	return s.info, true
}

// Lookup returns the entry with the exact id in one source.
func (p *Pack) Lookup(source, id string) (Entry, bool) {
	if p == nil {
		return Entry{}, false
	}
	s, ok := p.sources[strings.ToLower(strings.TrimSpace(source))]
	if !ok {
		return Entry{}, false
	}
	i, ok := s.byID[strings.ToUpper(strings.TrimSpace(id))]
	if !ok {
		return Entry{}, false
	}
	return s.entries[i], true
}

// Search returns up to limit matches for query in one source and the total
// number of matches before the limit. An exact id wins and returns one entry.
func (p *Pack) Search(source, query string, limit int) ([]Entry, int, error) {
	if p == nil {
		return nil, 0, fmt.Errorf("knowledge pack is not loaded")
	}
	s, ok := p.sources[strings.ToLower(strings.TrimSpace(source))]
	if !ok {
		return nil, 0, fmt.Errorf("unknown knowledge source %q (valid: %s)", source, strings.Join(p.names, ", "))
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, 0, fmt.Errorf("query is required")
	}
	if i, ok := s.byID[strings.ToUpper(q)]; ok {
		return []Entry{s.entries[i]}, 1, nil
	}
	tokens := strings.Fields(strings.ToLower(q))
	if len(tokens) == 0 {
		return nil, 0, fmt.Errorf("query is required")
	}
	type hit struct {
		entry Entry
		score int
	}
	hits := make([]hit, 0, 16)
	lowerQuery := strings.ToLower(q)
	for _, e := range s.entries {
		hay := strings.ToLower(strings.Join([]string{
			e.ID, e.Name, strings.Join(e.Category, " "), e.Status, e.Description,
		}, " "))
		if !containsAll(hay, tokens) {
			continue
		}
		score := 1
		name := strings.ToLower(e.Name)
		id := strings.ToLower(e.ID)
		switch {
		case strings.HasPrefix(name, lowerQuery):
			score += 4
		case strings.Contains(name, lowerQuery):
			score += 3
		}
		if strings.HasPrefix(id, lowerQuery) {
			score += 2
		}
		hits = append(hits, hit{entry: e, score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].entry.ID < hits[j].entry.ID
	})
	total := len(hits)
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Entry, len(hits))
	for i := range hits {
		out[i] = hits[i].entry
	}
	return out, total, nil
}

func containsAll(hay string, tokens []string) bool {
	for _, token := range tokens {
		if !strings.Contains(hay, token) {
			return false
		}
	}
	return true
}
