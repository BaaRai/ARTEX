package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// MinDiscoveryWordlistEntries enforces a real wordlist run (at least 50 entries)
// so a trivial 1-entry run cannot satisfy criterion (a) of DiscoveryArtifact.
const MinDiscoveryWordlistEntries = 50

// DiscoveryWordlistRun captures criterion (a): at least one curated/real
// wordlist run (such as skills/api-recon/wordlists/api.txt).
type DiscoveryWordlistRun struct {
	Wordlist      string   `json:"wordlist"`
	EntriesTested int      `json:"entries_tested"`
	Discovered    []string `json:"discovered,omitempty"`
}

// DiscoveryArtifact captures the 5 mandatory quality checks before a route space
// may be declared exhausted. A shallow 404 on N root nouns is never sufficient.
type DiscoveryArtifact struct {
	WordlistRun          DiscoveryWordlistRun `json:"wordlist_run"`            // (a) real wordlist run
	NestedPaths          []string             `json:"nested_paths"`            // (b) nested path discovery under discovered routes
	ParametersTested     []string             `json:"parameters_tested"`       // (c) parameter discovery
	MethodMatrix         []string             `json:"method_matrix"`           // (d) HTTP method matrix (GET/POST/PUT/PATCH/DELETE/OPTIONS)
	StaticDocsJSReviewed []string             `json:"static_docs_js_reviewed"` // (e) static docs/JS review
}

func (a DiscoveryArtifact) MissingChecks() []string {
	var missing []string
	if strings.TrimSpace(a.WordlistRun.Wordlist) == "" || a.WordlistRun.EntriesTested < MinDiscoveryWordlistEntries {
		missing = append(missing, fmt.Sprintf("(a) wordlist_run with non-empty wordlist (e.g. skills/api-recon/wordlists/api.txt) and entries_tested >= %d", MinDiscoveryWordlistEntries))
	}
	if len(nonEmptyTrimmed(a.NestedPaths)) == 0 {
		missing = append(missing, "(b) nested_paths under discovered routes")
	}
	if len(nonEmptyTrimmed(a.ParametersTested)) == 0 {
		missing = append(missing, "(c) parameters_tested (parameter discovery)")
	}
	if len(nonEmptyTrimmed(a.MethodMatrix)) == 0 {
		missing = append(missing, "(d) method_matrix (HTTP method matrix)")
	}
	if len(nonEmptyTrimmed(a.StaticDocsJSReviewed)) == 0 {
		missing = append(missing, "(e) static_docs_js_reviewed (static docs/JS review)")
	}
	return missing
}

func nonEmptyTrimmed(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (a DiscoveryArtifact) CompactSummary() string {
	return fmt.Sprintf("wordlist=%s(%d) nested=%d params=%d methods=%s docs_js=%d",
		strings.TrimSpace(a.WordlistRun.Wordlist),
		a.WordlistRun.EntriesTested,
		len(nonEmptyTrimmed(a.NestedPaths)),
		len(nonEmptyTrimmed(a.ParametersTested)),
		strings.Join(nonEmptyTrimmed(a.MethodMatrix), ","),
		len(nonEmptyTrimmed(a.StaticDocsJSReviewed)),
	)
}

var (
	routeExhaustionClaimRe = regexp.MustCompile(`(?i)\b(route(?:\s+space)?\s+exhaust(?:ed|ion)?|exhaust(?:ed|ion)\s+(?:all\s+)?routes|no\s+(?:more\s+)?(?:api\s+)?routes|endpoint(?:\s+space)?\s+exhaust(?:ed|ion)?|(?:all|404)\s+(?:on\s+)?\d+\s+root\s+nouns?|wordlist\s+exhaust(?:ed|ion)?|surface\s+exhaust(?:ed|ion)?)\b`)
	dottedHostTokenRe      = regexp.MustCompile(`(?i)\b([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+)\b`)
)

func isRouteExhaustionClaim(text string) bool {
	return routeExhaustionClaimRe.MatchString(text)
}

func isRouteDiscoveryRiskArea(riskArea string) bool {
	r := strings.ToLower(strings.TrimSpace(riskArea))
	switch r {
	case "route_discovery", "api_routes", "routes", "endpoints", "surface_discovery", "route_enumeration", "endpoint_enumeration":
		return true
	default:
		return strings.Contains(r, "route_discovery") || strings.Contains(r, "endpoint_discovery") || strings.Contains(r, "surface_discovery")
	}
}

func normalizeDiscoverySurfaceHost(surface string) string {
	s := strings.TrimSpace(strings.ToLower(surface))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		}
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if host, _, ok := strings.Cut(s, ":"); ok && !strings.Contains(host, "]") {
		s = host
	}
	return strings.TrimSuffix(s, ".")
}

func isLikelyNonHostFilename(tok string) bool {
	for _, ext := range []string{
		".txt", ".js", ".mjs", ".ts", ".json", ".yaml", ".yml", ".xml",
		".html", ".htm", ".css", ".png", ".jpg", ".svg", ".go", ".py",
		".sh", ".md", ".csv", ".log", ".pdf",
	} {
		if strings.HasSuffix(tok, ext) {
			return true
		}
	}
	return false
}

