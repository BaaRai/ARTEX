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
	tr.ftsq.index(ftsJob{rowid: 4242, id: "gone", text: "orphan-marker-xyz"})
	var n int
	if err := tr.db.QueryRow(`SELECT count(*) FROM ex_fts WHERE ex_fts MATCH '"orphan-marker-xyz"'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("orphan index rows = %d, %v; want 0", n, err)
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
