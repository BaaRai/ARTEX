package intercept

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// StatefulGuardEnv controls whether the per-(task, host) stateful target
	// guard is active ("on" by default; "off"/"false"/"0"/"no" disables it).
	StatefulGuardEnv = "ARTEX_STATEFUL_GUARD"
	// StatefulLeaseTTLEnv sets the heartbeat inactivity TTL in integer seconds
	// before a held mutation lease becomes stale and reclaimable (default 900).
	StatefulLeaseTTLEnv = "ARTEX_STATEFUL_LEASE_TTL_S"
	// DefaultStatefulLeaseTTL is 900s (15m): provides a 300s safety margin over
	// Norma's 600s maximum Bash tool timeout so a single legitimate long tool
	// call cannot lose its lease mid-call while a pathological hang (ignoring
	// context past 900s) still frees the host.
	//
	// Residual risk: if a subprocess ignores cancellation and remains in-flight
	// past the 900s TTL, another intent may take over the lease; any subsequent
	// mutation call from the old holder is rejected once the new holder owns it.
	DefaultStatefulLeaseTTL    = 900 * time.Second
	maxStatefulLeaseTTLSeconds = 86400 // 24h cap to prevent time.Duration overflow

	DefaultLeaseWait = 120 * time.Second
	MaxLeaseWait     = 300 * time.Second
)

// StatefulGuardEnabled reports whether the stateful target guard is enabled
// (default on; disabled only by ARTEX_STATEFUL_GUARD=off|false|0|no,
// case-insensitive).
func StatefulGuardEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(StatefulGuardEnv))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// StatefulLeaseTTL returns the mutation-lease heartbeat TTL configured via
// ARTEX_STATEFUL_LEASE_TTL_S (integer seconds), defaulting to 900s.
// Zero, negative, non-numeric, or oversized values (>86400s) fall back safely
// to DefaultStatefulLeaseTTL without disabling the guard or overflowing.
func StatefulLeaseTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(StatefulLeaseTTLEnv))
	if raw == "" {
		return DefaultStatefulLeaseTTL
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 || secs > maxStatefulLeaseTTLSeconds {
		return DefaultStatefulLeaseTTL
	}
	return time.Duration(secs) * time.Second
}

// LeaseStatus describes an active (or stale) per-host mutation lease.
type LeaseStatus struct {
	Host                    string    `json:"host"`
	HolderIntentID          int64     `json:"holder_intent_id"`
	AcquiredAt              time.Time `json:"acquired_at"`
	AgeSeconds              int64     `json:"age_seconds"`
	LastHeartbeat           time.Time `json:"last_heartbeat"`
	LastHeartbeatAgeSeconds int64     `json:"last_heartbeat_age_seconds"`
	TTLSeconds              int64     `json:"ttl_seconds"`
	Stale                   bool      `json:"stale"`
}

// WaitLeaseResult is returned by WaitForMutationLease / the wait_for_lease tool.
type WaitLeaseResult struct {
	Acquired                bool   `json:"acquired"`
	Host                    string `json:"host"`
	HolderIntentID          int64  `json:"holder_intent_id"`
	WaitedSeconds           int64  `json:"waited_s"`
	LastHeartbeatAgeSeconds int64  `json:"last_heartbeat_age_s"`
	TTLSeconds              int64  `json:"ttl_s"`
	Stale                   bool   `json:"stale"`
}

type mutationLease struct {
	intentID   int64
	acquiredAt time.Time
	lastBeat   time.Time
}

// StatefulTargetGuard enforces two per-(task, host) safety invariants on
// stateful targets, completely independent of the MITM traffic store:
//  1. Pre-mutation baseline gate: any mutating HTTP call is refused until at
//     least one read-only GET/HEAD call to that task+host has completed.
//  2. Mutation lease: only one intent per task may hold the mutation lease for
//     a given host at a time (scoped to the intent run, kept alive by tool
//     heartbeats up to ARTEX_STATEFUL_LEASE_TTL_S); read-only calls remain parallel.
type StatefulTargetGuard struct {
	mu        sync.Mutex
	leases    map[string]mutationLease // taskID+"\x00"+host -> lease entry
	baselines map[string]bool          // taskID+"\x00"+host -> true once read-only GET completed
	now       func() time.Time
	ttl       time.Duration
}

