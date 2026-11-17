package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// A subscription CLI profile has no key, no base URL and may leave the model
// empty, yet must load, join the failover pool, pass task resolution and pass
// the connection test, which runs the CLI found on PATH.
func TestCLIKeylessProfileLifecycle(t *testing.T) {
	skipWithoutDSN(t)
	bin := t.TempDir()
	script := "#!/bin/sh\ncat > /dev/null\nprintf '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"OK\",\"structured_output\":{\"text\":\"OK\",\"tool_calls\":[]},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if m.pg == nil {
		t.Skip("no postgres for this run")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())

	pid, err := m.pg.SaveProfile(&db.LLMProfile{Name: "claude-sub-" + t.Name(), Format: "claude-cli", Model: ""})
	if err != nil {
		t.Fatalf("the format CHECK must accept claude-cli: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec("DELETE FROM llm_profiles WHERE id=$1", pid) })
	stored, err := m.pg.ProfileByID(pid)
	if err != nil || stored == nil {
		t.Fatalf("ProfileByID: %v %v", stored, err)
	}

	if _, ok := s.loadProfileConfig(pid); !ok {
		t.Fatal("keyless claude-cli profile must load")
	}
	if err := s.validateTaskProfileIDs([]int64{pid}); err != nil {
		t.Fatalf("task chain must accept keyless claude-cli: %v", err)
	}
	if res := s.resolutionFromProfile(stored, "test"); !res.Available {
		t.Fatalf("task resolution marked the profile unavailable: %s", res.Reason)
	}
	pool, err := m.pg.PoolProfiles()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range pool {
		found = found || p.ID == pid
	}
	if !found {
		t.Fatal("keyless claude-cli profile must join the failover pool")
	}

	post := func(handler http.HandlerFunc, path string, body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	rec := post(s.testLLM, "/api/llm/test", map[string]any{"profile_id": pid, "provider": "claude-cli", "model": "", "api_key": ""})
	var resp struct {
		OK    bool   `json:"ok"`
		Reply string `json:"reply"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK || resp.Reply != "OK" {
		t.Fatalf("connection test on keyless claude-cli: ok=%v reply=%q error=%q", resp.OK, resp.Reply, resp.Error)
	}

	if rec := post(s.pgListModels, "/api/llm/models", map[string]any{"provider": "claude-cli"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("model listing for a CLI format: status %d, want 400", rec.Code)
	}
}
