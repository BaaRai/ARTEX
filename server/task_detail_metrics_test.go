package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestTaskDetailReturnsListMetrics(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	task, err := m.CreateTask("detail metrics", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	defer func() { _ = m.pg.DeleteTask(taskID) }()

	// Seed the aggregates the metrics query reads: one result-activity with
	// tokens, one non-result row that must be excluded, and two goals (1 met).
	if _, err := m.pg.Exec(`
INSERT INTO activity(
    exploration_id, kind, input_tokens, output_tokens,
    cache_read_tokens, cache_write_tokens, created_at
) VALUES ($1,'result',11,7,3,2,now()), ($1,'tool_result',999,999,999,999,now())`,
		task.ExpID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.Exec(`
INSERT INTO exploration_nodes(exploration_id, kind, payload, state)
VALUES ($1,'goal','{}','met'), ($1,'goal','{}','open')`, task.ExpID); err != nil {
		t.Fatal(err)
	}

	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	var probe struct {
		Tokens struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			CacheReadTokens  int `json:"cache_read_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"tokens"`
		GoalsMet   int `json:"goals_met"`
		GoalsTotal int `json:"goals_total"`
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Tokens.InputTokens != 11 || probe.Tokens.OutputTokens != 7 ||
		probe.Tokens.CacheReadTokens != 3 || probe.Tokens.CacheWriteTokens != 2 {
		t.Fatalf("detail tokens not filled from metrics: %+v", probe.Tokens)
	}
	if probe.GoalsMet != 1 || probe.GoalsTotal != 2 {
		t.Fatalf("detail goal progress not filled from metrics: %d/%d", probe.GoalsMet, probe.GoalsTotal)
	}

	// The list endpoint must report the same numbers for the same task.
	listReq := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Tasks []struct {
			ID     string `json:"id"`
			Tokens struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"tokens"`
			GoalsMet   int `json:"goals_met"`
			GoalsTotal int `json:"goals_total"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lt := range listed.Tasks {
		if lt.ID != task.ID {
			continue
		}
		found = true
		if lt.Tokens.InputTokens != probe.Tokens.InputTokens ||
			lt.Tokens.OutputTokens != probe.Tokens.OutputTokens ||
			lt.GoalsMet != probe.GoalsMet || lt.GoalsTotal != probe.GoalsTotal {
			t.Fatalf("list and detail metrics disagree: list=%+v/%d detail=%+v/%d",
				lt.Tokens, lt.GoalsMet, probe.Tokens, probe.GoalsTotal)
		}
	}
	if !found {
		t.Fatalf("task %s missing from list response", task.ID)
	}
}
