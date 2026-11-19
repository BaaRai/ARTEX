package knowledge

import (
	"bytes"
	"encoding/json"
)

// One struct family per embedded file. Field names and JSON tags follow the
// upstream data verbatim so a re-vendored file decodes without a mapping layer.
//
// Files whose shape is open-ended (tools_meta, unified_patterns, scan_profiles)
// decode through a custom UnmarshalJSON in knowledge.go that keeps the known
// keys typed and the rest as json.RawMessage, so nothing is guessed at.

// ---- vuln_ontology.json ----------------------------------------------------

// Ontology is the canonical vulnerability vocabulary: CWE-parented categories,
// their children, synonyms, weighted regex signals and the escalation graph.
type Ontology struct {
	Comment    string                      `json:"_comment"`
	Version    string                      `json:"_version"`
	Design     string                      `json:"_design"`
	Learning   string                      `json:"_learning"`
	Categories map[string]OntologyCategory `json:"categories"`
	Escalation OntologyEscalation          `json:"escalation_graph"`
	DedupRules DedupRules                  `json:"dedup_rules"`
}

// OntologyCategory is one CWE-parented vulnerability category.
type OntologyCategory struct {
	Parent            string        `json:"parent"`
	Children          []string      `json:"children"`
	Synonyms          []string      `json:"synonyms"`
	RegexSignals      []RegexSignal `json:"regex_signals"`
	EscalationTargets []string      `json:"escalation_targets"`
	DedupKeyFields    []string      `json:"dedup_key_fields"`
}

// RegexSignal is one weighted detection pattern mapping text to a class label.
type RegexSignal struct {
	Pattern string  `json:"pattern"`
	Label   string  `json:"label"`
	Weight  float64 `json:"weight"`
}

// OntologyEscalation holds the category-to-category escalation graph.
type OntologyEscalation struct {
	Edges []OntologyEdge `json:"edges"`
}

// OntologyEdge is one directed escalation with a weight and rationale.
type OntologyEdge struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Weight      float64 `json:"weight"`
	Description string  `json:"description"`
}

// DedupRules bounds how often the same finding may be retested.
type DedupRules struct {
	SemanticThreshold           float64 `json:"semantic_threshold"`
	AllowRetestOnEvidenceGrowth bool    `json:"allow_retest_on_evidence_growth"`
	MaxRetestsWithoutGrowth     int     `json:"max_retests_without_growth"`
	VariantTracking             bool    `json:"variant_tracking"`
}

// ---- waf_signatures.json ---------------------------------------------------

// WAFSignatures is the confidence-scored WAF fingerprint corpus.
type WAFSignatures struct {
	Comment              string               `json:"_comment"`
	HeaderSignatures     []WAFHeaderSignature `json:"header_signatures"`
	BodySignatures       []WAFBodySignature   `json:"body_signatures"`
	BlockStatusCodes     []int                `json:"block_status_codes"`
	StatusCodeConfidence float64              `json:"status_code_confidence"`
}

// WAFHeaderSignature matches a response header, optionally by value pattern.
type WAFHeaderSignature struct {
	Header     string  `json:"header"`
	Pattern    string  `json:"pattern"`
	WAF        string  `json:"waf"`
	Confidence float64 `json:"confidence"`
}

// WAFBodySignature matches a response body pattern.
type WAFBodySignature struct {
	Pattern    string  `json:"pattern"`
	WAF        string  `json:"waf"`
	Confidence float64 `json:"confidence"`
}

// ---- tech_correlations.json ------------------------------------------------

// TechProfile is what the corpus knows about one technology.
type TechProfile struct {
	Vulns    []string `json:"vulns"`
	Paths    []string `json:"paths"`
	Tools    []string `json:"tools"`
	Severity string   `json:"severity"`
	Category string   `json:"category"`
}

// ---- port_correlations.json ------------------------------------------------

// PortCorrelation is one port's verified profile.
//
// Upstream also carried vulns[] and tools[]; both were dropped on import because
// they contained misattributed CVEs and binaries this image does not ship. See
// docs/skill-sources.md. Code must not reintroduce either field.
type PortCorrelation struct {
	Service  string `json:"service"`
	Severity string `json:"severity"`
	Notes    string `json:"notes"`
}

// ---- cve_correlations.json -------------------------------------------------

// CVE is one CVE record, keyed by CVE id in the embedded map.
type CVE struct {
	Name        string   `json:"name"`
	Product     string   `json:"product"`
	Severity    string   `json:"severity"`
	Description string   `json:"description"`
	Targets     []string `json:"targets"`
}

// ---- endpoint_patterns.json ------------------------------------------------

