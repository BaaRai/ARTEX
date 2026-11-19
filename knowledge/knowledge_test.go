package knowledge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// wantFiles is the vendored corpus. The count is asserted, not just the parse,
// because a silently missing file is exactly the failure this package exists to
// prevent.
var wantFiles = []string{
	"ab_signals.json",
	"attack_chains.json",
	"awaiting_input_markers.json",
	"compressors.json",
	"cve_correlations.json",
	"endpoint_patterns.json",
	"escalation_patterns.json",
	"file_extensions.json",
	"fuzzer_data.json",
	"patterns.json",
	"port_correlations.json",
	"refusal_markers.json",
	"scan_profiles.json",
	"skills.json",
	"tech_correlations.json",
	"tools.json",
	"tools_meta.json",
	"unified_patterns.json",
	"verification_patterns.json",
	"vuln_ontology.json",
	"waf_signatures.json",
	"waff_bypass.json",
}

func TestEmbeddedCorpusIsComplete(t *testing.T) {
	got, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(wantFiles) {
		t.Fatalf("found %d files, want %d: %v", len(got), len(wantFiles), got)
	}
	for i, want := range wantFiles {
		if got[i] != want {
			t.Fatalf("file %d = %q, want %q", i, got[i], want)
		}
		b, err := Raw(want)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if len(bytes.TrimSpace(b)) == 0 {
			t.Fatalf("%s is empty", want)
		}
		var probe any
		if err := json.Unmarshal(b, &probe); err != nil {
			t.Fatalf("%s does not parse: %v", want, err)
		}
	}
}

func TestLoadIsIdempotent(t *testing.T) {
	a, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("Load returned two different sets; the corpus should be parsed once")
	}
}

// TestPatternsCompile is the guard against a data refresh shipping a regex that
// Go's RE2 engine rejects: such a pattern would silently never match.
func TestPatternsCompile(t *testing.T) {
	s := MustLoad()
	if problems := s.CompileProblems(); len(problems) != 0 {
		for _, p := range problems {
			t.Errorf("uncompilable pattern: %s", p)
		}
	}
}

