package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// The task report summarises the task's own assets, not every asset in the
// shared library.
func TestReportCountsOnlyTheTaskAssets(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		db.SkipPGUnavailable(t, err)
	}
	defer m.Close()
	mine, err := m.CreateTask("report mine", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.CreateTask("report other", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	mineID, _ := strconv.ParseInt(mine.ID, 10, 64)
	otherID, _ := strconv.ParseInt(other.ID, 10, 64)
	defer func() {
		_ = m.pg.DeleteTask(mineID)
		_ = m.pg.DeleteTask(otherID)
	}()

	stamp := time.Now().UnixNano()
	var ids []int64
	for i, taskID := range []int64{mineID, otherID, otherID} {
		root := fmt.Sprintf("r%d-%d.invalid", stamp, i)
		var id int64
		if err := m.pg.QueryRow(`INSERT INTO assets(type,domain,root_domain,task_ids)
			VALUES ('subdomain',$1,$2,ARRAY[$3]::bigint[]) RETURNING id`, "a."+root, root, taskID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	defer func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=ANY($1::bigint[])`, ids) }()

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	defer stopTestServer(t, s, cancel)
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/report?task="+mine.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report status=%d body=%s", rec.Code, rec.Body.String())
	}
	var line string
	for _, l := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(l, "- Assets: ") {
			line = l
		}
	}
	if line != "- Assets: subdomain 1" {
		t.Fatalf("summary line %q, want %q", line, "- Assets: subdomain 1")
	}
}
