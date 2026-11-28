package egress

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/accessaudit"
	"github.com/Autumn-27/artex/intercept"
)

func TestAccessAuditCacheIsNotNetworkEvidence(t *testing.T) {
	keepProfile(t)
	s, err := accessaudit.Open(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("ok")) }))
	defer target.Close()
	SetProfile(Profile{Name: "test", RPS: 1000, Burst: 100, CacheTTL: time.Minute})
	ctx := intercept.WithTargetScope(accessaudit.WithStore(t.Context(), s), intercept.TargetScope{TaskID: "7"})
	client := &http.Client{Transport: Dynamic("test", nil)}
	for range 2 {
		req, _ := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	p, err := s.Search(t.Context(), accessaudit.Query{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 100})
	if err != nil || len(p.Events) != 2 || hits.Load() != 1 {
		t.Fatalf("hits=%d events=%+v err=%v", hits.Load(), p.Events, err)
	}
}
