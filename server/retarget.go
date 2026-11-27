package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// RetargetTask updates a task when the operator provisions a fresh target
// instance with a new hostname/URL: updates tasks.goal, open goal payloads,
// constraints, and task_scope, kills running/open stale intents targeting
// oldHost, seeds the new URL, and wakes the planner.
func (s *Server) RetargetTask(ctx context.Context, taskID, oldHost, newBaseURL string) (*db.RetargetResult, int, error) {
	t, ok := s.m.Task(taskID)
	if !ok {
		return nil, 0, fmt.Errorf("task %s not found", taskID)
	}
	idNum, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return nil, 0, err
	}
	res, err := s.m.pg.RetargetTask(ctx, idNum, oldHost, newBaseURL)
	if err != nil {
		return nil, 0, err
	}
	s.m.mu.Lock()
	t.Goal = res.UpdatedGoal
	s.m.mu.Unlock()

	// Kill running or open intents still targeting oldHost.
	staleCount := 0
	oldLower := strings.ToLower(strings.TrimSpace(res.OldHost))
	if oldLower != "" && t.Store != nil {
		intents, _ := t.Store.ListByKind(db.KindIntent, 500)
		for _, in := range intents {
			if in.State != "open" && in.State != "running" && in.State != "paused" {
				continue
			}
			var p struct {
				Summary string `json:"summary"`
			}
			_ = json.Unmarshal(in.Payload, &p)
			if !strings.Contains(strings.ToLower(p.Summary), oldLower) {
				continue
			}
			if in.State == "running" && s.engine != nil {
				_ = s.engine.KillWork(in.ID)
			}
			if err := t.Store.SetNodeState(in.ID, "stopped"); err == nil {
				staleCount++
			}
		}
	}
	s.seed(t, newBaseURL)
	if s.engine != nil {
		s.engine.emitActivity(t, db.Activity{
			Worker:  "system",
			Kind:    "text",
			Summary: fmt.Sprintf("Task retargeted from %s to %s (%d stale intent(s) stopped)", res.OldHost, res.NewHost, staleCount),
		})
	}
	t.Notify()
	log.Printf("[retarget] task %s retargeted %s -> %s (stale_intents=%d)", taskID, res.OldHost, res.NewHost, staleCount)
	return res, staleCount, nil
}

// retargetTaskHandler handles POST /api/tasks/{id}/retarget with JSON body
// {"old_host":"...","new_base_url":"https://..."}.
func (s *Server) retargetTaskHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.m.Task(id); !ok {
		writeErr(w, 404, "task not found")
		return
	}
	var req struct {
		OldHost    string `json:"old_host"`
		NewBaseURL string `json:"new_base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	oldHost := strings.TrimSpace(req.OldHost)
	newBaseURL := strings.TrimSpace(req.NewBaseURL)
	if oldHost == "" || newBaseURL == "" {
		writeErr(w, 400, "old_host and new_base_url are required")
		return
	}
	u, err := url.Parse(newBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeErr(w, 400, "new_base_url must be a valid http or https URL")
		return
	}
	if s.engine != nil {
		if !s.engine.beginTaskOperation(id) {
			writeErr(w, 409, "task is being deleted")
			return
		}
		defer s.engine.decInflight(id)
	}
	res, staleCount, err := s.RetargetTask(r.Context(), id, oldHost, newBaseURL)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"task_id":       id,
		"old_host":      res.OldHost,
		"new_host":      res.NewHost,
		"updated_goal":  res.UpdatedGoal,
		"stale_intents": staleCount,
		"retargeted":    res,
	})
}
