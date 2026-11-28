package db

// Exploration node kinds (exploration_nodes.kind).
const (
	KindBegin   = "begin"   // DEPRECATED: legacy task root; new tasks seed an origin fact (KindFact + StateOrigin) instead
	KindGoal    = "goal"    // a task objective
	KindIntent  = "intent"  // an exploration direction (planner-generated)
	KindFact    = "fact"    // a worker's exploration result/conclusion (incl. negative results), tied to its intent
	KindFinding = "finding" // a confirmed vulnerability (report_finding), distinct from a fact
	KindHint    = "hint"
	KindDigest  = "digest" // a compressed fold of cold intents/facts (cold-digest-spec §1); lossless — members kept, restorable by id
)

// Digest node states (kind='digest'). A digest is 'active' while it renders in
// graph_overview; major compaction retires a merged-away segment to 'superseded'
// (its covers edges repointed to the new digest) — cold-digest-spec §5.1.
const (
	StateDigestActive     = "active"
	StateDigestSuperseded = "superseded"
)

// StateOrigin marks the task root fact (KindFact) seeded at task creation — the
// exploration graph's origin. Every intent traces back to it, so "an intent must
// connect to a fact" holds uniformly from the very first intent. Worker-produced
// facts use state 'confirmed', so this never collides.
const StateOrigin = "origin"

// StateIntentDeleted marks an intent soft-deleted by the user: it drops out of
// the frontier and graph_overview like other terminal states, but keeps its node
// and full lineage. The delete reason lives in exploration_nodes.delete_reason.
const StateIntentDeleted = "deleted"

// Exploration edge relations (exploration_edges.rel).
const (
	RelSpawns      = "spawns"
	RelDerivedFrom = "derived_from"
	RelYields      = "yields"
	RelProves      = "proves"
	RelCovers      = "covers" // digest --covers--> member (cold-digest-spec §1); source of truth for "which digest folds node X"
)

// FactSubtypeHypothesis tags kind=fact nodes that represent planner hypotheses
// without altering ck_node_kind (zero schema migration).
const FactSubtypeHypothesis = "hypothesis"

// FactSubtypeCapability tags kind=fact nodes that represent capabilities (#613)
// without altering ck_node_kind (zero schema migration).
const FactSubtypeCapability = "capability"

// FactSubtypeGateConstant tags kind=fact nodes recording repeated constant HTTP
// responses across distinct probe inputs (zero schema migration).
const FactSubtypeGateConstant = "gate_constant"

// FactSubtypeTargetFingerprint tags kind=fact nodes capturing the initial
// read-only baseline fingerprint of a task's goal URL at task creation.
const FactSubtypeTargetFingerprint = "target_fingerprint"

// FactSubtypeDiscoveryCoverage tags kind=fact nodes storing a verified
// 5-part discovery quality artifact written via record_coverage.
const FactSubtypeDiscoveryCoverage = "discovery_coverage"
