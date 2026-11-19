package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadyCacheReusesFreshResult(t *testing.T) {
	var c readyCache
	var calls int32
	ping := func(context.Context) bool { atomic.AddInt32(&calls, 1); return true }

	if !c.check(ping) {
		t.Fatal("first check: want true")
	}
	if !c.check(ping) {
		t.Fatal("second check within TTL: want true")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("ping called %d times, want 1 (second call should reuse the cache)", n)
	}
}

func TestReadyCacheRefetchesAfterTTL(t *testing.T) {
	var c readyCache
	var calls int32
	ping := func(context.Context) bool {
		n := atomic.AddInt32(&calls, 1)
		return n == 1 // first ping ok, second ping reports down
	}

	if !c.check(ping) {
		t.Fatal("first check: want true")
	}
	// Back-date the cached result past readyTTL instead of sleeping for it.
	c.mu.Lock()
	c.at = time.Now().Add(-2 * readyTTL)
	c.mu.Unlock()

	if c.check(ping) {
		t.Fatal("check after TTL expiry: want false (second ping)")
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("ping called %d times, want 2 (expiry should trigger a re-ping)", n)
	}
}

func TestReadyCacheDedupesInFlightPings(t *testing.T) {
	var c readyCache
	var calls int32
	release := make(chan struct{})
	ping := func(context.Context) bool {
		atomic.AddInt32(&calls, 1)
		<-release // held open until every caller has had a chance to join this ping
		return true
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = c.check(ping)
		}(i)
	}
	// Give every goroutine a chance to reach check() and either start the
	// ping or see c.pending set before it is released.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("ping called %d times for %d concurrent callers, want 1", got, n)
	}
	for i, ok := range results {
		if !ok {
			t.Errorf("caller %d: got false, want true", i)
		}
	}
}

func TestReadyHandler_NoDatabase(t *testing.T) {
	s := &Server{m: &Manager{}} // m.pg is nil: no PostgreSQL configured
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rec := httptest.NewRecorder()

	s.ready(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ready"] != false {
		t.Fatalf("body = %v, want ready:false", body)
	}
	if len(body) != 1 {
		t.Fatalf("body = %v, want only the ready field (no connection details)", body)
	}
}