// EndpointPatterns scores discovered URLs and subdomains by attack value.
type EndpointPatterns struct {
	Meta              EndpointMeta      `json:"_meta"`
	URLPatterns       []EndpointPattern `json:"url_patterns"`
	SubdomainPatterns []EndpointPattern `json:"subdomain_patterns"`
	StaticDirPatterns []string          `json:"static_dir_patterns"`
}

// EndpointMeta is the file's own versioning/description block.
type EndpointMeta struct {
	Version     string `json:"version"`
	Description string `json:"description"`
	Scoring     string `json:"scoring"`
}

// EndpointPattern is one scored pattern with the attacks it implies.
type EndpointPattern struct {
	Pattern       string   `json:"pattern"`
	Score         float64  `json:"score"`
	Category      string   `json:"category"`
	AttackVectors []string `json:"attack_vectors"`
	Description   string   `json:"description"`
}

// ---- verification_patterns.json --------------------------------------------

// VerificationPatterns is the noise/false-positive filter corpus: the strings
// that tell a real hit from a CDN error page, a honeypot or a reflected redirect.
type VerificationPatterns struct {
	AuthFailure      map[string]string `json:"auth_failure_patterns"`
	LoginForm        map[string]string `json:"login_form_patterns"`
	CleanResponse    []string          `json:"clean_response_patterns"`
	DynamicContent   []string          `json:"dynamic_content_markers"`
	Honeypot         []string          `json:"honeypot_indicators"`
	WAFBlock         []string          `json:"waf_block_indicators"`
	LFIFile          []string          `json:"lfi_file_indicators"`
	SQLError         []string          `json:"sql_error_patterns"`
	FalsePositive    []string          `json:"false_positive_indicators"`
	URLEncodingCheck []string          `json:"url_encoding_checks"`
}

// ---- file_extensions.json --------------------------------------------------

// FileExtensions buckets extensions by what they mean to a tester.
type FileExtensions struct {
	Static    []string `json:"static"`
	HighValue []string `json:"high_value"`
	Document  []string `json:"document"`
	Backend   []string `json:"backend"`
}

// ---- fuzzer_data.json ------------------------------------------------------

// FuzzerData holds the parameter, payload and chain tables that drive fuzzing.
type FuzzerData struct {
	FuzzPoints          []string                       `json:"FUZZ_POINTS"`
	FuzzPayloads        map[string][]string            `json:"FUZZ_PAYLOADS"`
	VulnerablePatterns  map[string][]string            `json:"VULNERABLE_PATTERNS"`
	WAFSignatures       map[string]FuzzerWAF           `json:"WAF_SIGNATURES"`
	ChainRules          map[string][]string            `json:"CHAIN_RULES"`
	ChainPayloads       map[string][]string            `json:"CHAIN_PAYLOADS"`
	SeverityOrder       []string                       `json:"_SEVERITY_ORDER"`
	ParamTypeMap        map[string][]string            `json:"PARAM_TYPE_MAP"`
	TechPayloadAugments map[string]map[string][]string `json:"TECH_PAYLOAD_AUGMENTS"`
	PriorityParamHints  []PriorityParamHint            `json:"PRIORITY_PARAM_HINTS"`
	ChainFamilyFollowOn []ChainFamily                  `json:"CHAIN_FAMILY_FOLLOWONS"`
}

// FuzzerWAF is the coarse WAF fingerprint the fuzzer uses.
type FuzzerWAF struct {
	Headers      []string `json:"headers"`
	BodyPatterns []string `json:"body_patterns"`
	StatusCodes  []int    `json:"status_codes"`
}

// PriorityParamHint suggests parameters worth trying for a URL keyword.
type PriorityParamHint struct {
	URLKeywords []string `json:"url_keywords"`
	Params      []string `json:"params"`
}

// ChainFamily groups finding classes that share follow-on classes.
type ChainFamily struct {
	Members   []string `json:"members"`
	FollowOns []string `json:"follow_ons"`
}

// ---- patterns.json ---------------------------------------------------------

// BehaviorPatterns is patterns.json: behaviour rules plus one operational table
// that upstream files under the same object.
//
// The file is not uniform. Thirty-five keys are rules of the form
// {description, indicators, suggested_actions, severity}; the thirty-sixth,
// "waf_bypass_strategies", is a map of WAF name -> technique list. Folding that
// into the rule map would mean claiming it has a description and indicators it
// does not have, so the decoder splits the two by key name.
type BehaviorPatterns struct {
	Rules     map[string]BehaviorPattern `json:"-"`
	WAFBypass map[string][]string        `json:"-"`
}

// BehaviorPattern is one "this surface implies that test" rule.
type BehaviorPattern struct {
	Description      string   `json:"description"`
	Indicators       []string `json:"indicators"`
	SuggestedActions []string `json:"suggested_actions"`
	Severity         string   `json:"severity"`
}

// ---- ab_signals.json -------------------------------------------------------

