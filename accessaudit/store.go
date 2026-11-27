// Package accessaudit stores bounded, redacted observations of target access.
// An observation is evidence from its named producer, never a complete ledger
// of host traffic or proof that the origin received a request.
package accessaudit

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const RetentionDays = 30
const MaxEvents = 500000
const MaxURL = 8192

// Identity is supplied by trusted execution context, never request headers.
type Identity struct {
	TaskID         string `json:"task_id,omitempty"`
	ConversationID int64  `json:"conversation_id,omitempty"`
	Agent          string `json:"agent,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	ToolUseID      string `json:"tool_use_id,omitempty"`
}
type Event struct {
	ID         int64     `json:"id"`
	ObservedAt time.Time `json:"observed_at"`
	Identity
	Source    string `json:"source"`
	Outcome   string `json:"outcome"`
	Tool      string `json:"tool,omitempty"`
	Method    string `json:"method,omitempty"`
	URL       string `json:"url"`
	Host      string `json:"host"`
	Status    int    `json:"status,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}
type Store struct {
	db       *sql.DB
	failures atomic.Uint64
	started  time.Time
	mu       sync.RWMutex
	closed   bool
	queue    chan pending
	done     chan struct{}
}

func Open(path string) (*Store, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Create privately before sqlite opens the file (including WAL/SHM siblings).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	d, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(250)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	s := &Store{db: d, started: time.Now().UTC(), queue: make(chan pending, 1024), done: make(chan struct{})}
	_, err = d.Exec(`CREATE TABLE IF NOT EXISTS access_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL,
 task_id TEXT NOT NULL, conversation_id INTEGER NOT NULL, agent TEXT NOT NULL,
 run_id TEXT NOT NULL, tool_use_id TEXT NOT NULL, source TEXT NOT NULL,
 outcome TEXT NOT NULL, tool TEXT NOT NULL, method TEXT NOT NULL,
 url TEXT NOT NULL, host TEXT NOT NULL, status INTEGER NOT NULL, request_id TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS access_ts ON access_events(ts);
 CREATE INDEX IF NOT EXISTS access_host_ts ON access_events(host,ts);
 CREATE INDEX IF NOT EXISTS access_task_ts ON access_events(task_id,ts);`)
	if err == nil {
		err = s.prune(context.Background())
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	go s.writeLoop()
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()
	<-s.done
	return s.db.Close()
}
func (s *Store) Failures() uint64   { return s.failures.Load() }
func (s *Store) Started() time.Time { return s.started }

// RedactURL drops userinfo, ALL query values and keys, and fragments. Paths can
// still contain sensitive data; access remains operator-only. Never store a raw
// URL or raw error as fallback when parsing fails.
func RedactURL(raw string) (string, string, error) {
	if len(raw) > MaxURL {
		return "", "", errors.New("URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", errors.New("invalid HTTP URL")
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), strings.ToLower(u.Hostname()), nil
}
func (s *Store) prune(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM access_events WHERE ts < ? OR id <= (SELECT COALESCE(MAX(id),0)-? FROM access_events)`, time.Now().Add(-RetentionDays*24*time.Hour).UnixMilli(), MaxEvents)
	return err
}

// Record queues a redacted observation without blocking the request on disk I/O.
// The bounded queue reports overflow/commit failures; normal Close drains it.
func (s *Store) Record(e Event) {
	if s == nil {
		return
	}
	safe, host, err := RedactURL(e.URL)
	if err != nil {
		s.failures.Add(1)
		return
	}
	if len(e.Tool) > 256 || len(e.Method) > 64 || len(e.Source) > 64 || len(e.Outcome) > 64 || len(e.TaskID) > 64 || len(e.Agent) > 256 || len(e.RunID) > 256 || len(e.ToolUseID) > 256 || len(e.RequestID) > 256 {
		s.failures.Add(1)
		return
	}
	e.URL, e.Host = safe, host
	e.ObservedAt = time.Now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		s.failures.Add(1)
		return
	}
	select {
	case s.queue <- pending{event: &e}:
	default:
		s.failures.Add(1)
	}
}

type pending struct {
	event   *Event
	barrier chan struct{}
}

// Flush waits for earlier queued events to finish their commit attempt. A failed
// commit increments Failures; it does not turn missing evidence into success.
func (s *Store) Flush(ctx context.Context) error {
	barrier := make(chan struct{})
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("access audit closed")
	}
	select {
	case s.queue <- pending{barrier: barrier}:
		s.mu.RUnlock()
	case <-ctx.Done():
		s.mu.RUnlock()
		return ctx.Err()
	}
	select {
	case <-barrier:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Store) writeLoop() {
	defer close(s.done)
	for first := range s.queue {
		batch := []pending{first}
		// Drain currently queued observations together: one durable commit per batch,
		// never one fsync per network callback. No unbounded goroutine per event.
	drain:
		for len(batch) < 64 {
			select {
			case next, ok := <-s.queue:
				if !ok {
					break drain
				}
				batch = append(batch, next)
			default:
				break drain
			}
		}
		var events []Event
		for _, p := range batch {
			if p.event != nil {
				events = append(events, *p.event)
			}
		}
		if len(events) > 0 {
			if err := s.writeBatch(events); err != nil {
				s.failures.Add(uint64(len(events)))
			}
		}
		for _, p := range batch {
			if p.barrier != nil {
				close(p.barrier)
			}
		}
	}
}
func (s *Store) writeBatch(events []Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		_, err = tx.ExecContext(ctx, `INSERT INTO access_events(ts,task_id,conversation_id,agent,run_id,tool_use_id,source,outcome,tool,method,url,host,status,request_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.ObservedAt.UnixMilli(), e.TaskID, e.ConversationID, e.Agent, e.RunID, e.ToolUseID, e.Source, e.Outcome, e.Tool, e.Method, e.URL, e.Host, e.Status, e.RequestID)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_events WHERE ts < ? OR id <= (SELECT COALESCE(MAX(id),0)-? FROM access_events)`, time.Now().Add(-RetentionDays*24*time.Hour).UnixMilli(), MaxEvents); err != nil {
		return err
	}
	return tx.Commit()
}

type Query struct {
	From, To                  time.Time
	URL, Host, TaskID, Source string
	Before                    int64
	Limit                     int
}
type Page struct {
	Events []Event `json:"events"`
	Next   int64   `json:"next,omitempty"`
}

// Stored observations have millisecond precision. Round query boundaries up
// so [from,to) remains exact even when an RFC3339 timestamp has finer precision.
func boundaryMillis(t time.Time) int64 {
	n := t.UnixMilli()
	if t.Nanosecond()%int(time.Millisecond) != 0 {
		n++
	}
	return n
}

func (s *Store) Search(ctx context.Context, q Query) (Page, error) {
	out := Page{Events: []Event{}}
	if q.From.IsZero() || q.To.IsZero() || !q.From.Before(q.To) || q.To.Sub(q.From) > 31*24*time.Hour || q.Before < 0 || q.Limit < 1 || q.Limit > 200 || len(q.URL) > MaxURL || len(q.Host) > 255 || len(q.TaskID) > 64 || len(q.Source) > 64 {
		return out, errors.New("invalid access audit query")
	}
	// Expired rows may await the next pruning write; never expose them as retained.
	if floor := time.Now().Add(-RetentionDays * 24 * time.Hour); q.From.Before(floor) {
		q.From = floor
	}
	if err := s.Flush(ctx); err != nil {
		return out, err
	}
	where := "ts>=? AND ts<?"
	args := []any{boundaryMillis(q.From), boundaryMillis(q.To)}
	for _, v := range []struct{ key, value string }{{"host", strings.ToLower(q.Host)}, {"task_id", q.TaskID}, {"source", q.Source}} {
		if v.value != "" {
			where += " AND " + v.key + "=?"
			args = append(args, v.value)
		}
	}
	if q.URL != "" {
		where += " AND instr(url,?)>0"
		args = append(args, q.URL)
	}
	if q.Before > 0 {
		where += " AND id<?"
		args = append(args, q.Before)
	}
	args = append(args, q.Limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id,ts,task_id,conversation_id,agent,run_id,tool_use_id,source,outcome,tool,method,url,host,status,request_id FROM access_events WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.TaskID, &e.ConversationID, &e.Agent, &e.RunID, &e.ToolUseID, &e.Source, &e.Outcome, &e.Tool, &e.Method, &e.URL, &e.Host, &e.Status, &e.RequestID); err != nil {
			return out, err
		}
		e.ObservedAt = time.UnixMilli(ts).UTC()
		out.Events = append(out.Events, e)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Events) > q.Limit {
		out.Events = out.Events[:q.Limit]
		out.Next = out.Events[len(out.Events)-1].ID
	}
	return out, nil
}
