package server

import (
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmpool"
)

func TestHealthSyncCoalescesToLatest(t *testing.T) {
	h := &healthSync{pending: map[int64]healthOp{}}
	h.enqueue(7, healthOp{st: llmpool.State{Fails: 1}})
	h.enqueue(7, healthOp{st: llmpool.State{Fails: 4}})
	h.enqueue(7, healthOp{clear: true})
	h.enqueue(8, healthOp{st: llmpool.State{Fails: 2}})
	batch := h.take()
	if len(batch) != 2 || !batch[7].clear || batch[8].st.Fails != 2 {
		t.Fatalf("batch=%+v", batch)
	}
	if again := h.take(); again != nil {
		t.Fatalf("pending was not cleared: %+v", again)
	}
	var saved []db.LLMHealth
	var cleared []int64
	worker := &healthSync{
		save: func(row db.LLMHealth) error {
			saved = append(saved, row)
			return nil
		},
		clear: func(id int64) error {
			cleared = append(cleared, id)
			return nil
		},
	}
	worker.enqueue(7, healthOp{clear: true})
	worker.enqueue(8, healthOp{st: llmpool.State{Fails: 3, LastError: "down"}})
	for id, op := range worker.take() {
		if op.clear {
			if err := worker.clear(id); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := worker.save(db.LLMHealth{ProfileID: id, Fails: op.st.Fails, LastError: op.st.LastError}); err != nil {
			t.Fatal(err)
		}
	}
	if len(cleared) != 1 || cleared[0] != 7 || len(saved) != 1 || saved[0].ProfileID != 8 || saved[0].Fails != 3 {
		t.Fatalf("cleared=%v saved=%+v", cleared, saved)
	}
}
