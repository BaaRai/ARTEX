package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

var capabilityStrictOverride atomic.Int32 // 0=env, 1=shadow(false), 2=strict(true)

// SetCapabilityStrictMode overrides shadow vs strict enforcement for tests.
func SetCapabilityStrictMode(strict bool) {
	if strict {
		capabilityStrictOverride.Store(2)
	} else {
		capabilityStrictOverride.Store(1)
	}
}

// ResetCapabilityStrictMode restores env-based capability mode (default shadow).
func ResetCapabilityStrictMode() {
	capabilityStrictOverride.Store(0)
}

func capabilityStrictEnabled() bool {
	switch capabilityStrictOverride.Load() {
	case 1:
		return false
	case 2:
		return true
	default:
		return os.Getenv("ARTEX_CAPABILITY_STRICT") == "1"
	}
}

type capabilityPayload struct {
	Subtype      string   `json:"subtype"`
	Status       string   `json:"status"` // held | required | closed
	Holder       string   `json:"holder"`
	Verb         string   `json:"verb"`
	Object       string   `json:"object"`
	Precondition string   `json:"precondition,omitempty"`
	Evidence     string   `json:"evidence"`
	Confidence   string   `json:"confidence"`
	Verified     bool     `json:"verified"`
	CloseReason  string   `json:"close_reason,omitempty"`
	ProvingIDs   []int64  `json:"proving_ids,omitempty"`
	TrafficRefs  []string `json:"traffic_refs,omitempty"`
}

func isCapabilityNode(n *db.Node) (capabilityPayload, bool) {
	if n == nil || n.Kind != db.KindFact {
		return capabilityPayload{}, false
	}
	var p capabilityPayload
	if json.Unmarshal(n.Payload, &p) != nil || p.Subtype != db.FactSubtypeCapability {
		return capabilityPayload{}, false
	}
	if p.Status == "" {
		p.Status = "held"
	}
	return p, true
}

