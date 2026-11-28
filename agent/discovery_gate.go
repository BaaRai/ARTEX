package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

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
	if strings.TrimSpace(a.WordlistRun.Wordlist) == "" || a.WordlistRun.EntriesTested <= 0 {
		missing = append(missing, "(a) wordlist_run with non-empty wordlist (e.g. skills/api-recon/wordlists/api.txt) and entries_tested > 0")
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

var routeExhaustionClaimRe = regexp.MustCompile(`(?i)\b(route(?:\s+space)?\s+exhaust(?:ed|ion)?|exhaust(?:ed|ion)\s+(?:all\s+)?routes|no\s+(?:more\s+)?(?:api\s+)?routes|endpoint(?:\s+space)?\s+exhaust(?:ed|ion)?|(?:all|404)\s+(?:on\s+)?\d+\s+root\s+nouns?|wordlist\s+exhaust(?:ed|ion)?|surface\s+exhaust(?:ed|ion)?)\b`)

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

func isDiscoveryCoverageNode(n *db.Node) (DiscoveryArtifact, bool) {
	if n == nil || n.Kind != db.KindFact {
		return DiscoveryArtifact{}, false
	}
	var p struct {
		Subtype  string             `json:"subtype"`
		Artifact *DiscoveryArtifact `json:"artifact"`
	}
	if json.Unmarshal(n.Payload, &p) != nil || p.Subtype != db.FactSubtypeDiscoveryCoverage || p.Artifact == nil {
		return DiscoveryArtifact{}, false
	}
	return *p.Artifact, true
}

func hasCompleteDiscoveryCoverage(ts *db.ExplorationStore) bool {
	if ts == nil {
		return false
	}
	nodes, err := ts.ListByKind(db.KindFact, 500)
	if err != nil {
		return false
	}
	for _, n := range nodes {
		if art, ok := isDiscoveryCoverageNode(n); ok && len(art.MissingChecks()) == 0 {
			return true
		}
	}
	return false
}

func recordDiscoveryCoverageFact(ts *db.ExplorationStore, intentID int64, surface string, art DiscoveryArtifact) (int64, error) {
	if ts == nil {
		return 0, nil
	}
	payload := map[string]any{
		"subtype":    db.FactSubtypeDiscoveryCoverage,
		"surface":    surface,
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
	if hasCompleteDiscoveryCoverage(ts) {
		return nil
	}
	return fmt.Errorf("route space cannot be declared exhausted without a complete record_coverage discovery_artifact covering all 5 checks: (a) wordlist_run (e.g. skills/api-recon/wordlists/api.txt), (b) nested_paths under discovered routes, (c) parameters_tested, (d) method_matrix, (e) static_docs_js_reviewed; a shallow 404 on root nouns is not exhaustive")
}
