package server

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// readyTTL is how long a PostgreSQL ping result is reused before /api/ready
// pings again. readyPingTimeout bounds each ping itself.
const (
	readyTTL         = time.Second
	readyPingTimeout = time.Second
)

// readyCache holds the last /api/ready PostgreSQL ping result. A request
// within readyTTL of the last ping reuses it; a request that lands while a
// ping is in flight waits for that ping instead of starting its own, so a
// burst of probes cannot queue up one ping per request.
type readyCache struct {
	mu      sync.Mutex
	at      time.Time
	ok      bool
	pending chan struct{} // non-nil while a ping is in flight; closed when it completes
}

// check returns whether PostgreSQL is currently reachable, from the cache
// when fresh, otherwise by pinging (or waiting for an in-flight ping).
func (c *readyCache) check(ping func(context.Context) bool) bool {
	c.mu.Lock()
	if !c.at.IsZero() && time.Since(c.at) < readyTTL {
		ok := c.ok
		c.mu.Unlock()
		return ok
	}
	if c.pending != nil {
		wait := c.pending
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
		ok := c.ok
		c.mu.Unlock()
		return ok
	}
	done := make(chan struct{})
	c.pending = done
	c.mu.Unlock()

	// The ping outlasts any single caller's request, so it gets its own
	// timeout rather than a caller's (possibly already-canceled) context.
	ctx, cancel := context.WithTimeout(context.Background(), readyPingTimeout)
	ok := ping(ctx)
	cancel()

	c.mu.Lock()
	c.ok, c.at, c.pending = ok, time.Now(), nil
	c.mu.Unlock()
	close(done)
	return ok
}

// ready answers GET /api/ready for deployment/monitoring readiness probes. It
// makes no LLM calls, scans, database writes or migrations, and never exposes
// connection details or raw errors. Unlike /api/health, it checks PostgreSQL:
// HTTP 200 {"ready":true} when reachable, HTTP 503 {"ready":false} otherwise.
// It is exempt from auth (see requireAuth), matching /api/health.
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ok := s.readyPG.check(func(ctx context.Context) bool {
		pg := s.m.pg
		if pg == nil {
			return false
		}
		return pg.PingContext(ctx) == nil
	})
	status := http.StatusServiceUnavailable
	if ok {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"ready": ok})
}