// ABSignals is the A-to-B promotion table: evidence keywords that should move a
// goal on to the next objective.
type ABSignals struct {
	Comment     string     `json:"_comment"`
	MinSeverity int        `json:"min_severity"`
	Signals     []ABSignal `json:"signals"`
}

// ABSignal is one keyword-to-follow-up promotion rule.
type ABSignal struct {
	ID               string           `json:"id"`
	OWASPID          string           `json:"owasp_id"`
	Severity         string           `json:"severity"`
	Keywords         []string         `json:"keywords"`
	NegativeKeywords []string         `json:"negative_keywords"`
	Followup         []string         `json:"followup"`
	TestVectors      []TestVector     `json:"test_vectors"`
	ChainWith        []string         `json:"chain_with"`
	KillConditions   []string         `json:"kill_conditions"`
	ObjectivePatches []ObjectivePatch `json:"objective_patches"`
}

// TestVector is one labelled payload.
type TestVector struct {
	Label   string `json:"label"`
	Payload string `json:"payload"`
}

// ObjectivePatch is a goal patch the upstream loop would apply.
type ObjectivePatch struct {
	Op       string `json:"op"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

// ---- attack_chains.json ----------------------------------------------------

// AttackChains lists the multi-step escalation playbooks.
type AttackChains struct {
	Comment     string        `json:"_comment"`
	MinSeverity int           `json:"min_severity"`
	Chains      []AttackChain `json:"chains"`
}

// AttackChain is one ordered escalation playbook.
type AttackChain struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	Triggers         []string     `json:"triggers"`
	Steps            []AttackStep `json:"steps"`
	RequiredFindings []string     `json:"required_findings"`
	Severity         string       `json:"severity"`
	EstimatedTime    string       `json:"estimated_time"`
	DetectionRisk    string       `json:"detection_risk"`
	Prerequisites    []string     `json:"prerequisites"`
	PostExploitation []string     `json:"post_exploitation"`
}

// AttackStep is one ordered step of a chain.
type AttackStep struct {
	Order           int    `json:"order"`
	Description     string `json:"description"`
	ToolHint        string `json:"tool_hint"`
	SuccessCriteria string `json:"success_criteria"`
}

// ---- escalation_patterns.json ----------------------------------------------

// EscalationPatterns is the finding-to-finding escalation table.
type EscalationPatterns struct {
	Comment         string                          `json:"_comment"`
	Version         string                          `json:"_version"`
	Patterns        []EscalationPattern             `json:"patterns"`
	LearnedPatterns []EscalationPattern             `json:"_learned_patterns"`
	Templates       map[string][]EscalationTemplate `json:"templates"`
}

// EscalationPattern is one "from these classes, try this objective next" rule.
type EscalationPattern struct {
	ID          string   `json:"id"`
	From        []string `json:"from"`
	To          string   `json:"to"`
	Description string   `json:"description"`
	Probability float64  `json:"probability"`
}

// EscalationTemplate is one templated next step for a target class.
type EscalationTemplate struct {
	Step     string `json:"step"`
	ToolHint string `json:"tool_hint"`
}

// ---- scan_profiles.json ----------------------------------------------------

// ScanProfiles maps a profile name to the config overrides it applies. Decoded
// by custom UnmarshalJSON because the file mixes a "_comment" list with
// profile-keyed maps of mixed value types.
type ScanProfiles struct {
	Comment  []string                    `json:"_comment"`
	Profiles map[string]ProfileOverrides `json:"-"`
}

// ProfileOverrides is one profile's config-key overrides. Values stay any: the
// keys are AIRecon's config keys, not ours, so typing them would invent a schema
// we do not own.
type ProfileOverrides map[string]any

// ---- refusal_markers / awaiting_input_markers ------------------------------

// Markers is the shared shape of the refusal and awaiting-input marker lists.
type Markers struct {
	Comment string   `json:"_comment"`
	Markers []string `json:"markers"`
}

// ---- compressors.json ------------------------------------------------------

// Compressors describes the pinned output-compressor releases.
type Compressors map[string]Compressor

// Compressor is one pinned release with its per-platform assets.
type Compressor struct {
	Version string                     `json:"version"`
	License string                     `json:"license"`
	Assets  map[string]CompressorAsset `json:"assets"`
}

// CompressorAsset is one downloadable archive with its checksum.
type CompressorAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// ---- tools.json ------------------------------------------------------------

// ToolSpec is one upstream LLM tool definition (OpenAI function-calling shape).
type ToolSpec struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is a tool spec's name, prose and parameter schema.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ---- tools_meta.json -------------------------------------------------------

// ToolsMeta holds the upstream agent's tool metadata. The file's 25 keys have
// unrelated shapes, so it decodes into Raw plus a handful of typed convenience
// views; a consumer that needs another key calls Raw[key] rather than waiting
// for a struct field.
type ToolsMeta struct {
	Raw               map[string]json.RawMessage   `json:"-"`
	Categories        map[string]ToolCategory      `json:"-"`
	Parallelizable    []string                     `json:"-"`
	ToolAlternatives  map[string]string            `json:"-"`
	ToolDescriptions  map[string]string            `json:"-"`
	SkillsCatalog     map[string]map[string]string `json:"-"`
	ToolFlagConflicts map[string]ToolFlagConflict  `json:"-"`
	PhaseSequence     []string                     `json:"-"`
}

// ToolCategory is one upstream tool category.
type ToolCategory struct {
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

// ToolFlagConflict records flags a tool does not accept and the tool that does.
type ToolFlagConflict struct {
	Flags       []string `json:"flags"`
	CorrectTool string   `json:"correct_tool"`
}

// ---- unified_patterns.json -------------------------------------------------

// UnifiedPatterns merges the six upstream pattern files, plus the objective
// indicator table and one nested detection section that is not a pattern.
type UnifiedPatterns struct {
	Comment    string                    `json:"_comment"`
	Version    string                    `json:"_version"`
	MergedFrom []string                  `json:"_merged_from"`
	Objectives map[string]Objective      `json:"objectives"`
	Detection  VulnDetection             `json:"vuln_detection_patterns"`
	Patterns   map[string]UnifiedPattern `json:"-"`
}

// VulnDetection is the nested detection section of unified_patterns: the class
// regexes and result keywords the upstream semantic loop used. It is not a
// behaviour pattern, so it is kept out of the Patterns map.
type VulnDetection struct {
	Comment         string                       `json:"_comment"`
	CommandPatterns map[string][]string          `json:"command_patterns"`
	ResultKeywords  map[string][]string          `json:"result_keywords"`
	ToolVulnMap     map[string]string            `json:"tool_vuln_map"`
	TestVariants    map[string]map[string]string `json:"test_variants"`
}

// Objective is one testing objective and the text that indicates it.
type Objective struct {
	Description string   `json:"description"`
	Indicators  []string `json:"indicators"`
}

// UnifiedPattern is one merged behaviour rule.
type UnifiedPattern struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	Indicators  []string `json:"indicators"`
	Severity    string   `json:"severity"`
	Actions     []string `json:"actions"`
}

// ---- waff_bypass.json ------------------------------------------------------

// WAFBypass is the WAF-bypass policy document. It is operational policy rather
// than detection data, so only the bypass strategies are typed; the policy
// blocks keep their upstream JSON because they mix scalars, lists and nested
// objects under one key and inventing a Go type for them would misrepresent the
// data.
type WAFBypass struct {
	Comment              string                      `json:"_comment"`
	SchemaVersion        string                      `json:"schema_version"`
	ProfileType          string                      `json:"profile_type"`
	Intent               string                      `json:"intent"`
	Description          string                      `json:"description"`
	BypassStrategies     map[string][]BypassStrategy `json:"BYPASS_STRATEGIES"`
	AuditFields          []string                    `json:"AUDIT_FIELDS"`
	WAFPatterns          json.RawMessage             `json:"WAF_PATTERNS"`
	DetectionScoring     json.RawMessage             `json:"DETECTION_SCORING"`
	ChallengeClassifiers json.RawMessage             `json:"CHALLENGE_CLASSIFIERS"`
	SafeTestPolicy       json.RawMessage             `json:"SAFE_TEST_POLICY"`
	Observability        json.RawMessage             `json:"OBSERVABILITY"`
	Reporting            json.RawMessage             `json:"REPORTING"`
}

// BypassStrategy is one ordered, bounded bypass attempt against a named WAF.
type BypassStrategy struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Priority    int                     `json:"priority"`
	MaxAttempts int                     `json:"max_attempts"`
	CooldownMS  int                     `json:"cooldown_ms"`
	Headers     map[string]HeaderValues `json:"headers"`
}

// HeaderValues is one header's candidate values.
//
// The corpus writes a single-value header as a bare string and a multi-value
// header as an array, so the decoder accepts either rather than forcing the
// data to be normalised on import.
type HeaderValues []string

// UnmarshalJSON accepts both `"value"` and `["a","b"]`.
func (h *HeaderValues) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*h = nil
		return nil
	}
	if trimmed[0] == '[' {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		*h = list
		return nil
	}
	var one string
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return err
	}
	*h = []string{one}
	return nil
}

// ---- skills.json -----------------------------------------------------------

// Skills maps a discovery keyword to an upstream skill path.
//
// Embedded for provenance and completeness only: artex discovers skills through
// SKILL.md frontmatter plus builtinSkillVisibility, and this table must never
// become a second discovery path.
type Skills struct {
	SkillKeywords map[string]string `json:"skill_keywords"`
}