func NewStatefulTargetGuard() *StatefulTargetGuard {
	return &StatefulTargetGuard{
		leases:    map[string]mutationLease{},
		baselines: map[string]bool{},
		now:       time.Now,
	}
}

// SetClock overrides the clock used for lease timestamps and staleness checks (tests).
func (g *StatefulTargetGuard) SetClock(fn func() time.Time) {
	if g == nil || fn == nil {
		return
	}
	g.mu.Lock()
	g.now = fn
	g.mu.Unlock()
}

// SetTTL overrides the lease heartbeat TTL on this guard instance (0 = read env).
func (g *StatefulTargetGuard) SetTTL(d time.Duration) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.ttl = d
	g.mu.Unlock()
}

func (g *StatefulTargetGuard) clockNow() time.Time {
	if g.now != nil {
		return g.now().UTC()
	}
	return time.Now().UTC()
}

func (g *StatefulTargetGuard) effectiveTTL() time.Duration {
	if g != nil && g.ttl > 0 {
		return g.ttl
	}
	return StatefulLeaseTTL()
}

// EffectiveTTL returns the heartbeat TTL currently applied by this guard.
func (g *StatefulTargetGuard) EffectiveTTL() time.Duration {
	if g == nil {
		return StatefulLeaseTTL()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.effectiveTTL()
}

type statefulGuardKey struct{}
type statefulActorKey struct{}

// StatefulActor identifies the task and intent executing a tool call.
type StatefulActor struct {
	TaskID   string
	IntentID int64
}

func WithStatefulGuard(ctx context.Context, g *StatefulTargetGuard) context.Context {
	if g == nil {
		return ctx
	}
	return context.WithValue(ctx, statefulGuardKey{}, g)
}

func StatefulGuardFromContext(ctx context.Context) *StatefulTargetGuard {
	g, _ := ctx.Value(statefulGuardKey{}).(*StatefulTargetGuard)
	return g
}

func WithStatefulActor(ctx context.Context, taskID string, intentID int64) context.Context {
	return context.WithValue(ctx, statefulActorKey{}, StatefulActor{TaskID: taskID, IntentID: intentID})
}

func StatefulActorFromContext(ctx context.Context) (StatefulActor, bool) {
	a, ok := ctx.Value(statefulActorKey{}).(StatefulActor)
	return a, ok && a.TaskID != ""
}

func canonicalHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	return strings.TrimSuffix(h, ".")
}

func canonicalGuardKey(taskID, host string) string {
	return taskID + "\x00" + canonicalHost(host)
}

func nonNegDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func buildLeaseStatus(host string, entry mutationLease, now time.Time, ttl time.Duration) LeaseStatus {
	heldAge := nonNegDuration(now.Sub(entry.acquiredAt))
	beatAge := nonNegDuration(now.Sub(entry.lastBeat))
	return LeaseStatus{
		Host:                    canonicalHost(host),
		HolderIntentID:          entry.intentID,
		AcquiredAt:              entry.acquiredAt.UTC(),
		AgeSeconds:              int64(heldAge / time.Second),
		LastHeartbeat:           entry.lastBeat.UTC(),
		LastHeartbeatAgeSeconds: int64(beatAge / time.Second),
		TTLSeconds:              int64(ttl / time.Second),
		Stale:                   beatAge >= ttl,
	}
}

// RecordBaseline records that a read-only baseline request succeeded for host
// in taskID.
func (g *StatefulTargetGuard) RecordBaseline(taskID, host string) {
	if g == nil || taskID == "" || strings.TrimSpace(host) == "" {
		return
	}
	g.mu.Lock()
	g.baselines[canonicalGuardKey(taskID, host)] = true
	g.mu.Unlock()
}

// HasBaseline reports whether a read-only baseline has been recorded for host
// in taskID.
func (g *StatefulTargetGuard) HasBaseline(taskID, host string) bool {
	if g == nil || taskID == "" || strings.TrimSpace(host) == "" {
		return false
	}
	g.mu.Lock()
	ok := g.baselines[canonicalGuardKey(taskID, host)]
	g.mu.Unlock()
	return ok
}

// AcquireMutationLease acquires the per-(taskID, host) mutation lease for
// intentID (re-entrant for the same intentID). Returns false and the holding
// intentID if another non-stale intent currently holds the lease.
func (g *StatefulTargetGuard) AcquireMutationLease(taskID, host string, intentID int64) (bool, int64) {
	ok, status := g.AcquireMutationLeaseStatus(taskID, host, intentID)
	return ok, status.HolderIntentID
}