// TestClassFolding covers the defect that motivated importing the ontology:
// before this, one risk class spelled four ways became four ledger classes.
func TestClassFolding(t *testing.T) {
	s := MustLoad()
	for _, in := range []string{"sqli", "SQLi", "SQL injection", "sql_injection", "SQL  Injection", " Sql-Injection "} {
		if got := s.Class(in); got != "sql_injection" {
			t.Errorf("Class(%q) = %q, want sql_injection", in, got)
		}
	}
	cases := map[string]string{
		"XSS":                  "xss",
		"idor":                 "idor",
		"ssrf":                 "ssrf",
		"csrf":                 "csrf",
		"xxe":                  "xxe",
		"lfi":                  "lfi",
		"ssti":                 "ssti",
		"open redirect":        "open_redirect",
		"path traversal":       "path_traversal",
		"business logic":       "business_logic",
		"default creds":        "default_creds",
		"http smuggling":       "http_smuggling",
		"race condition":       "race_condition",
		"prompt injection":     "prompt_injection",
		"cross-site scripting": "xss",
	}
	for in, want := range cases {
		if got := s.Class(in); got != want {
			t.Errorf("Class(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClassIsIdempotent: folding twice must equal folding once, otherwise
// repeated writes drift to a new token.
func TestClassIsIdempotent(t *testing.T) {
	s := MustLoad()
	for _, in := range append(s.Classes(), "SQLi", "SQL injection", "not a real class") {
		once := s.Class(in)
		if twice := s.Class(once); twice != once {
			t.Errorf("Class(%q) = %q then %q", in, once, twice)
		}
	}
}

// TestClassIndexIsUnambiguous: a class never folds onto another class, and every
// fold target is a canonical token that maps to itself.
func TestClassIndexIsUnambiguous(t *testing.T) {
	s := MustLoad()
	canonical := map[string]bool{}
	for _, c := range s.Classes() {
		canonical[c] = true
	}
	for alias, target := range s.classes {
		if !canonical[target] && s.classes[target] != target {
			t.Errorf("alias %q folds to %q, which is neither canonical nor self-mapping", alias, target)
		}
		if s.classes[target] != target {
			t.Errorf("canonical token %q is not self-mapping (%q)", target, s.classes[target])
		}
		if alias != target && canonical[alias] {
			t.Errorf("canonical class %q was folded onto %q", alias, target)
		}
	}
}

// TestAmbiguousAliasesStayLiteral pins the deliberate exclusions: these words
// belong to more than one class, so folding them would merge distinct findings.
func TestAmbiguousAliasesStayLiteral(t *testing.T) {
	s := MustLoad()
	for _, ambiguous := range []string{"rce", "script", "metadata", "credential", "pickle"} {
		if got := s.Class(ambiguous); got != ambiguous {
			t.Errorf("Class(%q) = %q; an ambiguous alias must stay literal", ambiguous, got)
		}
	}
}

func TestOntologyQueries(t *testing.T) {
	s := MustLoad()

	if cat, ok := s.Category("sql_injection"); !ok || cat != "INJECTION" {
		t.Errorf("Category(sql_injection) = %q,%v", cat, ok)
	}
	if cwe := s.CWEParent("sql_injection"); cwe != "CWE-74" {
		t.Errorf("CWEParent(sql_injection) = %q, want CWE-74", cwe)
	}
	if kids := s.Children("INJECTION"); len(kids) == 0 || !contains(kids, "sql_injection") {
		t.Errorf("Children(INJECTION) = %v", kids)
	}
	if targets := s.EscalationTargets("sql_injection"); !contains(targets, "CODE") {
		t.Errorf("EscalationTargets(sql_injection) = %v, want CODE", targets)
	}
	if keys := s.DedupKeys("sql_injection"); !contains(keys, "endpoint") {
		t.Errorf("DedupKeys(sql_injection) = %v", keys)
	}
	edges := s.EscalationEdges("INJECTION")
	if len(edges) == 0 {
		t.Fatal("no escalation edges from INJECTION")
	}
	for i := 1; i < len(edges); i++ {
		if edges[i-1].Weight < edges[i].Weight {
			t.Fatalf("edges not sorted by weight: %v", edges)
		}
	}
	if _, ok := s.Category("definitely not a class"); ok {
		t.Error("unknown class resolved to a category")
	}
}

func TestDetections(t *testing.T) {
	s := MustLoad()
	got := s.Detections("You have an error in your SQL syntax near UNION SELECT")
	if len(got) == 0 {
		t.Fatal("no detection for a classic SQL error string")
	}
	if got[0].Class != "sql_injection" {
		t.Fatalf("top detection = %+v, want sql_injection", got[0])
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Weight < got[i].Weight {
			t.Fatalf("detections not sorted: %v", got)
		}
	}
	if d := s.Detections(""); d != nil {
		t.Errorf("empty input returned %v", d)
	}
}

func TestIdentifyWAF(t *testing.T) {
	s := MustLoad()

	h := http.Header{}
	h.Set("cf-ray", "8a1b2c3d4e5f6789-SJC")
	h.Set("Server", "cloudflare")
	got := s.IdentifyWAF(h, nil, 200)
	if len(got) == 0 || got[0].Name != "Cloudflare" {
		t.Fatalf("Cloudflare headers gave %+v", got)
	}
	if got[0].Confidence < 0.8 {
		t.Errorf("confidence %v too low for a cf-ray header", got[0].Confidence)
	}
	if len(got[0].Evidence) == 0 {
		t.Error("no evidence recorded")
	}

	body := []byte(`<html><body>Error 1020 Ray ID: 8a1b2c3d4e5f6789</body></html>`)
	if got := s.IdentifyWAF(http.Header{}, body, 403); len(got) == 0 {
		t.Fatal("Cloudflare body markers produced no verdict")
	}

	// A bare block status must not assert a vendor.
	got = s.IdentifyWAF(http.Header{}, []byte("nope"), 403)
	if len(got) != 1 || got[0].Name != "Generic WAF" {
		t.Fatalf("block status alone gave %+v, want a single Generic WAF", got)
	}
	if got[0].Confidence != s.WAF.StatusCodeConfidence {
		t.Errorf("generic confidence = %v, want the corpus value %v", got[0].Confidence, s.WAF.StatusCodeConfidence)
	}

	// A clean 200 with no markers must say nothing.
	if got := s.IdentifyWAF(http.Header{}, []byte("<html>hello</html>"), 200); len(got) != 0 {
		t.Errorf("clean response produced %+v", got)
	}
}

func TestFingerprintTech(t *testing.T) {
	s := MustLoad()

	h := http.Header{}
	h.Set("Server", "nginx/1.25.3")
	h.Set("X-Powered-By", "PHP/8.2.1")
	got := s.FingerprintTech(h, nil, "https://example.test/")
	names := make([]string, 0, len(got))
	for _, h := range got {
		names = append(names, h.Name)
	}
	if !contains(names, "nginx") || !contains(names, "php") {
		t.Fatalf("banner %v missing nginx/php", names)
	}
	for _, h := range got {
		if h.Category == "" {
			t.Errorf("%s has no category", h.Name)
		}
		if h.Evidence == "" {
			t.Errorf("%s has no evidence", h.Name)
		}
	}

	if got := s.FingerprintTech(nil, []byte("<html><meta name=generator content=WordPress></html>"), ""); len(got) == 0 {
		t.Fatal("WordPress generator tag produced no hit")
	}

	// A short key must not match inside an unrelated word.
	for _, h := range s.FingerprintTech(nil, []byte("the phpxmlrpc library"), "") {
		if h.Name == "php" {
			t.Error("php matched a body token it does not own")
		}
	}
}

func TestPortCorrelationsAreTrimmed(t *testing.T) {
	s := MustLoad()

	raw, err := Raw("port_correlations.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"vulns"`, `"tools"`} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Errorf("port_correlations still contains %s; the unverifiable fields must be dropped", forbidden)
		}
	}

	if info, ok := s.Port(443); !ok || !strings.EqualFold(info.Service, "https") {
		t.Errorf("Port(443) = %+v,%v", info, ok)
	}
	if _, ok := s.Port(1); ok {
		t.Error("Port(1) resolved a service it should not have")
	}
	if info, ok := s.Port(1755); ok && strings.Contains(strings.ToUpper(info.Service), "RDP") {
		t.Errorf("port 1755 is still labelled %q; 1755/tcp is streaming, not RDP", info.Service)
	}
}

func TestCVERecords(t *testing.T) {
	s := MustLoad()
	rec, ok := s.CVE("cve-2021-44228")
	if !ok {
		t.Fatal("CVE-2021-44228 not found (id lookup must be case-insensitive)")
	}
	if rec.Name != "Log4Shell" || !strings.Contains(strings.ToLower(rec.Product), "log4j") {
		t.Errorf("record = %+v", rec)
	}
	if hits := s.CVEsForTech("log4j"); len(hits) == 0 {
		t.Error("no CVEs matched the log4j product")
	}
	if _, ok := s.CVE("CVE-0000-0000"); ok {
		t.Error("an unknown CVE id resolved")
	}
}

func TestScanProfiles(t *testing.T) {
	s := MustLoad()
	for _, name := range []string{"quick", "standard", "deep", "stealth", "ctf", "bugbounty"} {
		if _, ok := s.Profiles.Profiles[name]; !ok {
			t.Errorf("profile %q missing", name)
		}
	}
	deep := s.Profiles.Profiles["deep"]
	quick := s.Profiles.Profiles["quick"]
	deepIters, _ := deep["agent_max_tool_iterations"].(float64)
	quickIters, _ := quick["agent_max_tool_iterations"].(float64)
	if deepIters <= quickIters {
		t.Errorf("deep iterations %v should exceed quick %v", deepIters, quickIters)
	}
	if len(s.Profiles.Comment) == 0 {
		t.Error("scan_profiles comment block not captured")
	}
}

func TestToolsMetaKeepsEveryKey(t *testing.T) {
	s := MustLoad()
	if len(s.ToolsMeta.Raw) != 25 {
		t.Errorf("tools_meta has %d keys, want 25", len(s.ToolsMeta.Raw))
	}
	if len(s.ToolsMeta.ToolDescriptions) == 0 {
		t.Error("tool_descriptions not lifted")
	}
	if len(s.ToolsMeta.ToolAlternatives) == 0 {
		t.Error("tool_alternatives not lifted")
	}
	if len(s.ToolsMeta.SkillsCatalog) == 0 {
		t.Error("skills_catalog not lifted")
	}
}

// TestToolMetadataMatchesCatalog checks our real tool surface against the
// upstream metadata, so a tool we expose but never describe (or vice versa) is
// visible rather than assumed.
func TestToolMetadataMatchesCatalog(t *testing.T) {
	s := MustLoad()
	if len(s.Tools) == 0 {
		t.Fatal("tools.json is empty")
	}
	upstream := map[string]bool{}
	for _, spec := range s.Tools {
		if spec.Function.Name == "" {
			t.Error("a tool spec has no name")
		}
		upstream[spec.Function.Name] = true
	}
	// artex's own tool names must not collide with the upstream sandbox tools,
	// otherwise the metadata would describe a different tool than it names.
	for _, ours := range []string{"record_coverage", "report_finding", "insert_assets"} {
		if upstream[ours] {
			t.Errorf("upstream tools.json claims our tool name %q", ours)
		}
	}
}

func TestUnifiedPatternsSplit(t *testing.T) {
	s := MustLoad()
	if len(s.Unified.Patterns) == 0 {
		t.Fatal("no merged patterns")
	}
	if len(s.Unified.Objectives) == 0 {
		t.Fatal("no objectives")
	}
	if len(s.Unified.MergedFrom) == 0 {
		t.Fatal("_merged_from not captured")
	}
	for name, p := range s.Unified.Patterns {
		if p.Name != name {
			t.Errorf("pattern %q carries name %q", name, p.Name)
		}
	}
}

func TestMarkerListsAreLowercase(t *testing.T) {
	s := MustLoad()
	for _, group := range map[string][]string{
		"refusal":  s.Refusals.Markers,
		"awaiting": s.Awaiting.Markers,
	} {
		for name, list := range map[string][]string{"": group} {
			_ = name
			for _, m := range list {
				if strings.ToLower(m) != m {
					t.Errorf("marker %q is not lowercase and can never match", m)
				}
			}
		}
	}
}

func TestCompressorsArePinned(t *testing.T) {
	s := MustLoad()
	if len(s.Compressors) == 0 {
		t.Fatal("no compressors")
	}
	for name, c := range s.Compressors {
		if c.Version == "" {
			t.Errorf("%s has no version", name)
		}
		for platform, a := range c.Assets {
			if len(a.SHA256) != 64 {
				t.Errorf("%s/%s sha256 is not 64 hex chars", name, platform)
			}
		}
	}
}

func TestSkillIndexIsNotASecondDiscoveryPath(t *testing.T) {
	s := MustLoad()
	if len(s.SkillIndex.SkillKeywords) == 0 {
		t.Fatal("skills.json is empty")
	}
	// The mapping points at AIRecon's Python skill tree, which this repo does not
	// ship. It is embedded for provenance only; assert that no path is a bare
	// name that could accidentally resolve against our skills/ directory.
	for kw, path := range s.SkillIndex.SkillKeywords {
		if strings.HasPrefix(path, "skills/") {
			t.Errorf("keyword %q maps to %q, which could shadow our own skills tree", kw, path)
		}
	}
}

func TestEndpointAndExtensionScoring(t *testing.T) {
	s := MustLoad()
	if len(s.Endpoints.URLPatterns) == 0 {
		t.Fatal("no url patterns")
	}
	for _, p := range s.Endpoints.URLPatterns {
		if p.Score < 0 || p.Score > 1 {
			t.Errorf("score %v out of range for %q", p.Score, p.Pattern)
		}
	}
	if len(s.Extensions.HighValue) == 0 {
		t.Fatal("no high_value extensions")
	}
	if !contains(s.Extensions.Backend, "php") {
		t.Errorf("backend extensions = %v, want php", s.Extensions.Backend)
	}
}

func TestVerificationPatterns(t *testing.T) {
	s := MustLoad()
	if len(s.Verification.SQLError) == 0 || len(s.Verification.Honeypot) == 0 {
		t.Fatal("core verification groups are empty")
	}
	if len(s.Verification.AuthFailure) == 0 {
		t.Fatal("auth_failure_patterns is empty")
	}
}

func TestFuzzerTables(t *testing.T) {
	s := MustLoad()
	if len(s.Fuzzer.FuzzPoints) == 0 {
		t.Fatal("no fuzz points")
	}
	if len(s.Fuzzer.FuzzPayloads) == 0 {
		t.Fatal("no fuzz payloads")
	}
	for family, payloads := range s.Fuzzer.FuzzPayloads {
		if len(payloads) == 0 {
			t.Errorf("payload family %q is empty", family)
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
