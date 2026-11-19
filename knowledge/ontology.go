package knowledge

import (
	"regexp"
	"sort"
	"sync"
)

// This file turns the ontology into queries. Callers want "which class is this"
// and "what escalates from here", not the JSON shape, so everything here is a
// lookup over the loaded corpus.

// Category returns the ontology category that owns a class, e.g.
// "sql_injection" -> "INJECTION", or false when the corpus does not know it.
func (s *Set) Category(class string) (string, bool) {
	if s == nil {
		return "", false
	}
	token := s.Class(class)
	if token == "" {
		return "", false
	}
	for name, cat := range s.Ontology.Categories {
		if normClass(name) == token {
			return name, true
		}
		for _, child := range cat.Children {
			if normClass(child) == token {
				return name, true
			}
		}
	}
	return "", false
}

// Children returns the class tokens a category owns, sorted. It returns nil for
// an unknown category rather than an empty slice, so callers can tell "no such
// category" from "category with no children".
func (s *Set) Children(category string) []string {
	if s == nil {
		return nil
	}
	want := normClass(category)
	for name, cat := range s.Ontology.Categories {
		if normClass(name) != want {
			continue
		}
		out := make([]string, 0, len(cat.Children))
		for _, child := range cat.Children {
			out = append(out, normClass(child))
		}
		sort.Strings(out)
		return out
	}
	return nil
}

// CWEParent returns the CWE a class rolls up to, e.g. "sql_injection" ->
// "CWE-74". Empty when the class is unknown.
func (s *Set) CWEParent(class string) string {
	cat, ok := s.Category(class)
	if !ok {
		return ""
	}
	return s.Ontology.Categories[cat].Parent
}

// EscalationTargets returns the abstract objectives a class can escalate to
// (DATA, CODE, ACCESS, ...), sorted.
func (s *Set) EscalationTargets(class string) []string {
	cat, ok := s.Category(class)
	if !ok {
		return nil
	}
	out := append([]string(nil), s.Ontology.Categories[cat].EscalationTargets...)
	sort.Strings(out)
	return out
}

// DedupKeys returns the fields that distinguish two findings of one class, e.g.
// INJECTION -> [endpoint injection_type parameter]. The corpus order is kept:
// it is the order the upstream dedup logic used.
func (s *Set) DedupKeys(class string) []string {
	cat, ok := s.Category(class)
	if !ok {
		return nil
	}
	return append([]string(nil), s.Ontology.Categories[cat].DedupKeyFields...)
}

// EscalationEdges returns the edges leaving a category, strongest first, so a
// caller can suggest "you have X, look for Y next". Ties break on target name so
// the order is stable across runs.
func (s *Set) EscalationEdges(category string) []OntologyEdge {
	if s == nil {
		return nil
	}
	want := normClass(category)
	var out []OntologyEdge
	for _, e := range s.Ontology.Escalation.Edges {
		if normClass(e.From) == want {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].To < out[j].To
	})
	return out
}

// Detection is one ontology regex hit with its weight. It is a hint to verify,
// never a verdict.
type Detection struct {
	Class  string
	Weight float64
}

// Detections returns the class labels whose ontology regex matches text,
// heaviest first. When two patterns hit the same label the strongest weight
// wins, so a specific rule beats a broad one.
func (s *Set) Detections(text string) []Detection {
	if s == nil || text == "" {
		return nil
	}
	best := map[string]float64{}
	for _, cat := range s.Ontology.Categories {
		for _, sig := range cat.RegexSignals {
			// The corpus writes these patterns in lowercase prose form, so they
			// must be matched case-insensitively: agent and tool output spells
			// the same keyword in every case.
			re, err := compiled("(?i)" + sig.Pattern)
			if err != nil || !re.MatchString(text) {
				continue
			}
			label := s.Class(sig.Label)
			if label == "" {
				continue
			}
			if sig.Weight > best[label] {
				best[label] = sig.Weight
			}
		}
	}
	out := make([]Detection, 0, len(best))
	for label, weight := range best {
		out = append(out, Detection{Class: label, Weight: weight})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Class < out[j].Class
	})
	return out
}

// regexCache memoises compiled patterns. The corpus is immutable, so a
// package-level cache is safe. A pattern that fails to compile is cached as nil,
// so a bad entry costs one attempt rather than one per call.
var regexCache sync.Map // string -> *regexp.Regexp (nil when uncompilable)

// compiled returns the compiled pattern. Callers skip an unusable pattern rather
// than failing the whole lookup: one exotic entry must not disable detection.
func compiled(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, errEmptyPattern
	}
	if v, ok := regexCache.Load(pattern); ok {
		if re, _ := v.(*regexp.Regexp); re != nil {
			return re, nil
		}
		return nil, errBadPattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		regexCache.Store(pattern, (*regexp.Regexp)(nil))
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// constError is a string-backed error, so the sentinels below cost no
// allocation at package init.
type constError string

func (e constError) Error() string { return string(e) }

const (
	errEmptyPattern = constError("knowledge: empty pattern")
	errBadPattern   = constError("knowledge: pattern previously failed to compile")
)

// CompileProblems lists every corpus pattern Go's regexp engine rejects. A test
// asserts the list is empty, so a data refresh cannot silently ship dead
// patterns that would never match.
func (s *Set) CompileProblems() []string {
	if s == nil {
		return nil
	}
	var bad []string
	check := func(where, pattern string) {
		if pattern == "" {
			// An empty header pattern means "match on header presence", which is
			// intentional in waf_signatures, so it is not a problem.
			return
		}
		if _, err := regexp.Compile(pattern); err != nil {
			bad = append(bad, where+": "+pattern+" ("+err.Error()+")")
		}
	}

	for name, cat := range s.Ontology.Categories {
		for _, sig := range cat.RegexSignals {
			check("vuln_ontology/"+name, sig.Pattern)
		}
	}
	for _, sig := range s.WAF.HeaderSignatures {
		check("waf_signatures/header/"+sig.WAF, sig.Pattern)
	}
	for _, sig := range s.WAF.BodySignatures {
		check("waf_signatures/body/"+sig.WAF, sig.Pattern)
	}
	for _, p := range s.Endpoints.URLPatterns {
		check("endpoint_patterns/url", p.Pattern)
	}
	for _, p := range s.Endpoints.SubdomainPatterns {
		check("endpoint_patterns/subdomain", p.Pattern)
	}
	for k, v := range s.Verification.AuthFailure {
		check("verification_patterns/auth_failure/"+k, v)
	}
	for k, v := range s.Verification.LoginForm {
		check("verification_patterns/login_form/"+k, v)
	}
	for _, group := range []struct {
		name string
		list []string
	}{
		{"clean_response", s.Verification.CleanResponse},
		{"dynamic_content", s.Verification.DynamicContent},
		{"honeypot", s.Verification.Honeypot},
		{"waf_block", s.Verification.WAFBlock},
		{"lfi_file", s.Verification.LFIFile},
		{"sql_error", s.Verification.SQLError},
		{"false_positive", s.Verification.FalsePositive},
		{"url_encoding", s.Verification.URLEncodingCheck},
	} {
		for _, p := range group.list {
			check("verification_patterns/"+group.name, p)
		}
	}
	sort.Strings(bad)
	return bad
}
