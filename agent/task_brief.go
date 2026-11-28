package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

// TaskBrief is a compact coordination snapshot giving a worker immediate visibility
// into active goals, live intents and their worker holders, host mutation leases,
// open hypotheses/capabilities, and the latest 5 facts/findings — eliminating turns
// spent reading peer worker traces just to coordinate.
type TaskBrief struct {
	ActiveGoals           []map[string]any        `json:"active_goals"`
	OpenAndRunningIntents []map[string]any        `json:"open_and_running_intents"`
	LeaseStatus           []intercept.LeaseStatus `json:"lease_status"`
	OpenHypotheses        []map[string]any        `json:"open_hypotheses"`
	CapabilitiesHeld      []map[string]any        `json:"capabilities_held"`
	CapabilitiesRequired  []map[string]any        `json:"capabilities_required"`
	LastFacts             []map[string]any        `json:"last_facts"`
	LastFindings          []map[string]any        `json:"last_findings"`
}

const taskBriefRecentCap = 5

func (t *ToolSet) taskBriefData(ctx context.Context) TaskBrief {
	brief := TaskBrief{
		ActiveGoals:           []map[string]any{},
		OpenAndRunningIntents: []map[string]any{},
		LeaseStatus:           []intercept.LeaseStatus{},
		OpenHypotheses:        []map[string]any{},
		CapabilitiesHeld:      []map[string]any{},
		CapabilitiesRequired:  []map[string]any{},
		LastFacts:             []map[string]any{},
		LastFindings:          []map[string]any{},
	}

	sg := t.statefulGuard
	if sg == nil {
		sg = intercept.StatefulGuardFromContext(ctx)
	}
	taskIDStr := ""
	if t.taskID > 0 {
		taskIDStr = strconv.FormatInt(t.taskID, 10)
	}
	if actor, ok := intercept.StatefulActorFromContext(ctx); ok && actor.TaskID != "" {
		taskIDStr = actor.TaskID
	}
	if intercept.StatefulGuardEnabled() && sg != nil && taskIDStr != "" {
		brief.LeaseStatus = sg.LeaseStatuses(taskIDStr, "")
	}

	if t.ts == nil {
		return brief
	}

	if goals, err := t.ts.ListByKind(db.KindGoal, 50); err == nil {
		for _, g := range goals {
			if g.State == "met" {
				continue
			}
			var p map[string]any
			_ = json.Unmarshal(g.Payload, &p)
			txt, _ := p["text"].(string)
			txt = strings.TrimSpace(txt)
			if txt == "" {
				continue
			}
			brief.ActiveGoals = append(brief.ActiveGoals, map[string]any{
				"id":      g.ID,
				"state":   g.State,
				"summary": truncOutput(txt, 160),
			})
		}
	}

	if intents, err := t.ts.ListByKind(db.KindIntent, 100); err == nil {
		for _, in := range intents {
			if in.State != "open" && in.State != "running" && in.State != "paused" {
				continue
			}
			var p map[string]any
			_ = json.Unmarshal(in.Payload, &p)
			summ, _ := p["summary"].(string)
			entry := map[string]any{
				"id":      in.ID,
				"state":   in.State,
				"summary": truncOutput(strings.TrimSpace(summ), 160),
			}
			if in.Owner != "" {
				entry["holder"] = in.Owner
			}
			brief.OpenAndRunningIntents = append(brief.OpenAndRunningIntents, entry)
		}
	}

	if openHyp, _ := t.hypothesisOverview(); len(openHyp) > 0 {
		brief.OpenHypotheses = openHyp
	}
	if heldCap, reqCap, _ := t.capabilityOverview(); len(heldCap) > 0 || len(reqCap) > 0 {
		if len(heldCap) > 0 {
			brief.CapabilitiesHeld = heldCap
		}
		if len(reqCap) > 0 {
			brief.CapabilitiesRequired = reqCap
		}
	}

	if facts, err := t.ts.ListByKind(db.KindFact, 100); err == nil {
		for _, f := range facts {
			if len(brief.LastFacts) >= taskBriefRecentCap {
				break
			}
			if _, ok := isHypothesisNode(f); ok {
				continue
			}
			if _, ok := isCapabilityNode(f); ok {
				continue
			}
			cf := compactFact(f)
			summ, _ := cf["summary"].(string)
			brief.LastFacts = append(brief.LastFacts, map[string]any{
				"id":      f.ID,
				"summary": truncOutput(strings.TrimSpace(summ), 160),
			})
		}
	}

	if findings, err := t.ts.ListByKind(db.KindFinding, 25); err == nil {
		for _, fd := range findings {
			if len(brief.LastFindings) >= taskBriefRecentCap {
				break
			}
			var fp map[string]any
			_ = json.Unmarshal(fd.Payload, &fp)
			summ, _ := fp["summary"].(string)
			brief.LastFindings = append(brief.LastFindings, map[string]any{
				"id":      fd.ID,
				"summary": truncOutput(strings.TrimSpace(summ), 160),
			})
		}
	}

	return brief
}

