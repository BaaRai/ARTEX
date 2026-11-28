package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

type hypothesisPayload struct {
	Subtype      string `json:"subtype"`
	Status       string `json:"status"`
	Hypothesis   string `json:"hypothesis"`
	Direction    string `json:"direction,omitempty"`
	CloseReason  string `json:"close_reason,omitempty"`
	ReopenReason string `json:"reopen_reason,omitempty"`
	NewEvidence  string `json:"new_evidence,omitempty"`
}

func isHypothesisNode(n *db.Node) (hypothesisPayload, bool) {
	if n == nil || n.Kind != db.KindFact {
		return hypothesisPayload{}, false
	}
	var p hypothesisPayload
	if json.Unmarshal(n.Payload, &p) != nil || p.Subtype != db.FactSubtypeHypothesis {
		return hypothesisPayload{}, false
	}
	if p.Status == "" {
		if n.State == "stopped" {
			p.Status = "closed"
		} else {
			p.Status = "open"
		}
	}
	return p, true
}

func (t *ToolSet) upsertHypothesis() actool.CoreTool {
	return t.writeExpTool("upsert_hypothesis",
		"Create, update, or close a planner hypothesis. Closed hypotheses must record close_reason; add_intent rejects variants of a closed hypothesis unless both new_evidence and reopen_reason are provided.",
		obj(map[string]any{
			"id":            idp("optional: existing hypothesis node ID to update/close/reopen"),
			"hypothesis":    str("concise statement of the hypothesis (required when creating)"),
			"direction":     str("optional: normalized attack direction/endpoint pattern used to match duplicate variants"),
			"status":        map[string]any{"type": "string", "enum": []string{"open", "closed"}, "description": "hypothesis status (default open)"},
			"close_reason":  str("required when status=closed: why this hypothesis is closed (e.g. negative_sweep, 403_cloudflare, state_churned)"),
			"new_evidence":  str("required when reopening a closed hypothesis: new fact/finding summary"),
			"reopen_reason": str("required when reopening a closed hypothesis: why the close_reason no longer holds"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				ID           json.RawMessage `json:"id"`
				Hypothesis   string          `json:"hypothesis"`
				Direction    string          `json:"direction"`
				Status       string          `json:"status"`
				CloseReason  string          `json:"close_reason"`
				NewEvidence  string          `json:"new_evidence"`
				ReopenReason string          `json:"reopen_reason"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			status := strings.ToLower(strings.TrimSpace(a.Status))
			if status == "" {
				status = "open"
			}
			if status != "open" && status != "closed" {
				return actool.Errorf("status must be open or closed"), nil
			}
			if status == "closed" && strings.TrimSpace(a.CloseReason) == "" {
				return actool.Errorf("close_reason is required when closing a hypothesis"), nil
			}
			id := pid(a.ID)
			claimContext := a.CloseReason + " " + a.Hypothesis + " " + a.Direction
			if id > 0 {
				n, err := t.ts.GetNode(id)
				p, ok := isHypothesisNode(n)
				if err != nil || !ok {
					return actool.Errorf(fmt.Sprintf("hypothesis #%d not found", id)), nil
				}
				if status == "closed" && isRouteExhaustionClaim(claimContext+" "+p.Hypothesis+" "+p.Direction) {
					if gerr := missingDiscoveryCoverageErrorForClaim(t.ts, claimContext+" "+p.Hypothesis+" "+p.Direction); gerr != nil {
						return actool.Errorf(gerr.Error()), nil
					}
				}
				if p.Status == "closed" && status == "open" {
					if strings.TrimSpace(a.NewEvidence) == "" || strings.TrimSpace(a.ReopenReason) == "" {
						return actool.Errorf("reopening a closed hypothesis requires both new_evidence and reopen_reason"), nil
					}
					p.NewEvidence = strings.TrimSpace(a.NewEvidence)
					p.ReopenReason = strings.TrimSpace(a.ReopenReason)
				}
				if h := strings.TrimSpace(a.Hypothesis); h != "" {
					p.Hypothesis = h
				}
				if d := strings.TrimSpace(a.Direction); d != "" {
					p.Direction = d
				}
				if status == "closed" {
					p.CloseReason = strings.TrimSpace(a.CloseReason)
				}
				if err := t.updateHypothesisNode(id, p, status); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{"id": id, "status": status, "hypothesis": p.Hypothesis, "close_reason": p.CloseReason})
			}

			if status == "closed" && isRouteExhaustionClaim(claimContext) {
				if gerr := missingDiscoveryCoverageErrorForClaim(t.ts, claimContext); gerr != nil {
					return actool.Errorf(gerr.Error()), nil
				}
			}
			hypText := strings.TrimSpace(a.Hypothesis)
			if hypText == "" {
				return actool.Errorf("hypothesis is required when creating a new hypothesis"), nil
			}
			p := map[string]any{
				"subtype":    db.FactSubtypeHypothesis,
				"status":     status,
				"hypothesis": hypText,
			}
			if d := strings.TrimSpace(a.Direction); d != "" {
				p["direction"] = d
			}
			if status == "closed" {
				p["close_reason"] = strings.TrimSpace(a.CloseReason)
			}
			newID, err := t.ts.AddNode(db.KindFact, p, 0, "confirmed", "planner", nil)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"id": newID, "status": status, "hypothesis": hypText, "close_reason": a.CloseReason})
		})
}

func (t *ToolSet) updateHypothesisNode(id int64, p hypothesisPayload, status string) error {
	p.Subtype = db.FactSubtypeHypothesis
	p.Status = status
	raw, _ := json.Marshal(p)
	return t.ts.UpdateNodePayload(id, raw)
}

func (t *ToolSet) checkClosedHypothesis(it intentItem) error {
	if t.ts == nil {
		return nil
	}
	nodes, err := t.ts.ListByKind(db.KindFact, 500)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	hypID := pid(it.HypothesisID)
	intentDir := normalizeDirection(it.Summary)
	var matched *db.Node
	var matchedPayload hypothesisPayload
	for _, n := range nodes {
		p, ok := isHypothesisNode(n)
		if !ok || p.Status != "closed" {
			continue
		}
		if hypID > 0 && n.ID == hypID {
			matched = n
			matchedPayload = p
			break
		}
		if intentDir != "" {
			hDir := normalizeDirection(p.Direction)
			if hDir == "" {
				hDir = normalizeDirection(p.Hypothesis)
			}
			if hDir != "" && hDir == intentDir {
				matched = n
				matchedPayload = p
				break
			}
		}
	}
	if matched == nil {
		return nil
	}
	if strings.TrimSpace(it.NewEvidence) == "" || strings.TrimSpace(it.ReopenReason) == "" {
		return fmt.Errorf("hypothesis #%d is closed (%s): variants of a closed hypothesis are blocked unless both new_evidence and reopen_reason are provided", matched.ID, matchedPayload.CloseReason)
	}
	matchedPayload.NewEvidence = strings.TrimSpace(it.NewEvidence)
	matchedPayload.ReopenReason = strings.TrimSpace(it.ReopenReason)
	_ = t.updateHypothesisNode(matched.ID, matchedPayload, "open")
	return nil
}

func (t *ToolSet) hypothesisOverview() (openList, closedList []map[string]any) {
	if t.ts == nil {
		return nil, nil
	}
	nodes, err := t.ts.ListByKind(db.KindFact, 500)
	if err != nil {
		return nil, nil
	}
	for _, n := range nodes {
		p, ok := isHypothesisNode(n)
		if !ok {
			continue
		}
		m := map[string]any{
			"id":         n.ID,
			"hypothesis": p.Hypothesis,
		}
		if p.Direction != "" {
			m["direction"] = p.Direction
		}
		if p.Status == "closed" {
			m["close_reason"] = p.CloseReason
			closedList = append(closedList, m)
		} else {
			openList = append(openList, m)
		}
	}
	return openList, closedList
}