func extractDiscoveryClaimHosts(text string) []string {
	seen := map[string]bool{}
	var hosts []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	for _, raw := range probeURLRe.FindAllString(text, 16) {
		if u, err := url.Parse(strings.TrimRight(raw, `.,;:'")`)); err == nil && u.Hostname() != "" {
			add(u.Hostname())
		}
	}
	for _, m := range dottedHostTokenRe.FindAllStringSubmatch(text, 16) {
		if len(m) == 2 {
			tok := strings.ToLower(m[1])
			if !isLikelyNonHostFilename(tok) {
				add(tok)
			}
		}
	}
	return hosts
}

type storedDiscoveryCoverage struct {
	surface string
	host    string
	art     DiscoveryArtifact
}

func isDiscoveryCoverageNode(n *db.Node) (DiscoveryArtifact, bool) {
	sc, ok := decodeDiscoveryCoverageNode(n)
	if !ok {
		return DiscoveryArtifact{}, false
	}
	return sc.art, true
}

func decodeDiscoveryCoverageNode(n *db.Node) (storedDiscoveryCoverage, bool) {
	if n == nil || n.Kind != db.KindFact {
		return storedDiscoveryCoverage{}, false
	}
	var p struct {
		Subtype  string             `json:"subtype"`
		Surface  string             `json:"surface"`
		Host     string             `json:"host"`
		Artifact *DiscoveryArtifact `json:"artifact"`
	}
	if json.Unmarshal(n.Payload, &p) != nil || p.Subtype != db.FactSubtypeDiscoveryCoverage || p.Artifact == nil {
		return storedDiscoveryCoverage{}, false
	}
	host := strings.ToLower(strings.TrimSpace(p.Host))
	if host == "" {
		host = normalizeDiscoverySurfaceHost(p.Surface)
	}
	return storedDiscoveryCoverage{
		surface: strings.ToLower(strings.TrimSpace(p.Surface)),
		host:    host,
		art:     *p.Artifact,
	}, true
}

func hasCompleteDiscoveryCoverage(ts *db.ExplorationStore) bool {
	return hasCompleteDiscoveryCoverageForClaim(ts, "")
}

func hasCompleteDiscoveryCoverageForClaim(ts *db.ExplorationStore, claimText string) bool {
	if ts == nil {
		return false
	}
	nodes, err := ts.ListByKind(db.KindFact, 500)
	if err != nil {
		return false
	}
	var complete []storedDiscoveryCoverage
	for _, n := range nodes {
		if sc, ok := decodeDiscoveryCoverageNode(n); ok && len(sc.art.MissingChecks()) == 0 {
			complete = append(complete, sc)
		}
	}
	if len(complete) == 0 {
		return false
	}

	claimHosts := extractDiscoveryClaimHosts(claimText)
	if len(claimHosts) == 0 {
		desc, goal, _ := ts.Root()
		claimHosts = extractDiscoveryClaimHosts(desc + " " + goal)
	}
	if len(claimHosts) == 0 {
		return true
	}

	claimLower := strings.ToLower(strings.TrimSpace(claimText))
	for _, targetHost := range claimHosts {
		var matched bool
		for _, sc := range complete {
			if sc.host != "" && sc.host == targetHost {
				matched = true
				break
			}
			if sc.surface != "" && (sc.surface == targetHost || strings.Contains(claimLower, sc.surface)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func recordDiscoveryCoverageFact(ts *db.ExplorationStore, intentID int64, surface string, art DiscoveryArtifact) (int64, error) {
	if ts == nil {
		return 0, nil
	}
	host := normalizeDiscoverySurfaceHost(surface)
	payload := map[string]any{
		"subtype":    db.FactSubtypeDiscoveryCoverage,
		"surface":    surface,
		"host":       host,
		"artifact":   art,
		"summary":    fmt.Sprintf("Discovery quality coverage complete for %s (%s)", surface, art.CompactSummary()),
		"source":     "tool_output",
		"confidence": "observed",
		"evidence":   art.CompactSummary(),
	}
	id, err := ts.AddNode(db.KindFact, payload, 0, "confirmed", "system", nil)
	if err != nil {
		return 0, err
	}
	if intentID > 0 {
		_ = ts.Link(intentID, db.RelYields, id)
	}
	return id, nil
}

func missingDiscoveryCoverageError(ts *db.ExplorationStore) error {
	return missingDiscoveryCoverageErrorForClaim(ts, "")
}

func missingDiscoveryCoverageErrorForClaim(ts *db.ExplorationStore, claimText string) error {
	if hasCompleteDiscoveryCoverageForClaim(ts, claimText) {
		return nil
	}
	hosts := extractDiscoveryClaimHosts(claimText)
	scopeNote := ""
	if len(hosts) > 0 {
		scopeNote = fmt.Sprintf(" matching target host/surface %s", strings.Join(hosts, ", "))
	}
	return fmt.Errorf("route space cannot be declared exhausted without a complete record_coverage discovery_artifact%s covering all 5 checks: (a) wordlist_run (e.g. skills/api-recon/wordlists/api.txt, entries_tested >= %d), (b) nested_paths under discovered routes, (c) parameters_tested, (d) method_matrix, (e) static_docs_js_reviewed; a shallow 404 on root nouns is not exhaustive", scopeNote, MinDiscoveryWordlistEntries)
}
