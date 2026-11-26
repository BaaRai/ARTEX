package traffic

import (
	"bytes"
	"net/http"
	"testing"
)

// A body search right after recording finds the exchange, though indexing
// now happens on the background queue.
func TestBodySearchSeesJustRecordedExchange(t *testing.T) {
	tr, _ := openTraffic(t)
	if tr.ftsq == nil {
		t.Skip("no full-text index in this build")
	}
	for i := range 20 {
		body := bytes.Repeat([]byte("filler text "), 5000)
		if i == 19 {
			body = append(body, "apiKey=AKIAEXAMPLE123"...)
		}
		tr.record(newFlow("app.example", "GET", "/static/app.js", nil, body))
	}
	got, err := tr.query("", "", "AKIAEXAMPLE123", 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("body search = %d results, %v; want 1", len(got), err)
	}
}

// A queued entry whose exchange was deleted before the worker reached it is
// dropped, so it cannot attach to a later exchange that reuses the rowid.
func TestQueuedIndexSkipsDeletedExchange(t *testing.T) {
	tr, _ := openTraffic(t)
	if tr.ftsq == nil {
		t.Skip("no full-text index in this build")
	}
	if _, err := tr.db.Exec(`INSERT INTO ex_fts_pending(rowid,id,content) VALUES(4242,'gone','orphan-marker-xyz')`); err != nil {
		t.Fatal(err)
	}
	tr.ftsq.index(ftsJob{rowid: 4242, id: "gone"})
	var indexed, pending int
	if err := tr.db.QueryRow(`SELECT count(*) FROM ex_fts WHERE ex_fts MATCH '"orphan-marker-xyz"'`).Scan(&indexed); err != nil || indexed != 0 {
		t.Fatalf("orphan index rows = %d, %v; want 0", indexed, err)
	}
	if err := tr.db.QueryRow(`SELECT count(*) FROM ex_fts_pending`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending rows = %d, %v; want 0", pending, err)
	}
}

// An exchange recorded but not yet indexed when the process stops is indexed
// on the next Open instead of staying invisible to body search.
func TestPendingIndexSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tr.ftsq == nil {
		tr.Close()
		t.Skip("no full-text index in this build")
	}
	tr.ftsq.close() // the worker is gone, as after a kill
	tr.record(newFlow("app.example", "GET", "/config.js", nil, []byte("var key = 'restart-marker-77';")))
	tr.Close()

	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	got, err := tr.query("", "", "restart-marker-77", 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("body search after restart = %d results, %v; want 1", len(got), err)
	}
}

// BenchmarkRecordScript is the time the proxy holds a response for, recording a
// 1 MiB script body with the indexer idle.
func BenchmarkRecordScript(b *testing.B) {
	tr, err := Open(b.TempDir(), "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer tr.Close()
	body := bytes.Repeat([]byte("function a(b){return b.c+\"x\"};var q=1;"), 1<<20/40)
	for b.Loop() {
		f := newFlow("app.example", "GET", "/static/app.js", nil, body)
		f.Response.Header = http.Header{"Content-Type": []string{"application/javascript"}}
		tr.record(f)
		b.StopTimer()
		if tr.ftsq != nil {
			tr.ftsq.wait()
		}
		b.StartTimer()
	}
}
