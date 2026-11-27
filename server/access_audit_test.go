package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/accessaudit"
	"github.com/Autumn-27/artex/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

func TestAccessAuditQueryValidation(t *testing.T) {
	store, err := accessaudit.Open(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := &Server{m: &Manager{audit: store}}
	store.Record(accessaudit.Event{URL: "https://example.com/k5613?secret=hidden", Source: "native_http"})
	for _, query := range []string{"limit=201", "before=-1", "from=yesterday", "from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z", "from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z"} {
		w := httptest.NewRecorder()
		s.getAccessAudit(w, httptest.NewRequest("GET", "/api/access-audit?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.getAccessAudit(w, httptest.NewRequest("GET", "/api/access-audit?url=https%3A%2F%2Fexample.com%2Fk5613%3Fsecret%3Dhidden", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "k5613") || strings.Contains(w.Body.String(), "hidden") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
func TestAccessAuditNativeHTTPCaptureOffAndAuthentication(t *testing.T) {
	s, request := ingestServer(t)
	// The shared traffic parent must remain traversable for the executor to load
	// the public recording CA; the private audit directory must not hide that CA.
	parent, statErr := os.Stat(filepath.Join(s.m.dir, "traffic"))
	if statErr != nil || parent.Mode().Perm()&0005 != 0005 {
		t.Fatalf("public CA parent inaccessible: %v %v", parent, statErr)
	}

	if err := s.m.SetTrafficEnabled(false); err != nil {
		t.Fatal(err)
	}
	s.m.traffic.SetAccessAudit(s.m.audit)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/k5613?password=secret", 302)
			return
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	ctx := intercept.WithTaskContext(t.Context(), "42", "worker", nil)
	spec, _ := json.Marshal(map[string]string{"url": target.URL + "/start"})
	res, err := s.runHTTPTool(ctx, "test_http", spec, nil, &actool.ToolContext{})
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Flatten())
	}
	w := request("GET", "/api/access-audit?url=k5613&task=42", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "response_headers") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	rows, _ := s.m.traffic.Count()
	if rows != 0 {
		t.Fatalf("body capture off but wrote %d exchanges", rows)
	}
	unauth := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauth, httptest.NewRequest("GET", "/api/access-audit", nil))
	if unauth.Code != 401 {
		t.Fatalf("anonymous audit returned %d", unauth.Code)
	}
	// External data does not gain task identity or become a local observation.
	w = request("POST", "/api/traffic/ingest", `{"exchanges":[{"method":"GET","url":"https://reported.example/k5613?token=secret","status":200,"task_id":"42","source":"native_http"}]}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = request("GET", "/api/access-audit?source=extension_ingest", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "reported_exchange") || strings.Contains(w.Body.String(), `"task_id":"42"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
