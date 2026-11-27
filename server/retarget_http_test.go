package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRetargetTaskHTTPHandler(t *testing.T) {
	skipWithoutDSN(t)

	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, "", m.dir, m.dir)
	t.Cleanup(func() { stopTestServer(t, s, cancel) })

	task, err := m.CreateTaskWithOptions("retarget-http", "Audit https://old.example.test/api", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	if _, err := task.Store.AddGoal(map[string]any{"summary": "Capture flags on https://old.example.test"}, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Store.AddConstraint("allow", "Only test old.example.test", "operator"); err != nil {
		t.Fatal(err)
	}
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "Enumerate old.example.test/docs"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Unauthenticated request must be rejected with 401.
	req401 := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/retarget", bytes.NewBufferString(`{"old_host":"old.example.test","new_base_url":"https://new.example.test"}`))
	req401.Header.Set("Content-Type", "application/json")
	rec401 := httptest.NewRecorder()
	h.ServeHTTP(rec401, req401)
	if rec401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d: %s", rec401.Code, rec401.Body.String())
	}

	// 2. Invalid/missing fields must return 400.
	for _, badBody := range []string{
		`{}`,
		`{"old_host":"old.example.test","new_base_url":"not-a-url"}`,
		`{"old_host":"","new_base_url":"https://new.example.test"}`,
	} {
		req400 := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/retarget", bytes.NewBufferString(badBody))
		req400.Header.Set("Content-Type", "application/json")
		req400.Header.Set("Authorization", "Bearer "+tok)
		rec400 := httptest.NewRecorder()
		h.ServeHTTP(rec400, req400)
		if rec400.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for body %s, got %d: %s", badBody, rec400.Code, rec400.Body.String())
		}
	}

	// 3. Unknown task returns 404.
	req404 := httptest.NewRequest("POST", "/api/tasks/999999/retarget", bytes.NewBufferString(`{"old_host":"old.example.test","new_base_url":"https://new.example.test"}`))
	req404.Header.Set("Content-Type", "application/json")
	req404.Header.Set("Authorization", "Bearer "+tok)
	rec404 := httptest.NewRecorder()
	h.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown task, got %d: %s", rec404.Code, rec404.Body.String())
	}

	// 4. Valid retarget updates goal, constraints, stale intent, and returns 200 JSON.
	req200 := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/retarget", bytes.NewBufferString(`{"old_host":"old.example.test","new_base_url":"https://new.example.test:8443"}`))
	req200.Header.Set("Content-Type", "application/json")
	req200.Header.Set("Authorization", "Bearer "+tok)
	rec200 := httptest.NewRecorder()
	h.ServeHTTP(rec200, req200)
	if rec200.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid retarget, got %d: %s", rec200.Code, rec200.Body.String())
	}

	var resp struct {
		TaskID       string `json:"task_id"`
		OldHost      string `json:"old_host"`
		NewHost      string `json:"new_host"`
		UpdatedGoal  string `json:"updated_goal"`
		StaleIntents int    `json:"stale_intents"`
	}
	if err := json.Unmarshal(rec200.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TaskID != task.ID || resp.OldHost != "old.example.test" || resp.NewHost != "new.example.test" {
		t.Fatalf("unexpected retarget response: %+v", resp)
	}
	if !strings.Contains(resp.UpdatedGoal, "new.example.test") || strings.Contains(resp.UpdatedGoal, "old.example.test") {
		t.Fatalf("expected updated_goal to reference new host only, got %q", resp.UpdatedGoal)
	}
	if resp.StaleIntents != 1 {
		t.Fatalf("expected 1 stale intent stopped, got %d", resp.StaleIntents)
	}

	node, err := task.Store.GetNode(intentID)
	if err != nil || node == nil {
		t.Fatal(err)
	}
	if node.State != "stopped" {
		t.Fatalf("expected stale intent stopped, got %q", node.State)
	}
}