func renderWorkerTaskBrief(brief TaskBrief, env dataEnvelope) string {
	// Keep operator goals and guard lease statuses structured; wrap agent/target
	// summaries inside env so derived content never leaves the data envelope.
	headerData := map[string]any{
		"active_goals": brief.ActiveGoals,
		"lease_status": brief.LeaseStatus,
	}
	derivedData := map[string]any{
		"open_and_running_intents": brief.OpenAndRunningIntents,
		"open_hypotheses":          brief.OpenHypotheses,
		"capabilities_held":        brief.CapabilitiesHeld,
		"capabilities_required":    brief.CapabilitiesRequired,
		"last_facts":               brief.LastFacts,
		"last_findings":            brief.LastFindings,
	}
	hb, err1 := json.Marshal(headerData)
	dbBytes, err2 := json.Marshal(derivedData)
	if err1 != nil || err2 != nil {
		return ""
	}
	return "\n\n[task_brief (live coordination summary — avoid reading peer traces unless debugging)]:\n" +
		string(hb) + "\n" +
		env.wrap("task_brief", string(dbBytes))
}

func (t *ToolSet) taskBriefTool() actool.CoreTool {
	return readTool("task_brief",
		"Read a compact task coordination summary: active goals, open/running intents and holders, live host lease_status, open hypotheses and capabilities, and the latest 5 facts and findings with ids and one-line summaries. Use this instead of reading peer worker traces to coordinate.",
		obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			return jsonResult(t.taskBriefData(ctx))
		})
}

var mutatingIntentSummaryRe = regexp.MustCompile(`(?i)\b(post|put|patch|delete|mutate|upload|deploy|create|update|modify|submit|register|renew|retire|rotate|revoke|write|overwrite|tamper|exploit|trigger)\b`)

func intentLooksMutating(summary string) bool {
	return mutatingIntentSummaryRe.MatchString(summary)
}

func (t *ToolSet) extractIntentTargetHosts(it intentItem) []string {
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
	for _, raw := range probeURLRe.FindAllString(it.Summary, 8) {
		if u, err := url.Parse(strings.TrimRight(raw, `.,;:'")`)); err == nil && u.Hostname() != "" {
			add(u.Hostname())
		}
	}
	if t.as != nil {
		if ids := pidList(it.AssetIDs); len(ids) > 0 {
			if assets, err := t.as.GetByIDs(ids); err == nil {
				for _, a := range assets {
					if a == nil {
						continue
					}
					if a.Domain != "" {
						add(a.Domain)
					}
					if a.URL != "" {
						if u, perr := url.Parse(a.URL); perr == nil && u.Hostname() != "" {
							add(u.Hostname())
						}
					}
					if a.IP != "" {
						add(a.IP)
					}
				}
			}
		}
	}
	return hosts
}

// checkActiveLeaseForNewIntent prevents the planner from queuing a competing
// mutating intent against a host with an active (non-stale) mutation lease,
// unless the intent is read-only or explicitly sets wait_for_lease=true.
func (t *ToolSet) checkActiveLeaseForNewIntent(it intentItem) error {
	if !intercept.StatefulGuardEnabled() || t.statefulGuard == nil || t.taskID <= 0 {
		return nil
	}
	if !intentLooksMutating(it.Summary) {
		return nil
	}
	if it.WaitForLease {
		return nil
	}
	taskIDStr := strconv.FormatInt(t.taskID, 10)
	hosts := t.extractIntentTargetHosts(it)
	for _, h := range hosts {
		for _, ls := range t.statefulGuard.LeaseStatuses(taskIDStr, h) {
			if ls.Stale {
				continue
			}
			return fmt.Errorf(
				"host %s has an active mutation lease held by intent #%d (held %ds, last heartbeat %ds ago): pick a read-only direction, wait for intent #%d to finish, or set wait_for_lease: true on add_intent",
				ls.Host, ls.HolderIntentID, ls.AgeSeconds, ls.LastHeartbeatAgeSeconds, ls.HolderIntentID,
			)
		}
	}
	return nil
}
