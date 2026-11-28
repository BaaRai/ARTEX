package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestExtendTaskHTTPHandlerAndEngineDeadline(t *testing.T) {
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

	task, err := m.CreateTaskWithOptions("extend-test", "Audit https://target.example.test", db.TaskCreateOptions{
		TimeoutSeconds: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })

	origDL, err := m.StampTaskFirstRun(task.ID)
	if err != nil || origDL <= 0 {
		t.Fatalf("StampTaskFirstRun: dl=%d err=%v", origDL, err)
	}
	s.engine.deadline.Store(task.ID, origDL)

	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 400 on invalid / out-of-range seconds.
	for _, body := range []string{`{}`, `{"seconds":0}`, `{"seconds":-10}`, `{"seconds":3601}`, `not-json`} {
		req := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/extend", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d (%s)", body, rec.Code, rec.Body.String())
		}
	}

	// 2. 404 on unknown task.
	req404 := httptest.NewRequest("POST", "/api/tasks/999999/extend", bytes.NewBufferString(`{"seconds":300}`))
	req404.Header.Set("Content-Type", "application/json")
	req404.Header.Set("Authorization", "Bearer "+tok)
	rec404 := httptest.NewRecorder()
	h.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec404.Code)
	}

	// 3. 200 extends DB timeout_seconds + deadline_at + engine deadline.
	req200 := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/extend", bytes.NewBufferString(`{"seconds":900}`))
	req200.Header.Set("Content-Type", "application/json")
	req200.Header.Set("Authorization", "Bearer "+tok)
	rec200 := httptest.NewRecorder()
	h.ServeHTTP(rec200, req200)
	if rec200.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec200.Code, rec200.Body.String())
	}

	var resp struct {
		ID               string `json:"id"`
		AddedSeconds     int    `json:"added_seconds"`
		TimeoutSeconds   int    `json:"timeout_seconds"`
		DeadlineAt       int64  `json:"deadline_at"`
		RemainingSeconds int64  `json:"remaining_seconds"`
	}
	if err := json.Unmarshal(rec200.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != task.ID || resp.AddedSeconds != 900 || resp.TimeoutSeconds != 1500 {
		t.Fatalf("unexpected extend response: %+v", resp)
	}
	if resp.DeadlineAt < origDL+895 || resp.RemainingSeconds <= 0 {
		t.Fatalf("expected deadline pushed forward by ~900s (orig=%d), got %+v", origDL, resp)
	}
	if gotEngineDL := s.engine.taskDeadline(task); gotEngineDL != resp.DeadlineAt {
		t.Fatalf("engine taskDeadline=%d, want %d", gotEngineDL, resp.DeadlineAt)
	}

	// Drain any notify triggered by extend without hanging.
	select {
	case <-task.notify:
	case <-time.After(time.Second):
		t.Fatal("expected extend to wake planner via task.Notify()")
	}

	// 4. Terminal task returns 409.
	if err := m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	req409 := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/extend", bytes.NewBufferString(`{"seconds":300}`))
	req409.Header.Set("Content-Type", "application/json")
	req409.Header.Set("Authorization", "Bearer "+tok)
	rec409 := httptest.NewRecorder()
	h.ServeHTTP(rec409, req409)
	if rec409.Code != http.StatusConflict {
		t.Fatalf("expected 409 on terminal task, got %d (%s)", rec409.Code, rec409.Body.String())
	}

	// 5. Unbounded task (timeout_seconds=0) returns 409 Conflict.
	unboundedTask, err := m.CreateTaskWithOptions("extend-unbounded", "Audit https://target.example.test", db.TaskCreateOptions{
		TimeoutSeconds: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(unboundedTask.ID, DeleteTaskOptions{}) })

	reqUnb := httptest.NewRequest("POST", "/api/tasks/"+unboundedTask.ID+"/extend", bytes.NewBufferString(`{"seconds":300}`))
	reqUnb.Header.Set("Content-Type", "application/json")
	reqUnb.Header.Set("Authorization", "Bearer "+tok)
	recUnb := httptest.NewRecorder()
	h.ServeHTTP(recUnb, reqUnb)
	if recUnb.Code != http.StatusConflict {
		t.Fatalf("expected 409 on unbounded task (timeout_seconds=0), got %d (%s)", recUnb.Code, recUnb.Body.String())
	}
}
