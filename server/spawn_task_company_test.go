package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestSpawnTaskCompanyIDsScopeAndFilter(t *testing.T) {
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

	// 1. Schema exposes company_ids.
	schema := s.toolSpawnTask().InputSchema()
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["company_ids"]; !ok {
		t.Fatalf("spawn_task schema missing company_ids property: %+v", props)
	}

	// 2. Create two companies; acme has a declared domain scope.
	acmeID, _, _, _, _, err := m.pg.Companies().CreateCompanyWithScope("spawn-acme", "", []db.ScopeInput{
		{Kind: "domain", Value: "acme.example.test"},
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.Companies().DeleteCompany(acmeID) })

	otherID, _, err := m.pg.Companies().UpsertCompany("spawn-other", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.Companies().DeleteCompany(otherID) })

	// 3. Invalid or nonexistent company_ids return a tool error.
	for _, bad := range []string{
		`{"description":"bad","goal":"audit","company_ids":[0]}`,
		`{"description":"bad","goal":"audit","company_ids":[-5]}`,
		`{"description":"bad","goal":"audit","company_ids":[999999999]}`,
	} {
		res, callErr := s.toolSpawnTask().Call(ctx, json.RawMessage(bad), nil)
		if callErr != nil || !res.IsError {
			t.Fatalf("expected error for %s, got err=%v res=%s", bad, callErr, res.Flatten())
		}
	}

	// 4. Valid (and duplicated) company_ids normalize cleanly, populate task_scope, and appear in company filter.
	in, _ := json.Marshal(map[string]any{
		"description": "Acme assessment",
		"goal":        "Map acme.example.test",
		"company_ids": []int64{acmeID, acmeID},
	})
	res, err := s.toolSpawnTask().Call(ctx, in, nil)
	if err != nil || res.IsError {
		t.Fatalf("spawn_task with company_ids failed: err=%v res=%s", err, res.Flatten())
	}
	taskID := strings.TrimSpace(strings.TrimPrefix(res.Flatten(), "task created:"))
	if taskID == "" {
		t.Fatalf("unexpected spawn_task output: %q", res.Flatten())
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(taskID, DeleteTaskOptions{}) })

	taskObj, ok := m.Task(taskID)
	if !ok || taskObj == nil {
		t.Fatalf("spawned task %q not found in manager", taskID)
	}
	if !slices.Equal(taskObj.lifecycleSnapshot().CompanyIDs, []int64{acmeID}) {
		t.Fatalf("spawned task CompanyIDs = %v, want [%d]", taskObj.lifecycleSnapshot().CompanyIDs, acmeID)
	}

	taskNum, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := m.Assets().ListTaskScope(taskNum)
	if err != nil {
		t.Fatal(err)
	}
	var sawAcmeScope bool
	for _, sc := range scopes {
		if (sc.Kind == "company" && sc.CompanyID != nil && *sc.CompanyID == acmeID) ||
			strings.EqualFold(sc.Domain, "acme.example.test") ||
			strings.EqualFold(sc.Value, "acme.example.test") {
			sawAcmeScope = true
		}
	}
	if !sawAcmeScope {
		t.Fatalf("expected task_scope to reference company %d / acme.example.test, got %+v", acmeID, scopes)
	}

	// 5. GET /api/tasks?company=<acmeID> lists the spawned task; ?company=<otherID> does not.
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	queryTasks := func(cid int64) []string {
		req := httptest.NewRequest("GET", "/api/tasks?company="+strconv.FormatInt(cid, 10), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/tasks?company=%d status=%d body=%s", cid, rec.Code, rec.Body.String())
		}
		var out struct {
			Tasks []struct {
				ID string `json:"id"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(out.Tasks))
		for _, item := range out.Tasks {
			ids = append(ids, item.ID)
		}
		return ids
	}

	if got := queryTasks(acmeID); !slices.Contains(got, taskID) {
		t.Fatalf("GET /api/tasks?company=%d missing spawned task %s: got %v", acmeID, taskID, got)
	}
	if got := queryTasks(otherID); slices.Contains(got, taskID) {
		t.Fatalf("GET /api/tasks?company=%d unexpectedly included spawned task %s: got %v", otherID, taskID, got)
	}
}