// AcquireMutationLeaseStatus behaves like AcquireMutationLease and also returns
// the full LeaseStatus for diagnostics and conflict reporting.
func (g *StatefulTargetGuard) AcquireMutationLeaseStatus(taskID, host string, intentID int64) (bool, LeaseStatus) {
	if g == nil || taskID == "" || strings.TrimSpace(host) == "" || intentID <= 0 {
		return true, LeaseStatus{}
	}
	chost := canonicalHost(host)
	key := taskID + "\x00" + chost
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clockNow()
	ttl := g.effectiveTTL()

	if entry, ok := g.leases[key]; ok && entry.intentID != 0 {
		if entry.intentID == intentID {
			entry.lastBeat = now
			g.leases[key] = entry
			return true, buildLeaseStatus(chost, entry, now, ttl)
		}
		beatAge := nonNegDuration(now.Sub(entry.lastBeat))
		if beatAge < ttl {
			return false, buildLeaseStatus(chost, entry, now, ttl)
		}
		heldAge := nonNegDuration(now.Sub(entry.acquiredAt))
		slog.Warn("[stateful-guard] taking over stale mutation lease",
			"task_id", taskID,
			"host", chost,
			"old_holder_intent_id", entry.intentID,
			"new_holder_intent_id", intentID,
			"held_age_s", int64(heldAge/time.Second),
			"last_heartbeat_age_s", int64(beatAge/time.Second),
			"ttl_s", int64(ttl/time.Second),
		)
	}

	entry := mutationLease{
		intentID:   intentID,
		acquiredAt: now,
		lastBeat:   now,
	}
	g.leases[key] = entry
	return true, buildLeaseStatus(chost, entry, now, ttl)
}

// WaitForMutationLease polls the internal guard (never the network) until the
// lease for (taskID, host) is free/stale/acquired by intentID or maxWait expires.
func (g *StatefulTargetGuard) WaitForMutationLease(ctx context.Context, taskID, host string, intentID int64, maxWait, pollInterval time.Duration) WaitLeaseResult {
	chost := canonicalHost(host)
	if maxWait <= 0 {
		maxWait = DefaultLeaseWait
	}
	if maxWait > MaxLeaseWait {
		maxWait = MaxLeaseWait
	}
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	if !StatefulGuardEnabled() || g == nil || taskID == "" || chost == "" {
		ttlS := int64(StatefulLeaseTTL() / time.Second)
		if g != nil {
			ttlS = int64(g.EffectiveTTL() / time.Second)
		}
		return WaitLeaseResult{
			Acquired:       true,
			Host:           chost,
			HolderIntentID: intentID,
			TTLSeconds:     ttlS,
		}
	}

	startWall := time.Now()
	startClock := g.clockNowSnapshot()
	elapsedSeconds := func() int64 {
		wallS := int64(time.Since(startWall) / time.Second)
		clkS := int64(nonNegDuration(g.clockNowSnapshot().Sub(startClock)) / time.Second)
		if clkS > wallS {
			return clkS
		}
		return wallS
	}

	tryOnce := func() (WaitLeaseResult, bool) {
		if intentID > 0 {
			ok, st := g.AcquireMutationLeaseStatus(taskID, chost, intentID)
			return WaitLeaseResult{
				Acquired:                ok,
				Host:                    chost,
				HolderIntentID:          st.HolderIntentID,
				WaitedSeconds:           elapsedSeconds(),
				LastHeartbeatAgeSeconds: st.LastHeartbeatAgeSeconds,
				TTLSeconds:              st.TTLSeconds,
				Stale:                   st.Stale,
			}, ok
		}
		sts := g.LeaseStatuses(taskID, chost)
		ttlS := int64(g.EffectiveTTL() / time.Second)
		if len(sts) == 0 {
			return WaitLeaseResult{
				Acquired:      true,
				Host:          chost,
				WaitedSeconds: elapsedSeconds(),
				TTLSeconds:    ttlS,
			}, true
		}
		st := sts[0]
		return WaitLeaseResult{
			Acquired:                st.Stale,
			Host:                    chost,
			HolderIntentID:          st.HolderIntentID,
			WaitedSeconds:           elapsedSeconds(),
			LastHeartbeatAgeSeconds: st.LastHeartbeatAgeSeconds,
			TTLSeconds:              st.TTLSeconds,
			Stale:                   st.Stale,
		}, st.Stale
	}

	for {
		res, done := tryOnce()
		if done {
			return res
		}
		rem := maxWait - time.Since(startWall)
		if rem <= 0 {
			return res
		}
		step := pollInterval
		if rem < step {
			step = rem
		}
		timer := time.NewTimer(step)
		select {
		case <-ctx.Done():
			timer.Stop()
			res, _ = tryOnce()
			return res
		case <-timer.C:
		}
	}
}

