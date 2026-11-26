package traffic

import (
	"log/slog"
	"sync"
	"time"
)

// ftsPendingSchema holds index text that is committed with its exchange but not
// yet in ex_fts. A crash or kill loses nothing: Open queues what is left.
const ftsPendingSchema = `CREATE TABLE IF NOT EXISTS ex_fts_pending (
  rowid   INTEGER PRIMARY KEY,
  id      TEXT NOT NULL,
  content TEXT NOT NULL
);`

// ftsWaitLimit bounds how long a search waits for the indexer, e.g. while a
// backlog left by a crash is being indexed after start. Past it the search runs
// on what is indexed so far.
const ftsWaitLimit = 30 * time.Second

type ftsJob struct {
	rowid int64
	id    string
}

// ftsQueue writes the full-text index on one background goroutine. Trigram
// indexing is almost all of the cost of recording a large text body, and the
// proxy records before it replies, so inline indexing held the client for it.
// The text waits in ex_fts_pending, so jobs are only (rowid, id). ftsFilter
// calls wait, so a search still sees every exchange recorded before it.
type ftsQueue struct {
	t      *Traffic
	mu     sync.Mutex
	cond   *sync.Cond
	jobs   []ftsJob
	queued int64
	done   int64
	closed bool
	exited chan struct{}
}

func newFTSQueue(t *Traffic) *ftsQueue {
	q := &ftsQueue{t: t, exited: make(chan struct{})}
	q.cond = sync.NewCond(&q.mu)
	go q.run()
	return q
}

// resume queues the entries a previous process committed but never indexed.
func (q *ftsQueue) resume() {
	rows, err := q.t.db.Query(`SELECT rowid, id FROM ex_fts_pending ORDER BY rowid`)
	if err != nil {
		slog.Error("[traffic] failed to read pending full-text entries", "err", err)
		return
	}
	var jobs []ftsJob
	for rows.Next() {
		var j ftsJob
		if err := rows.Scan(&j.rowid, &j.id); err == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	if len(jobs) > 0 {
		slog.Info("[traffic] indexing exchanges left unindexed by the last run", "count", len(jobs))
	}
	for _, j := range jobs {
		q.add(j)
	}
}

func (q *ftsQueue) add(j ftsJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return // still in ex_fts_pending; the next Open indexes it
	}
	q.jobs = append(q.jobs, j)
	q.queued++
	q.cond.Broadcast()
}

// wait returns once every job queued before the call has been indexed, the
// queue is closed, or ftsWaitLimit has passed.
func (q *ftsQueue) wait() {
	q.mu.Lock()
	defer q.mu.Unlock()
	target := q.queued
	if q.done >= target {
		return
	}
	expired := false
	timer := time.AfterFunc(ftsWaitLimit, func() {
		q.mu.Lock()
		expired = true
		q.cond.Broadcast()
		q.mu.Unlock()
	})
	defer timer.Stop()
	for q.done < target && !q.closed && !expired {
		q.cond.Wait()
	}
	if expired {
		slog.Warn("[traffic] search ran before the full-text index caught up", "pending", target-q.done)
	}
}

// close stops the worker after the entry it is writing. Queued entries stay in
// ex_fts_pending, so shutdown time does not grow with the backlog.
func (q *ftsQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
	<-q.exited
}

func (q *ftsQueue) run() {
	defer close(q.exited)
	for {
		q.mu.Lock()
		for len(q.jobs) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		j := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()

		q.index(j)

		q.mu.Lock()
		q.done++
		q.cond.Broadcast()
		q.mu.Unlock()
	}
}

// index moves one entry from ex_fts_pending into ex_fts. The join skips an
// exchange deleted after it was queued, or whose rowid now belongs to another
// exchange.
func (q *ftsQueue) index(j ftsJob) {
	q.t.wmu.Lock()
	defer q.t.wmu.Unlock()
	tx, err := q.t.db.Begin()
	if err != nil {
		slog.Error("[traffic] failed to write full-text index", "exchange", j.id, "err", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`INSERT INTO ex_fts(rowid,content)
SELECT p.rowid, p.content FROM ex_fts_pending p JOIN exchanges e ON e.rowid=p.rowid AND e.id=p.id
WHERE p.rowid=? AND p.id=?`, j.rowid, j.id); err != nil {
		slog.Error("[traffic] failed to write full-text index", "exchange", j.id, "err", err)
		return
	}
	if _, err := tx.Exec(`DELETE FROM ex_fts_pending WHERE rowid=? AND id=?`, j.rowid, j.id); err != nil {
		slog.Error("[traffic] failed to clear pending full-text entry", "exchange", j.id, "err", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("[traffic] failed to write full-text index", "exchange", j.id, "err", err)
	}
}
