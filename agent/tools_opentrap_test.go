package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func opentrapToolSet(t *testing.T) *ToolSet {
	t.Helper()
	ts := NewToolSet(nil, "")
	ts.SetWorkBase(t.TempDir())
	return ts
}

func TestOpentrapTools_ReportNotConfigured(t *testing.T) {
	t.Setenv("ARTEX_OPENTRAP_URL", "")
	ts := opentrapToolSet(t)
	for _, tool := range []actool.CoreTool{
		ts.opentrapStatus(), ts.opentrapHistory(), ts.opentrapHistoryItem(),
		ts.opentrapScopeGet(), ts.opentrapScopePut(), ts.opentrapReplay(),
	} {
		res := callGuardTool(t, tool, `{"url":"http://example.com","id":"h1","rules":"{}","body":"","update_content_length":true,"follow_redirects":true}`)
		if !res.IsError {
			t.Fatalf("%s succeeded without a URL", tool.Name())
		}
		if !strings.Contains(res.Flatten(), "ARTEX_OPENTRAP_URL") {
			t.Fatalf("%s does not name the setting: %q", tool.Name(), res.Flatten())
		}
	}
}

func TestOpentrapHistory_PaginatesAndClamps(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	ts := opentrapToolSet(t)
	res := callGuardTool(t, ts.opentrapHistory(), `{"limit":5000,"offset":20}`)
	if res.IsError {
		t.Fatalf("history failed: %s", res.Flatten())
	}
	if !strings.Contains(query, "limit=200") {
		t.Fatalf("page size not clamped: %q", query)
	}
	if !strings.Contains(query, "offset=20") {
		t.Fatalf("offset dropped: %q", query)
	}
}

func TestOpentrapHistoryItem_RequiresID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"h1"}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	ts := opentrapToolSet(t)
	res := callGuardTool(t, ts.opentrapHistoryItem(), `{"id":""}`)
	if !res.IsError || !strings.Contains(res.Flatten(), "required") {
		t.Fatalf("empty id accepted: %s", res.Flatten())
	}
}

func TestOpentrapScopePut_SendsRules(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	ts := opentrapToolSet(t)
	res := callGuardTool(t, ts.opentrapScopePut(), `{"rules":"{\"include\":[\"example.com\"]}"}`)
	if res.IsError {
		t.Fatalf("scope_put failed: %s", res.Flatten())
	}
	if !strings.Contains(gotBody, "example.com") {
		t.Fatalf("rules not sent: %s", gotBody)
	}
}

func TestOpentrapReplay_SendsHeaders(t *testing.T) {
	var gotHeaders http.Header
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"status":200}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	ts := opentrapToolSet(t)
	res := callGuardTool(t, ts.opentrapReplay(), `{"url":"http://target.example.com","body":"{\"k\":\"v\"}","update_content_length":true,"follow_redirects":true}`)
	if res.IsError {
		t.Fatalf("replay failed: %s", res.Flatten())
	}
	for _, h := range []string{"X-OpenTrap-URL", "X-OpenTrap-Update-Content-Length", "X-OpenTrap-Follow-Redirects"} {
		if gotHeaders.Get(h) == "" {
			t.Fatalf("header %s missing: %v", h, gotHeaders)
		}
	}
	if gotHeaders.Get("X-OpenTrap-URL") != "http://target.example.com" {
		t.Fatalf("X-OpenTrap-URL = %q", gotHeaders.Get("X-OpenTrap-URL"))
	}
	if gotBody != `{"k":"v"}` {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestOpentrapReplay_RequiresURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":200}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	ts := opentrapToolSet(t)
	res := callGuardTool(t, ts.opentrapReplay(), `{"url":""}`)
	if !res.IsError || !strings.Contains(res.Flatten(), "required") {
		t.Fatalf("empty url accepted: %s", res.Flatten())
	}
}

func TestOpentrapStatus_ReportsEndpointAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","version":"1.0"}`)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	out := callGuardTool(t, opentrapToolSet(t).opentrapStatus(), `{}`).Flatten()
	for _, want := range []string{srv.URL, `"status":"ok"`, `"version":"1.0"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q: %s", want, out)
		}
	}
}

func TestOpentrapTools_ClipOversizedResult(t *testing.T) {
	big := `{"items":[` + strings.Repeat(`{"x":"` + strings.Repeat("a", 1000) + `"}`, 200) + `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()
	t.Setenv("ARTEX_OPENTRAP_URL", srv.URL)

	ts := opentrapToolSet(t)
	out := callGuardTool(t, ts.opentrapHistory(), `{}`).Flatten()
	if !strings.Contains(out, "truncated") {
		t.Fatalf("oversized result was not marked as truncated (len %d)", len(out))
	}
	if len(out) > opentrapMaxToolBytes+256 {
		t.Fatalf("result not clipped: %d bytes", len(out))
	}
}