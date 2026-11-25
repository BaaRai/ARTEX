package traffic

import (
	"log/slog"
	"sync"
)

// ftsQueueMax bounds the bodies waiting for the indexer. A recorder that finds
// the queue full waits, so memory stays bounded under a burst of large bodies.
const ftsQueueMax = 16

type ftsJob struct {
	rowid int64
	id    string
	text  string
}

// ftsQueue writes the full-text index on one background goroutine. Trigram
// indexing is almost all of the cost of recording a large text body, and the
// proxy records before it replies, so inline indexing held the client and every
// other recorder (on wmu) for it. ftsFilter calls wait, so a search still sees
// every exchange recorded before it.
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

// add queues a job. It must not be called with wmu held: the worker needs wmu
// to drain a full queue.
func (q *ftsQueue) add(j ftsJob) {
	q.mu.Lock()
	for len(q.jobs) >= ftsQueueMax && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		q.mu.Unlock()
		q.index(j)
		return
	}
	q.jobs = append(q.jobs, j)
	q.queued++
	q.cond.Broadcast()
	q.mu.Unlock()
}

// wait returns once every job queued before the call has been indexed.
func (q *ftsQueue) wait() {
	q.mu.Lock()
	for target := q.queued; q.done < target; {
		q.cond.Wait()
	}
	q.mu.Unlock()
}

// close indexes what is still queued and stops the worker.
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
		if len(q.jobs) == 0 {
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

// index writes one entry, skipping it when the exchange was deleted (or its
// rowid reused by another exchange) after it was queued.
func (q *ftsQueue) index(j ftsJob) {
	q.t.wmu.Lock()
	defer q.t.wmu.Unlock()
	if _, err := q.t.db.Exec(`INSERT INTO ex_fts(rowid,content)
SELECT ?, ? WHERE EXISTS(SELECT 1 FROM exchanges WHERE rowid=? AND id=?)`, j.rowid, j.text, j.rowid, j.id); err != nil {
		slog.Error("[traffic] failed to write full-text index", "exchange", j.id, "err", err)
	}
}
