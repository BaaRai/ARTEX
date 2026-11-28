package traffic

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/accessaudit"
)

func TestAccessAuditProxyAndImportedProvenance(t *testing.T) {
	audit, err := accessaudit.Open(filepath.Join(t.TempDir(), "access.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	tr, _, addr := startProxy(t)
	tr.SetAccessAudit(audit)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	resp, err := proxyClient(t, tr, addr).Get(target.URL + "/k5613?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := tr.RecordExternal(ExternalExchange{Method: "GET", URL: "https://external.example/only-reported", Status: 201}); err != nil {
		t.Fatal(err)
	}
	p, err := audit.Search(t.Context(), accessaudit.Query{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Events) != 2 {
		t.Fatalf("events=%+v", p.Events)
	}
	if p.Events[0].Source != "extension_ingest" || p.Events[0].Outcome != "reported_exchange" || p.Events[1].Source != "recording_proxy" || p.Events[1].Status != 204 {
		t.Fatalf("%+v", p.Events)
	}
	for _, e := range p.Events {
		if e.TaskID != "" {
			t.Fatal("fabricated attribution", e)
		}
	}
	if _, _, err := tr.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	p, err = audit.Search(t.Context(), accessaudit.Query{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 100})
	if err != nil || len(p.Events) != 2 {
		t.Fatalf("deleting full capture erased audit: %+v %v", p, err)
	}
}
