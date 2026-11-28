package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func ghidraToolSet(t *testing.T) *ToolSet {
	t.Helper()
	ts := NewToolSet(nil, "")
	ts.SetWorkBase(t.TempDir())
	return ts
}

func TestGhidraTools_ReportNotConfigured(t *testing.T) {
	t.Setenv("ARTEX_GHIDRA_URL", "")
	ts := ghidraToolSet(t)
	for _, tool := range []actool.CoreTool{
		ts.ghidraStatus(), ts.ghidraAnalyze(), ts.ghidraJob(),
		ts.ghidraFunctions(), ts.ghidraDecompile(), ts.ghidraStrings(), ts.ghidraAttackSurface(),
	} {
		res := callGuardTool(t, tool, `{"path":"a.bin","job_id":"j1","addr":"0x401000"}`)
		if !res.IsError {
			t.Fatalf("%s succeeded without a URL", tool.Name())
		}
		if !strings.Contains(res.Flatten(), "ARTEX_GHIDRA_URL") {
			t.Fatalf("%s does not name the setting: %q", tool.Name(), res.Flatten())
		}
	}
}

func TestGhidraAnalyze_UploadsFileFromWorkingDir(t *testing.T) {
	base := t.TempDir()
	runDir := filepath.Join(base, "tasks", "1", "worker")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := "\x7fELF fake"
	if err := os.WriteFile(filepath.Join(runDir, "libfoo.so"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var uploaded string
	var gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			return
		}
		defer file.Close()
		gotName = header.Filename
		b, _ := io.ReadAll(file)
		uploaded = string(b)
		_, _ = io.WriteString(w, `{"job_id":"job-42","status":"queued"}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_GHIDRA_URL", srv.URL)

	ts := NewToolSet(nil, "")
	ts.SetWorkBase(base)
	res, err := ts.ghidraAnalyze().Call(context.Background(), json.RawMessage(`{"path":"libfoo.so"}`), &actool.ToolContext{WorkingDir: runDir})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("analyze failed: %s", res.Flatten())
	}
	if uploaded != payload {
		t.Fatalf("uploaded body = %q", uploaded)
	}
	if gotName != "libfoo.so" {
		t.Fatalf("filename = %q", gotName)
	}
	if !strings.Contains(res.Flatten(), "job-42") {
		t.Fatalf("result = %s", res.Flatten())
	}
}

func TestGhidraAnalyze_RequiresRunDirectory(t *testing.T) {
	t.Setenv("ARTEX_GHIDRA_URL", "http://127.0.0.1:1")
	res := callGuardTool(t, ghidraToolSet(t).ghidraAnalyze(), `{"path":"a.bin"}`)
	if !res.IsError || !strings.Contains(res.Flatten(), "working directory") {
		t.Fatalf("upload without a run directory was not refused: %s", res.Flatten())
	}
}

func TestGhidraAnalyze_RefusesPathOutsideWorkingDir(t *testing.T) {
	base := t.TempDir()
	runDir := filepath.Join(base, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_GHIDRA_URL", srv.URL)

	ts := NewToolSet(nil, "")
	ts.SetWorkBase(base)
	tool := ts.ghidraAnalyze()
	res, err := tool.Call(context.Background(), json.RawMessage(`{"path":"../secret.bin"}`), &actool.ToolContext{WorkingDir: runDir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("escaped the run directory: %s", res.Flatten())
	}
	if called {
		t.Fatal("the sidecar was called for an out-of-tree path")
	}
}

func TestGhidraFunctions_ClampsPageSize(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"functions":[]}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_GHIDRA_URL", srv.URL)

	ts := ghidraToolSet(t)
	res := callGuardTool(t, ts.ghidraFunctions(), `{"job_id":"j1","limit":5000,"search":"main"}`)
	if res.IsError {
		t.Fatalf("functions failed: %s", res.Flatten())
	}
	if !strings.Contains(query, "limit=200") {
		t.Fatalf("page size not clamped: %q", query)
	}
	if !strings.Contains(query, "q=main") {
		t.Fatalf("search dropped: %q", query)
	}
}

func TestGhidraTools_ClipOversizedResult(t *testing.T) {
	big := `{"blob":"` + strings.Repeat("a", ghidraMaxToolBytes) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_GHIDRA_URL", srv.URL)

	ts := ghidraToolSet(t)
	out := callGuardTool(t, ts.ghidraFunctions(), `{"job_id":"j1"}`).Flatten()
	if !strings.Contains(out, "truncated") {
		t.Fatalf("oversized result was not marked as truncated (len %d)", len(out))
	}
	if len(out) > ghidraMaxToolBytes+256 {
		t.Fatalf("result not clipped: %d bytes", len(out))
	}
}

func TestGhidraStatus_ReportsEndpointAndHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/health":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/v1/version":
			_, _ = io.WriteString(w, `{"version":"1.2.1"}`)
		case "/v1/capabilities":
			_, _ = io.WriteString(w, `{"decompile":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("ARTEX_GHIDRA_URL", srv.URL)

	out := callGuardTool(t, ghidraToolSet(t).ghidraStatus(), `{}`).Flatten()
	for _, want := range []string{srv.URL, `"status":"ok"`, `"version":"1.2.1"`, `"decompile":true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q: %s", want, out)
		}
	}
}
