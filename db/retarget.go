package db

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// RetargetResult summarizes the rows updated when retargeting a task to a new
// instance base URL.
type RetargetResult struct {
	TaskID             int64  `json:"task_id"`
	OldHost            string `json:"old_host"`
	NewHost            string `json:"new_host"`
	NewBaseURL         string `json:"new_base_url"`
	GoalsUpdated       int    `json:"goals_updated"`
	ConstraintsUpdated int    `json:"constraints_updated"`
	ScopesUpdated      int    `json:"scopes_updated"`
	UpdatedGoal        string `json:"updated_goal"`
}

func replaceHostCaseInsensitive(text, oldHost, newHost string) (string, bool) {
	if oldHost == "" || newHost == "" || strings.EqualFold(oldHost, newHost) {
		return text, false
	}
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(oldHost))
	if !re.MatchString(text) {
		return text, false
	}
	return re.ReplaceAllString(text, newHost), true
}

// RetargetTask updates tasks.goal, explorations.goal, open goal node payloads,
// task_constraints, and task_scope from oldHost to newBaseURL in a single
// transaction (no schema migration).
func (d *DB) RetargetTask(ctx context.Context, taskID int64, oldHost, newBaseURL string) (*RetargetResult, error) {
	u, err := url.Parse(strings.TrimSpace(newBaseURL))
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid new base URL %q", newBaseURL)
	}
	oldH := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(oldHost), "."))
	newH := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(u.Hostname()), "."))
	if oldH == "" {
		return nil, fmt.Errorf("old_host is required")
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var expID int64
	var curGoal string
	if err := tx.QueryRowContext(ctx, `SELECT exploration_id, goal FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&expID, &curGoal); err != nil {
		return nil, err
	}

	newGoal, replaced := replaceHostCaseInsensitive(curGoal, oldH, newH)
	if !replaced && !strings.Contains(strings.ToLower(curGoal), newH) {
		newGoal = strings.TrimSpace(curGoal) + " [Target: " + strings.TrimSpace(newBaseURL) + "]"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET goal=$1 WHERE id=$2`, newGoal, taskID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE explorations SET goal=$1 WHERE id=$2`, newGoal, expID); err != nil {
		return nil, err
	}

	res := &RetargetResult{
		TaskID:      taskID,
		OldHost:     oldH,
		NewHost:     newH,
		NewBaseURL:  strings.TrimSpace(newBaseURL),
		UpdatedGoal: newGoal,
	}

	// Update open goal nodes.
	rows, err := tx.QueryContext(ctx, `SELECT id, payload FROM exploration_nodes WHERE exploration_id=$1 AND kind=$2 AND state='open'`, expID, KindGoal)
	if err != nil {
		return nil, err
	}
	type goalUpd struct {
		id      int64
		payload string
	}
	var goalUpds []goalUpd
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var p map[string]any
		if json.Unmarshal(raw, &p) == nil {
			if txt, _ := p["text"].(string); txt != "" {
				if updated, ok := replaceHostCaseInsensitive(txt, oldH, newH); ok {
					p["text"] = updated
					b, _ := json.Marshal(p)
					goalUpds = append(goalUpds, goalUpd{id: id, payload: string(b)})
				}
			}
		}
	}
	rows.Close()
	for _, gu := range goalUpds {
		if _, err := tx.ExecContext(ctx, `UPDATE exploration_nodes SET payload=$1 WHERE id=$2`, gu.payload, gu.id); err != nil {
			return nil, err
		}
		res.GoalsUpdated++
	}

	// Update task_constraints referencing oldH.
	cRows, err := tx.QueryContext(ctx, `SELECT id, text FROM task_constraints WHERE exploration_id=$1`, expID)
	if err != nil {
		return nil, err
	}
	type cUpd struct {
		id   int64
		text string
	}
	var cUpds []cUpd
	for cRows.Next() {
		var id int64
		var txt string
		if err := cRows.Scan(&id, &txt); err != nil {
			cRows.Close()
			return nil, err
		}
		if updated, ok := replaceHostCaseInsensitive(txt, oldH, newH); ok {
			cUpds = append(cUpds, cUpd{id: id, text: updated})
		}
	}
	cRows.Close()
	for _, cu := range cUpds {
		if _, err := tx.ExecContext(ctx, `UPDATE task_constraints SET text=$1 WHERE id=$2`, cu.text, cu.id); err != nil {
			return nil, err
		}
		res.ConstraintsUpdated++
	}

	// Replace task_scope entries matching oldH with newH.
	delRes, err := tx.ExecContext(ctx, `DELETE FROM task_scope WHERE task_id=$1 AND LOWER(value)=$2`, taskID, oldH)
	if err != nil {
		return nil, err
	}
	if n, _ := delRes.RowsAffected(); n > 0 {
		res.ScopesUpdated += int(n)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_scope(task_id, kind, value, source, reason) VALUES ($1, 'subdomain', $2, 'manual', 'retargeted to new instance') ON CONFLICT DO NOTHING`, taskID, newH); err != nil {
		return nil, err
	}
	res.ScopesUpdated++

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}