func (g *StatefulTargetGuard) clockNowSnapshot() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.clockNow()
}

// Heartbeat refreshes lastBeat on all active mutation leases currently held by
// (taskID, intentID). Does nothing for hosts held by a different intent.
func (g *StatefulTargetGuard) Heartbeat(taskID string, intentID int64) {
	if g == nil || taskID == "" || intentID <= 0 {
		return
	}
	prefix := taskID + "\x00"
	g.mu.Lock()
	now := g.clockNow()
	for k, entry := range g.leases {
		if strings.HasPrefix(k, prefix) && entry.intentID == intentID {
			entry.lastBeat = now
			g.leases[k] = entry
		}
	}
	g.mu.Unlock()
}

// TouchStatefulHeartbeat refreshes the lease heartbeat for the actor on ctx if
// both guard and actor are present and the stateful guard is enabled.
func TouchStatefulHeartbeat(ctx context.Context) {
	if !StatefulGuardEnabled() {
		return
	}
	g := StatefulGuardFromContext(ctx)
	actor, ok := StatefulActorFromContext(ctx)
	if g == nil || !ok {
		return
	}
	g.Heartbeat(actor.TaskID, actor.IntentID)
}

// LeaseStatuses returns sorted lease status entries for taskID (optionally
// filtered to a single canonical host when hostFilter is non-empty).
func (g *StatefulTargetGuard) LeaseStatuses(taskID, hostFilter string) []LeaseStatus {
	if g == nil || taskID == "" {
		return []LeaseStatus{}
	}
	prefix := taskID + "\x00"
	filter := canonicalHost(hostFilter)
	g.mu.Lock()
	now := g.clockNow()
	ttl := g.effectiveTTL()
	out := make([]LeaseStatus, 0, len(g.leases))
	for k, entry := range g.leases {
		if !strings.HasPrefix(k, prefix) || entry.intentID <= 0 {
			continue
		}
		host := strings.TrimPrefix(k, prefix)
		if filter != "" && host != filter {
			continue
		}
		out = append(out, buildLeaseStatus(host, entry, now, ttl))
	}
	g.mu.Unlock()
	slices.SortFunc(out, func(a, b LeaseStatus) int {
		return strings.Compare(a.Host, b.Host)
	})
	return out
}

// ReleaseIntent releases all mutation leases held by (taskID, intentID).
func (g *StatefulTargetGuard) ReleaseIntent(taskID string, intentID int64) {
	if g == nil || taskID == "" || intentID <= 0 {
		return
	}
	prefix := taskID + "\x00"
	g.mu.Lock()
	for k, entry := range g.leases {
		if strings.HasPrefix(k, prefix) && entry.intentID == intentID {
			delete(g.leases, k)
		}
	}
	g.mu.Unlock()
}

var (
	// Flags are case-sensitive: curl -x is the proxy, -f fails on HTTP errors
	// and -D dumps headers; none of them sends a body. --method is wget's.
	mutatingMethodFlagRe = regexp.MustCompile(`(?:^|\s)(?:-X|--request|--method)\s*=*\s*\\?["']?(?i:POST|PUT|PATCH|DELETE)\b`)
	// A short flag takes its value attached or separate (-d@body.json, -d'{}').
	// --json, curl -T/--upload-file and wget's --post-data/--post-file send a
	// body too. -T is read only after curl: wget -T is a timeout.
	mutatingBodyFlagRe   = regexp.MustCompile(`(?:^|\s)(?:-d|-F|--data[\w-]*|--json|--form[\w-]*|--upload-file|--post-data|--post-file)(?:[\s=@'"{\\]|$)|\bcurl\b[^;&|]*\s-T(?:\s|$)`)
	mutatingScriptCallRe = regexp.MustCompile(`(?i)(?:\.(?:post|put|patch|delete)\s*\(|http\.Post|method\s*[=:]\s*["'](?:POST|PUT|PATCH|DELETE)["']|urlopen\s*\([^)]*data\s*=)`)
	scriptURLRe          = regexp.MustCompile(`(?i)https?://[^\s"'<>\\)\]]+`)
)

func isMutatingHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

// ClassifyHTTPAction extracts target hosts and whether the action performs a
// mutating HTTP request across WebFetch, Bash/shell tools, and custom tools
// (including runScriptTool).
func ClassifyHTTPAction(toolName string, input json.RawMessage, renderedText string, explicitMethod string) (hosts []string, mutating bool) {
	seen := map[string]bool{}
	addHost := func(h string) {
		h = canonicalHost(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		hosts = append(hosts, h)
	}

	if len(input) > 0 {
		for _, t := range ExtractTargets(toolName, input) {
			addHost(t.Host)
		}
	}
	if renderedText != "" {
		var l targetList
		l.scan(renderedText)
		for _, t := range l.targets {
			addHost(t.Host)
		}
		for _, raw := range scriptURLRe.FindAllString(renderedText, 16) {
			if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
				addHost(u.Hostname())
			}
		}
	}

	if explicitMethod != "" {
		mutating = isMutatingHTTPMethod(explicitMethod)
		return hosts, mutating
	}

	switch toolName {
	case "WebFetch":
		var in struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(input, &in)
		mutating = isMutatingHTTPMethod(in.Method)
	default:
		text := renderedText
		if text == "" && len(input) > 0 {
			text = string(input)
		}
		mutating = mutatingMethodFlagRe.MatchString(text) ||
			mutatingBodyFlagRe.MatchString(text) ||
			mutatingScriptCallRe.MatchString(text)
	}
	return hosts, mutating
}

// CheckStatefulTool enforces the pre-mutation baseline gate and per-(task, host)
// mutation lease using the StatefulTargetGuard and StatefulActor on ctx.
func CheckStatefulTool(ctx context.Context, toolName string, input json.RawMessage, renderedText string, explicitMethod string) error {
	if !StatefulGuardEnabled() {
		return nil
	}
	g := StatefulGuardFromContext(ctx)
	actor, ok := StatefulActorFromContext(ctx)
	if g == nil || !ok {
		return nil
	}
	g.Heartbeat(actor.TaskID, actor.IntentID)
	hosts, mutating := ClassifyHTTPAction(toolName, input, renderedText, explicitMethod)
	if len(hosts) == 0 || !mutating {
		return nil
	}
	for _, h := range hosts {
		if !g.HasBaseline(actor.TaskID, h) {
			return fmt.Errorf("pre-mutation baseline required for host %s: perform a read-only GET baseline before mutating state", h)
		}
	}
	for _, h := range hosts {
		if acquired, st := g.AcquireMutationLeaseStatus(actor.TaskID, h, actor.IntentID); !acquired {
			return fmt.Errorf(
				"mutation lease for host %s is held by intent #%d (held %ds, last heartbeat %ds ago, ttl %ds); "+
					"call lease_status or wait_for_lease (never sleep in Bash), do read-only work on this host, or report blocked if it remains stuck",
				h, st.HolderIntentID, st.AgeSeconds, st.LastHeartbeatAgeSeconds, st.TTLSeconds,
			)
		}
	}
	return nil
}

// RecordStatefulReadOnly records a baseline snapshot for each host touched by a
// completed non-mutating HTTP call, and refreshes the holder's heartbeat.
func RecordStatefulReadOnly(ctx context.Context, toolName string, input json.RawMessage, renderedText string, explicitMethod string) {
	if !StatefulGuardEnabled() {
		return
	}
	g := StatefulGuardFromContext(ctx)
	actor, ok := StatefulActorFromContext(ctx)
	if g == nil || !ok {
		return
	}
	g.Heartbeat(actor.TaskID, actor.IntentID)
	hosts, mutating := ClassifyHTTPAction(toolName, input, renderedText, explicitMethod)
	if mutating || len(hosts) == 0 {
		return
	}
	for _, h := range hosts {
		g.RecordBaseline(actor.TaskID, h)
	}
}