func (t *ToolSet) recordCapability() actool.CoreTool {
	return t.writeExpTool("record_capability",
		"Record an exploitation capability (held, required by an open goal, or closed/unreachable) in the exploitation capability ledger (#613). A capability is a power the operator has obtained over the target (an actor can now do an action on an object), not a permission granted to this agent: scope, constraints and the intercept gate remain the agent's permission model. Evidence and confidence are mandatory; held capabilities without a proving finding/fact/traffic reference are stored as unverified and cannot satisfy a goal.",
		obj(map[string]any{
			"status":       map[string]any{"type": "string", "enum": []string{"held", "required", "closed"}, "description": "held (acquired), required (needed by open goal), or closed (proven unreachable)"},
			"holder":       str("actor/principal holding or needing the capability (e.g. member_session, edge_peer)"),
			"verb":         str("action verb (e.g. retire_cert, read_enrolment, impersonate_edge)"),
			"object":       str("target object/resource"),
			"precondition": str("optional precondition"),
			"evidence":     str("mandatory: observed evidence or goal requirement rationale"),
			"confidence":   map[string]any{"type": "string", "enum": []string{"observed", "inferred"}, "description": "mandatory confidence level"},
			"proving_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "optional: finding/fact node IDs proving this capability"},
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "optional: related asset IDs"},
			"traffic_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional: proving traffic IDs"},
			"close_reason": str("required when status=closed"),
		}, "holder", "verb", "object", "evidence", "confidence"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Status       string            `json:"status"`
				Holder       string            `json:"holder"`
				Verb         string            `json:"verb"`
				Object       string            `json:"object"`
				Precondition string            `json:"precondition"`
				Evidence     string            `json:"evidence"`
				Confidence   string            `json:"confidence"`
				ProvingIDs   []json.RawMessage `json:"proving_ids"`
				AssetIDs     []json.RawMessage `json:"asset_ids"`
				TrafficRefs  []string          `json:"traffic_refs"`
				CloseReason  string            `json:"close_reason"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			status := strings.ToLower(strings.TrimSpace(a.Status))
			if status == "" {
				status = "held"
			}
			if status != "held" && status != "required" && status != "closed" {
				return actool.Errorf("status must be held, required, or closed"), nil
			}
			if strings.TrimSpace(a.Holder) == "" || strings.TrimSpace(a.Verb) == "" || strings.TrimSpace(a.Object) == "" {
				return actool.Errorf("holder, verb, and object are required"), nil
			}
			if strings.TrimSpace(a.Evidence) == "" || strings.TrimSpace(a.Confidence) == "" {
				return actool.Errorf("evidence and confidence are mandatory on a capability"), nil
			}
			if status == "closed" && strings.TrimSpace(a.CloseReason) == "" {
				return actool.Errorf("close_reason is required when status=closed"), nil
			}

			provingIDs := pidList(a.ProvingIDs)
			validProving := make([]int64, 0, len(provingIDs))
			for _, pidv := range provingIDs {
				n, err := t.ts.GetNodeWithSources(pidv)
				if err != nil || n == nil || (n.Kind != db.KindFinding && n.Kind != db.KindFact) {
					return actool.Errorf(fmt.Sprintf("proving_id %d must be an existing finding or fact node", pidv)), nil
				}
				validProving = append(validProving, pidv)
			}
			verified := len(validProving) > 0 || len(a.TrafficRefs) > 0
			if status != "held" {
				verified = false
			}

			p := map[string]any{
				"subtype":      db.FactSubtypeCapability,
				"status":       status,
				"holder":       strings.TrimSpace(a.Holder),
				"verb":         strings.TrimSpace(a.Verb),
				"object":       strings.TrimSpace(a.Object),
				"precondition": strings.TrimSpace(a.Precondition),
				"evidence":     strings.TrimSpace(a.Evidence),
				"confidence":   strings.TrimSpace(a.Confidence),
				"verified":     verified,
			}
			if status == "closed" {
				p["close_reason"] = strings.TrimSpace(a.CloseReason)
			}
			if len(validProving) > 0 {
				p["proving_ids"] = validProving
			}
			if len(a.TrafficRefs) > 0 {
				p["traffic_refs"] = a.TrafficRefs
			}
			anchors := pidList(a.AssetIDs)
			id, err := t.ts.AddNode(db.KindFact, p, 0, "confirmed", "planner", anchors)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			for _, provID := range validProving {
				_ = t.ts.Link(provID, db.RelDerivedFrom, id)
			}
			return jsonResult(map[string]any{
				"id":       id,
				"status":   status,
				"verified": verified,
				"holder":   p["holder"],
				"verb":     p["verb"],
				"object":   p["object"],
			})
		})
}

func (t *ToolSet) checkIntentCapabilities(it intentItem) (warning string, err error) {
	// Reject/flag assuming a closed capability.
	if t.ts != nil && len(it.Consumes) > 0 {
		nodes, _ := t.ts.ListByKind(db.KindFact, 500)
		closedByID := map[int64]capabilityPayload{}
		for _, n := range nodes {
			if p, ok := isCapabilityNode(n); ok && p.Status == "closed" {
				closedByID[n.ID] = p
			}
		}
		for _, raw := range it.Consumes {
			if id := pid(raw); id > 0 {
				if cp, closed := closedByID[id]; closed {
					msg := fmt.Sprintf("intent consumes closed capability #%d (%s %s: %s)", id, cp.Verb, cp.Object, cp.CloseReason)
					if capabilityStrictEnabled() {
						return "", fmt.Errorf("%s", msg)
					}
					warning = msg
				}
			}
		}
	}
	if len(it.Gains) == 0 {
		msg := "intent declares no capability gains (shadow capability ledger #613)"
		if capabilityStrictEnabled() {
			return "", fmt.Errorf("intent rejected: must declare at least one capability in gains")
		}
		slog.Warn("[capability] shadow flag on add_intent", "summary", it.Summary, "reason", msg)
		if warning == "" {
			warning = msg
		}
	}
	return warning, nil
}

func (t *ToolSet) capabilityOverview() (held, required, closed []map[string]any) {
	if t.ts == nil {
		return nil, nil, nil
	}
	nodes, err := t.ts.ListByKind(db.KindFact, 500)
	if err != nil {
		return nil, nil, nil
	}
	for _, n := range nodes {
		p, ok := isCapabilityNode(n)
		if !ok {
			continue
		}
		m := map[string]any{
			"id":       n.ID,
			"holder":   p.Holder,
			"verb":     p.Verb,
			"object":   p.Object,
			"verified": p.Verified,
		}
		if p.Precondition != "" {
			m["precondition"] = p.Precondition
		}
		switch p.Status {
		case "required":
			required = append(required, m)
		case "closed":
			m["close_reason"] = p.CloseReason
			closed = append(closed, m)
		default:
			held = append(held, m)
		}
	}
	return held, required, closed
}
