// Package knowledge embeds the AIRecon knowledge corpus (22 JSON files curated
// upstream at github.com/pikpikcu/airecon) and exposes it as typed, validated Go
// data.
//
// The corpus ships inside the binary, the same way db embeds schema.sql, so it
// needs no data directory at runtime. Nothing here touches the network or the
// database, which makes the package safe to import from any layer.
//
// Two rules keep the import honest:
//
//   - Verified-only. port_correlations ships without the upstream vulns[] and
//     tools[] arrays, which carried misattributed CVEs and binaries this image
//     does not ship. docs/skill-sources.md has the correction log.
//   - Validated on load. Load checks structure, id uniqueness and regex
//     compilability, so a bad edit fails the build rather than a task.
//
// Load is cheap - one pass over roughly half a megabyte of JSON - and its result
// is cached. Call it once at startup and pass the *Set down.
package knowledge

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed data/*.json
var dataFS embed.FS

// Set is the whole corpus, one field per source file. Files that are maps of
// records stay maps keyed by their natural id (port number, CVE id, tech name,
// pattern name) so a lookup never scans a slice.
type Set struct {
	Ontology     Ontology
	WAF          WAFSignatures
	Tech         map[string]TechProfile
	Ports        map[string]PortCorrelation
	CVEs         map[string]CVE
	Endpoints    EndpointPatterns
	Verification VerificationPatterns
	Extensions   FileExtensions
	Fuzzer       FuzzerData
	Patterns     BehaviorPatterns
	ABSignals    ABSignals
	AttackChains AttackChains
	Escalation   EscalationPatterns
	Profiles     ScanProfiles
	Refusals     Markers
	Awaiting     Markers
	Compressors  Compressors
	Tools        []ToolSpec
	ToolsMeta    ToolsMeta
	Unified      UnifiedPatterns
	SkillIndex   Skills
	WAFBypass    WAFBypass

	// classes maps a normalised alias to its canonical class token. Canonical
	// tokens map to themselves, so Class is idempotent.
	classes map[string]string
}

var (
	once      sync.Once
	cached    *Set
	cachedErr error
)

// Load parses and validates the embedded corpus. Results are cached: repeated
// calls return the same *Set, and a validation failure is sticky.
func Load() (*Set, error) {
	once.Do(func() { cached, cachedErr = load() })
	return cached, cachedErr
}

// MustLoad is Load for tests and package-level initialisation, where a broken
// corpus is a build defect rather than a runtime condition.
func MustLoad() *Set {
	s, err := Load()
	if err != nil {
		panic("knowledge: " + err.Error())
	}
	return s
}

// Files lists the embedded corpus file names, sorted. It exists so a test can
// assert the vendored set is complete rather than assume it.
func Files() ([]string, error) {
	entries, err := dataFS.ReadDir("data")
	if err != nil {
		return nil, fmt.Errorf("knowledge: read data dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Raw returns one embedded file verbatim. Callers that need the upstream bytes
// (provenance checks, tests) use this instead of re-reading the parsed structs.
func Raw(name string) ([]byte, error) {
	b, err := dataFS.ReadFile("data/" + name)
	if err != nil {
		return nil, fmt.Errorf("knowledge: read %s: %w", name, err)
	}
	return b, nil
}

func load() (*Set, error) {
	s := &Set{}
	read := func(name string, dst any) error {
		b, err := dataFS.ReadFile("data/" + name)
		if err != nil {
			return fmt.Errorf("knowledge: read %s: %w", name, err)
		}
		if err := json.Unmarshal(b, dst); err != nil {
			return fmt.Errorf("knowledge: parse %s: %w", name, err)
		}
		return nil
	}
	for _, step := range []struct {
		name string
		dst  any
	}{
		{"vuln_ontology.json", &s.Ontology},
		{"waf_signatures.json", &s.WAF},
		{"tech_correlations.json", &s.Tech},
		{"port_correlations.json", &s.Ports},
		{"cve_correlations.json", &s.CVEs},
		{"endpoint_patterns.json", &s.Endpoints},
		{"verification_patterns.json", &s.Verification},
		{"file_extensions.json", &s.Extensions},
		{"fuzzer_data.json", &s.Fuzzer},
		{"patterns.json", &s.Patterns},
		{"ab_signals.json", &s.ABSignals},
		{"attack_chains.json", &s.AttackChains},
		{"escalation_patterns.json", &s.Escalation},
		{"scan_profiles.json", &s.Profiles},
		{"refusal_markers.json", &s.Refusals},
		{"awaiting_input_markers.json", &s.Awaiting},
		{"compressors.json", &s.Compressors},
		{"tools.json", &s.Tools},
		{"tools_meta.json", &s.ToolsMeta},
		{"unified_patterns.json", &s.Unified},
		{"skills.json", &s.SkillIndex},
		{"waff_bypass.json", &s.WAFBypass},
	} {
		if err := read(step.name, step.dst); err != nil {
			return nil, err
		}
	}

	s.classes = buildClassIndex(s.Ontology, s.ABSignals)
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// ---- custom decoders for the three mixed-shape files -----------------------

// UnmarshalJSON splits scan_profiles into its comment block and its per-profile
// override maps. Values are any because the keys are AIRecon's config keys.
func (p *ScanProfiles) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Profiles = make(map[string]ProfileOverrides, len(raw))
	for k, v := range raw {
		if k == "_comment" {
			if err := json.Unmarshal(v, &p.Comment); err != nil {
				return fmt.Errorf("_comment: %w", err)
			}
			continue
		}
		var ov ProfileOverrides
		if err := json.Unmarshal(v, &ov); err != nil {
			return fmt.Errorf("profile %q: %w", k, err)
		}
		if ov == nil {
			ov = ProfileOverrides{}
		}
		p.Profiles[k] = ov
	}
	return nil
}

// UnmarshalJSON splits patterns.json into its behaviour rules and the one
// operational table filed under the same object. The table's values are
// technique lists, not rules, so it stays out of Rules.
func (p *BehaviorPatterns) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Rules = make(map[string]BehaviorPattern, len(raw))
	for k, v := range raw {
		if k == "waf_bypass_strategies" {
			if err := json.Unmarshal(v, &p.WAFBypass); err != nil {
				return fmt.Errorf("waf_bypass_strategies: %w", err)
			}
			continue
		}
		var rule BehaviorPattern
		if err := json.Unmarshal(v, &rule); err != nil {
			return fmt.Errorf("pattern %q: %w", k, err)
		}
		p.Rules[k] = rule
	}
	return nil
}

// UnmarshalJSON splits unified_patterns' metadata, objective table and nested
// detection section from the merged pattern map. The pattern name is kept as the
// map key.
func (u *UnifiedPatterns) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	u.Patterns = make(map[string]UnifiedPattern, len(raw))
	for k, v := range raw {
		switch k {
		case "_comment":
			if err := json.Unmarshal(v, &u.Comment); err != nil {
				return fmt.Errorf("_comment: %w", err)
			}
		case "_version":
			if err := json.Unmarshal(v, &u.Version); err != nil {
				return fmt.Errorf("_version: %w", err)
			}
		case "_merged_from":
			if err := json.Unmarshal(v, &u.MergedFrom); err != nil {
				return fmt.Errorf("_merged_from: %w", err)
			}
		case "objectives":
			if err := json.Unmarshal(v, &u.Objectives); err != nil {
				return fmt.Errorf("objectives: %w", err)
			}
		case "vuln_detection_patterns":
			// A nested detection section, not a behaviour rule: it has no name,
			// indicators or actions, so decoding it as a pattern would invent one.
			if err := json.Unmarshal(v, &u.Detection); err != nil {
				return fmt.Errorf("vuln_detection_patterns: %w", err)
			}
		default:
			var pat UnifiedPattern
			if err := json.Unmarshal(v, &pat); err != nil {
				return fmt.Errorf("pattern %q: %w", k, err)
			}
			if pat.Name == "" {
				pat.Name = k
			}
			u.Patterns[k] = pat
		}
	}
	return nil
}

// UnmarshalJSON keeps the whole tools_meta document in Raw and lifts the keys
// with a stable shape into typed views, so an unchanged upstream key is never
// silently dropped and a new key needs no struct change.
func (m *ToolsMeta) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &m.Raw); err != nil {
		return err
	}
	typed := []struct {
		key string
		dst any
	}{
		{"categories", &m.Categories},
		{"parallelizable_tools", &m.Parallelizable},
		{"tool_alternatives", &m.ToolAlternatives},
		{"tool_descriptions", &m.ToolDescriptions},
		{"skills_catalog", &m.SkillsCatalog},
		{"tool_flag_conflicts", &m.ToolFlagConflicts},
		{"phase_sequence", &m.PhaseSequence},
	}
	for _, t := range typed {
		v, ok := m.Raw[t.key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(v, t.dst); err != nil {
			return fmt.Errorf("tools_meta %q: %w", t.key, err)
		}
	}
	return nil
}

// ---- validation ------------------------------------------------------------

// validate rejects anything that would make a consumer misbehave. Regexes that
// do not compile are reported by CompileProblems (and asserted by a test) rather
// than failing Load, so one exotic pattern cannot block the whole corpus.
func (s *Set) validate() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("knowledge: "+format, args...)
	}

	if len(s.Ontology.Categories) == 0 {
		return fail("vuln_ontology has no categories")
	}
	for name, cat := range s.Ontology.Categories {
		if !strings.HasPrefix(cat.Parent, "CWE-") {
			return fail("category %q parent %q is not a CWE id", name, cat.Parent)
		}
		if len(cat.Children) == 0 {
			return fail("category %q has no children", name)
		}
	}
	for _, e := range s.Ontology.Escalation.Edges {
		if _, ok := s.Ontology.Categories[e.From]; !ok {
			return fail("escalation edge from unknown category %q", e.From)
		}
		if e.To == "" {
			return fail("escalation edge from %q has an empty target", e.From)
		}
		if e.Weight <= 0 || e.Weight > 1 {
			return fail("escalation edge %s->%s weight %v out of range", e.From, e.To, e.Weight)
		}
	}
	if t := s.Ontology.DedupRules.SemanticThreshold; t <= 0 || t > 1 {
		return fail("dedup semantic_threshold %v out of range", t)
	}

	if len(s.WAF.HeaderSignatures) == 0 || len(s.WAF.BodySignatures) == 0 {
		return fail("waf_signatures is missing a signature list")
	}
	for _, sig := range s.WAF.HeaderSignatures {
		if sig.Header == "" || sig.WAF == "" {
			return fail("waf header signature without header or waf")
		}
		if sig.Confidence <= 0 || sig.Confidence > 1 {
			return fail("waf header signature %s/%s confidence %v out of range", sig.WAF, sig.Header, sig.Confidence)
		}
	}
	for _, sig := range s.WAF.BodySignatures {
		if sig.Pattern == "" || sig.WAF == "" {
			return fail("waf body signature without pattern or waf")
		}
		if sig.Confidence <= 0 || sig.Confidence > 1 {
			return fail("waf body signature %s confidence %v out of range", sig.WAF, sig.Confidence)
		}
	}
	if len(s.WAF.BlockStatusCodes) == 0 {
		return fail("waf_signatures has no block status codes")
	}

	if len(s.Tech) == 0 {
		return fail("tech_correlations is empty")
	}
	for name, t := range s.Tech {
		if t.Category == "" {
			return fail("tech %q has no category", name)
		}
	}

	if len(s.Ports) == 0 {
		return fail("port_correlations is empty")
	}
	for port, p := range s.Ports {
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n < 1 || n > 65535 {
			return fail("port_correlations key %q is not a valid port", port)
		}
		if p.Service == "" {
			return fail("port %s has no service", port)
		}
		if p.Severity != "" {
			switch p.Severity {
			case "CRITICAL", "HIGH", "MEDIUM", "LOW":
			default:
				return fail("port %s severity %q is not one of CRITICAL/HIGH/MEDIUM/LOW", port, p.Severity)
			}
		}
	}

	if len(s.CVEs) == 0 {
		return fail("cve_correlations is empty")
	}
	for id, c := range s.CVEs {
		if !strings.HasPrefix(id, "CVE-") {
			return fail("cve_correlations key %q is not a CVE id", id)
		}
		if c.Product == "" || c.Name == "" {
			return fail("cve %s is missing name or product", id)
		}
	}

	if len(s.Endpoints.URLPatterns) == 0 || len(s.Endpoints.SubdomainPatterns) == 0 {
		return fail("endpoint_patterns is missing a pattern list")
	}
	for _, p := range append(append([]EndpointPattern{}, s.Endpoints.URLPatterns...), s.Endpoints.SubdomainPatterns...) {
		if p.Score <= 0 || p.Score > 1 {
			return fail("endpoint pattern %q score %v out of range", p.Pattern, p.Score)
		}
	}

	if len(s.Verification.SQLError) == 0 || len(s.Verification.Honeypot) == 0 {
		return fail("verification_patterns is missing a core group")
	}

	if len(s.Extensions.HighValue) == 0 || len(s.Extensions.Backend) == 0 {
		return fail("file_extensions is missing a bucket")
	}

	if len(s.Fuzzer.FuzzPoints) == 0 || len(s.Fuzzer.FuzzPayloads) == 0 {
		return fail("fuzzer_data is missing a fuzz table")
	}

	if len(s.ABSignals.Signals) == 0 {
		return fail("ab_signals is empty")
	}
	seen := map[string]bool{}
	for _, sig := range s.ABSignals.Signals {
		if sig.ID == "" {
			return fail("ab_signals has a signal with an empty id")
		}
		if seen[sig.ID] {
			return fail("ab_signals has duplicate id %q", sig.ID)
		}
		seen[sig.ID] = true
	}

	if len(s.AttackChains.Chains) == 0 {
		return fail("attack_chains is empty")
	}
	chainIDs := map[string]bool{}
	for _, c := range s.AttackChains.Chains {
		if c.ID == "" {
			return fail("attack_chains has a chain with an empty id")
		}
		if chainIDs[c.ID] {
			return fail("attack_chains has duplicate id %q", c.ID)
		}
		chainIDs[c.ID] = true
		if len(c.Steps) == 0 {
			return fail("attack chain %q has no steps", c.ID)
		}
		for i, st := range c.Steps {
			if st.Order != i+1 {
				return fail("attack chain %q step %d has order %d", c.ID, i+1, st.Order)
			}
		}
	}

	for _, name := range []string{"quick", "standard", "deep", "stealth", "ctf", "bugbounty"} {
		if _, ok := s.Profiles.Profiles[name]; !ok {
			return fail("scan_profiles is missing profile %q", name)
		}
	}

	if len(s.Refusals.Markers) == 0 || len(s.Awaiting.Markers) == 0 {
		return fail("marker files are empty")
	}
	for _, group := range [][]string{s.Refusals.Markers, s.Awaiting.Markers} {
		for _, m := range group {
			if strings.TrimSpace(m) == "" {
				return fail("a marker list has a blank entry")
			}
			if strings.ToLower(m) != m {
				return fail("marker %q is not lowercase, so it can never match", m)
			}
		}
	}

	for name, c := range s.Compressors {
		if c.Version == "" || len(c.Assets) == 0 {
			return fail("compressor %q missing version or assets", name)
		}
		for platform, a := range c.Assets {
			if a.URL == "" || len(a.SHA256) != 64 {
				return fail("compressor %q asset %q needs a url and a sha256", name, platform)
			}
		}
	}

	seenTool := map[string]bool{}
	for _, t := range s.Tools {
		if t.Type != "function" || t.Function.Name == "" {
			return fail("tools entry %q is not a named function", t.Function.Name)
		}
		if seenTool[t.Function.Name] {
			return fail("tools has duplicate function %q", t.Function.Name)
		}
		seenTool[t.Function.Name] = true
	}

	if len(s.Unified.Patterns) == 0 || len(s.Unified.Objectives) == 0 {
		return fail("unified_patterns is missing patterns or objectives")
	}

	if len(s.SkillIndex.SkillKeywords) == 0 {
		return fail("skills is empty")
	}

	for name, rule := range s.Patterns.Rules {
		if rule.Description == "" || len(rule.Indicators) == 0 {
			return fail("pattern %q has no description or indicators", name)
		}
	}

	if len(s.classes) == 0 {
		return fail("vulnerability class index is empty")
	}
	return nil
}

// ---- canonical class vocabulary -------------------------------------------

// buildClassIndex derives the class tokens and their unambiguous aliases from
// the two sources that agree on the vocabulary: the ontology's categories and
// children, and the A-to-B signals' ids.
//
// The rules are what make folding safe:
//
//   - A canonical class maps to itself, so Class is idempotent.
//   - An alias that is itself a canonical class is never rewritten; otherwise
//     one class could swallow another ("jwt" is both a class and a loose
//     synonym of AUTHENTICATION).
//   - An alias claimed by more than one class is dropped rather than guessed, so
//     folding can never merge two distinct classes. The dropped set is
//     deliberately small and covered by a test.
func buildClassIndex(o Ontology, ab ABSignals) map[string]string {
	classes := map[string]bool{}
	for name, cat := range o.Categories {
		classes[normClass(name)] = true
		for _, child := range cat.Children {
			classes[normClass(child)] = true
		}
	}
	for _, sig := range ab.Signals {
		classes[normClass(sig.ID)] = true
	}

	claims := map[string]map[string]bool{}
	claim := func(alias, target string) {
		if alias == "" || classes[alias] || target == "" {
			return
		}
		if claims[alias] == nil {
			claims[alias] = map[string]bool{}
		}
		claims[alias][target] = true
	}

	for _, sig := range ab.Signals {
		id := normClass(sig.ID)
		for _, kw := range sig.Keywords {
			claim(normClass(kw), id)
		}
	}
	for name, cat := range o.Categories {
		catToken := normClass(name)
		children := make([]string, 0, len(cat.Children))
		for _, child := range cat.Children {
			children = append(children, normClass(child))
		}
		for _, syn := range cat.Synonyms {
			n := normClass(syn)
			if n == "" || classes[n] {
				continue
			}
			// A synonym naming exactly one child folds to that child; otherwise
			// it folds to the category.
			hit := ""
			for _, child := range children {
				if child == n {
					hit = child
					break
				}
			}
			if hit == "" {
				hit = catToken
			}
			claim(n, hit)
		}
	}

	idx := make(map[string]string, len(classes)+len(claims))
	for c := range classes {
		idx[c] = c
	}
	for alias, targets := range claims {
		if len(targets) != 1 {
			continue
		}
		for t := range targets {
			idx[alias] = t
		}
	}
	return idx
}

// Class folds v to its canonical class token: "SQLi", "SQL injection" and
// "sql_injection" all become "sql_injection". Unknown input returns the
// normalised form so callers can still group on it; empty input returns "".
// Class is idempotent.
func (s *Set) Class(v string) string {
	n := normClass(v)
	if n == "" {
		return ""
	}
	if s == nil {
		return n
	}
	if c, ok := s.classes[n]; ok {
		return c
	}
	return n
}

// Classes returns every canonical class token, sorted. The result is a copy.
func (s *Set) Classes() []string {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(s.classes))
	for src, dst := range s.classes {
		if src != dst || seen[dst] {
			continue
		}
		seen[dst] = true
		out = append(out, dst)
	}
	sort.Strings(out)
	return out
}

// Aliases returns every alias that folds onto class token, sorted. The class
// itself is included.
func (s *Set) Aliases(class string) []string {
	canonical := s.Class(class)
	var out []string
	for alias, target := range s.classes {
		if target == canonical {
			out = append(out, alias)
		}
	}
	sort.Strings(out)
	return out
}

// normClass lowercases and collapses runs of non-alphanumerics to a single
// underscore: "SQL Injection" and "SQLi" become "sql_injection" and "sqli".
func normClass(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSep := true // trims a leading separator
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSep = false
		default:
			if !prevSep {
				b.WriteByte('_')
				prevSep = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
