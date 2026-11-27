package intercept

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// StatefulTargetGuard enforces two per-(task, host) safety invariants on
// stateful targets, completely independent of the MITM traffic store:
//  1. Pre-mutation baseline gate: any mutating HTTP call is refused until at
//     least one read-only GET/HEAD call to that task+host has completed.
//  2. Mutation lease: only one intent per task may hold the mutation lease for
//     a given host at a time; read-only calls remain parallel.
type StatefulTargetGuard struct {
	mu        sync.Mutex
	leases    map[string]int64 // taskID+"\x00"+host -> holding intentID
	baselines map[string]bool  // taskID+"\x00"+host -> true once read-only GET completed
}

func NewStatefulTargetGuard() *StatefulTargetGuard {
	return &StatefulTargetGuard{
		leases:    map[string]int64{},
		baselines: map[string]bool{},
	}
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

func canonicalGuardKey(taskID, host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(h, ".")
	return taskID + "\x00" + h
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
// intentID if another intent currently holds the lease.
func (g *StatefulTargetGuard) AcquireMutationLease(taskID, host string, intentID int64) (bool, int64) {
	if g == nil || taskID == "" || strings.TrimSpace(host) == "" || intentID <= 0 {
		return true, 0
	}
	key := canonicalGuardKey(taskID, host)
	g.mu.Lock()
	defer g.mu.Unlock()
	if holder, ok := g.leases[key]; ok && holder != 0 && holder != intentID {
		return false, holder
	}
	g.leases[key] = intentID
	return true, intentID
}

// ReleaseIntent releases all mutation leases held by (taskID, intentID).
func (g *StatefulTargetGuard) ReleaseIntent(taskID string, intentID int64) {
	if g == nil || taskID == "" || intentID <= 0 {
		return
	}
	prefix := taskID + "\x00"
	g.mu.Lock()
	for k, holder := range g.leases {
		if strings.HasPrefix(k, prefix) && holder == intentID {
			delete(g.leases, k)
		}
	}
	g.mu.Unlock()
}

var (
	mutatingMethodFlagRe = regexp.MustCompile(`(?i)(?:^|\s)(?:-X|--request)\s*=*\s*["']?(POST|PUT|PATCH|DELETE)\b`)
	mutatingBodyFlagRe   = regexp.MustCompile(`(?i)(?:^|\s)(?:-d|--data|--data-raw|--data-binary|--data-urlencode|-F|--form)(?:\s|=)`)
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
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
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
	g := StatefulGuardFromContext(ctx)
	actor, ok := StatefulActorFromContext(ctx)
	if g == nil || !ok {
		return nil
	}
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
		if acquired, holder := g.AcquireMutationLease(actor.TaskID, h, actor.IntentID); !acquired {
			return fmt.Errorf("mutation lease for host %s is held by intent #%d; retry after it finishes", h, holder)
		}
	}
	return nil
}

// RecordStatefulReadOnly records a baseline snapshot for each host touched by a
// completed non-mutating HTTP call.
func RecordStatefulReadOnly(ctx context.Context, toolName string, input json.RawMessage, renderedText string, explicitMethod string) {
	g := StatefulGuardFromContext(ctx)
	actor, ok := StatefulActorFromContext(ctx)
	if g == nil || !ok {
		return
	}
	hosts, mutating := ClassifyHTTPAction(toolName, input, renderedText, explicitMethod)
	if mutating || len(hosts) == 0 {
		return
	}
	for _, h := range hosts {
		g.RecordBaseline(actor.TaskID, h)
	}
}
