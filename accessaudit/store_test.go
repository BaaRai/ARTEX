package accessaudit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "access.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func window() Query {
	return Query{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 100}
}
func TestStoreRedactionPersistenceAndLiteralSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd?# name", "access.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(Event{URL: "https://user:password@EXAMPLE.com/k5613/a_%25?token=secret#private", Source: "native_http", Outcome: "response_headers", Identity: Identity{TaskID: "7"}})
	if s.Failures() != 0 {
		t.Fatal(s.Failures())
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := window()
	q.Host = "EXAMPLE.com"
	q.URL = "k5613/a_%25"
	q.TaskID = "7"
	p, err := s.Search(t.Context(), q)
	if err != nil || len(p.Events) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if p.Events[0].URL != "https://example.com/k5613/a_%25" {
		t.Fatal(p.Events[0])
	}
	q.URL = "%"
	p, err = s.Search(t.Context(), q)
	if err != nil || len(p.Events) != 1 {
		t.Fatal(p, err)
	}
	q.URL = "secret"
	p, _ = s.Search(t.Context(), q)
	if len(p.Events) != 0 {
		t.Fatal(p)
	}
	s.Close()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"password", "token=secret", "#private"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("persisted %s", secret)
		}
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}
func TestStorePaginationConcurrencyAndBounds(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				s.Record(Event{URL: "http://example.com/a", Source: "native_http"})
			}
		}()
	}
	wg.Wait()
	if s.Failures() != 0 {
		t.Fatal(s.Failures())
	}
	q := window()
	q.Limit = 7
	seen := map[int64]bool{}
	for {
		p, err := s.Search(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Events {
			if seen[e.ID] {
				t.Fatal("duplicate cursor row")
			}
			seen[e.ID] = true
		}
		if p.Next == 0 {
			break
		}
		q.Before = p.Next
	}
	if len(seen) != 80 {
		t.Fatal(len(seen))
	}
	q.Limit = 201
	if _, err := s.Search(t.Context(), q); err == nil {
		t.Fatal("unbounded page")
	}
	q = window()
	q.To = q.From.Add(32 * 24 * time.Hour)
	if _, err := s.Search(t.Context(), q); err == nil {
		t.Fatal("unbounded time")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Search(ctx, window()); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestStoreRetentionCapacityAndFailures(t *testing.T) {
	s := testStore(t)
	s.Record(Event{URL: "http://example.com/old"})
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE access_events SET ts=?`, time.Now().Add(-31*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	s.Record(Event{URL: "http://example.com/new"})
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM access_events`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	// Jump the sequence to exercise the bounded-window cutoff without allocating
	// half a million test rows. IDs are monotonic even across deletion/restart.
	if _, err := s.db.Exec(`UPDATE sqlite_sequence SET seq=? WHERE name='access_events'`, MaxEvents+10); err != nil {
		t.Fatal(err)
	}
	s.Record(Event{URL: "http://example.com/latest"})
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT count(*) FROM access_events`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	for _, u := range []string{"garbage", "file:///etc/passwd", "https://example.com/" + strings.Repeat("x", MaxURL)} {
		s.Record(Event{URL: u})
	}
	if s.Failures() != 3 {
		t.Fatal(s.Failures())
	}
	s.Close()
	s.Record(Event{URL: "http://example.com/after-close"})
	if s.Failures() != 4 {
		t.Fatal(s.Failures())
	}
}

func TestStoreTimeWindowAndUnknownTask(t *testing.T) {
	s := testStore(t)
	for _, path := range []string{"before", "from", "to"} {
		s.Record(Event{URL: "https://example.com/" + path})
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	from := time.Now().Add(-time.Hour).Truncate(time.Second)
	to := from.Add(time.Minute)
	for idx, ts := range []time.Time{from.Add(-time.Millisecond), from, to} {
		if _, err := s.db.Exec(`UPDATE access_events SET ts=? WHERE id=?`, ts.UnixMilli(), idx+1); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Search(t.Context(), Query{From: from, To: to, Limit: 100})
	if err != nil || len(p.Events) != 1 || p.Events[0].URL != "https://example.com/from" {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = s.Search(t.Context(), Query{From: from, To: from.Add(500 * time.Microsecond), Limit: 100})
	if err != nil || len(p.Events) != 1 {
		t.Fatalf("sub-ms end dropped prior observation: %+v %v", p, err)
	}
	p, err = s.Search(t.Context(), Query{From: from.Add(500 * time.Microsecond), To: from.Add(time.Millisecond), Limit: 100})
	if err != nil || len(p.Events) != 0 {
		t.Fatalf("sub-ms start included prior observation: %+v %v", p, err)
	}
	p, err = s.Search(t.Context(), Query{From: from, To: to, Limit: 100, TaskID: "7"})
	if err != nil || len(p.Events) != 0 {
		t.Fatalf("unknown attribution guessed: %+v %v", p, err)
	}
	if _, err := s.db.Exec(`UPDATE access_events SET ts=?`, time.Now().Add(-31*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	p, err = s.Search(t.Context(), Query{From: time.Now().Add(-32 * 24 * time.Hour), To: time.Now().Add(-2 * 24 * time.Hour), Limit: 100})
	if err != nil || len(p.Events) != 0 {
		t.Fatalf("expired records exposed: %+v %v", p, err)
	}
}

func TestStoreBoundedQueueAccountsForLoss(t *testing.T) {
	s := testStore(t)
	// Hold the only SQL connection so producers can outrun the writer, without
	// depending on disk speed. Overflow must be explicit, never an unbounded wait.
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for range 1200 {
		s.Record(Event{URL: "http://example.com/overflow"})
	}
	if s.Failures() == 0 {
		conn.Close()
		t.Fatal("queue was not bounded")
	}
	conn.Close()
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM access_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if uint64(count)+s.Failures() != 1200 {
		t.Fatalf("stored=%d lost=%d", count, s.Failures())
	}
}

func TestStoreCloseDrainsPendingEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 200 {
		s.Record(Event{URL: "http://example.com/drain"})
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Failures() != 0 {
		t.Fatal(s.Failures())
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM access_events`).Scan(&count); err != nil || count != 200 {
		t.Fatal(count, err)
	}
}

func TestStoreRelativeDataDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, filepath.Join(t.TempDir(), "data", "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(relative)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Record(Event{URL: "http://example.com/relative"})
	p, err := s.Search(t.Context(), window())
	if err != nil || len(p.Events) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}
