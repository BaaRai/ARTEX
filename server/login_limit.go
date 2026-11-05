package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Login attempts are limited per client IP, never per account: a per-account
// lockout would let anyone lock the admin out by failing on purpose.
const (
	loginMaxAttempts = 10               // attempts allowed per IP per window
	loginWindow      = time.Minute      // the counting window
	loginBaseBlock   = time.Minute      // length of the first block
	loginMaxBlock    = 15 * time.Minute // cap on the block; an entry idle this long is dropped
	loginSweepEvery  = time.Minute      // how often stale entries are swept
	loginMaxEntries  = 10000            // hard cap on tracked IPs
)

type loginEntry struct {
	attempts     int
	windowStart  time.Time
	blockedUntil time.Time
	block        time.Duration // current block length; doubles on every new block or blocked attempt
	lastSeen     time.Time
}

// loginLimiter counts login attempts per IP. Past loginMaxAttempts in one
// loginWindow the IP is blocked for loginBaseBlock; every attempt while blocked,
// and every later block, doubles the block up to loginMaxBlock. State is in
// memory only and swept of idle entries, so a restart clears it.
type loginLimiter struct {
	mu        sync.Mutex
	now       func() time.Time
	entries   map[string]*loginEntry
	lastSweep time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{now: now, entries: map[string]*loginEntry{}}
}

// allow records one attempt from ip. It returns 0 when the attempt may go on,
// otherwise how long ip must wait.
func (l *loginLimiter) allow(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	e := l.entries[ip]
	if e == nil {
		if len(l.entries) >= loginMaxEntries {
			l.evictOldest()
		}
		e = &loginEntry{windowStart: now}
		l.entries[ip] = e
	}
	e.lastSeen = now
	if now.Before(e.blockedUntil) {
		e.block = min(2*e.block, loginMaxBlock)
		e.blockedUntil = now.Add(e.block)
		return e.block
	}
	if now.Sub(e.windowStart) >= loginWindow {
		e.attempts, e.windowStart = 0, now
	}
	if e.attempts >= loginMaxAttempts {
		if e.block == 0 {
			e.block = loginBaseBlock
		} else {
			e.block = min(2*e.block, loginMaxBlock)
		}
		e.blockedUntil = now.Add(e.block)
		e.attempts, e.windowStart = 0, now
		return e.block
	}
	e.attempts++
	return 0
}

// reset forgets ip, after a successful login. A blocked IP never reaches the
// password check, so this only runs outside a block.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

// sweep drops entries idle for loginMaxBlock, at most once per loginSweepEvery.
// Such an entry is never blocked: a block ends at most loginMaxBlock after the
// attempt that set it.
func (l *loginLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < loginSweepEvery {
		return
	}
	l.lastSweep = now
	for ip, e := range l.entries {
		if now.Sub(e.lastSeen) >= loginMaxBlock {
			delete(l.entries, ip)
		}
	}
}

// evictOldest drops the least recently seen entry to stay under loginMaxEntries.
func (l *loginLimiter) evictOldest() {
	var oldest string
	var at time.Time
	for ip, e := range l.entries {
		if oldest == "" || e.lastSeen.Before(at) {
			oldest, at = ip, e.lastSeen
		}
	}
	delete(l.entries, oldest)
}

// peerIP is the host part of the TCP peer address. X-Forwarded-For and similar
// headers are ignored on purpose: the client sets them, so trusting them would
// give an attacker a fresh bucket per request.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
