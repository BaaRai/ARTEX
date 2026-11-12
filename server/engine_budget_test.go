package server

import (
	"context"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
)

// With the budget env vars unset the engine decides exactly as before (no round
// stopped, no claim blocked, contexts untouched, every tool call let through);
// a task budget flips the same decisions once the task cannot afford a worker.
func TestEngineBudgetPassthroughAndGate(t *testing.T) {
	for _, k := range []string{"ARTEX_TASK_BUDGET_TOKENS", "ARTEX_TASK_BUDGET_TURNS", "ARTEX_INTENT_BUDGET_TOKENS", "ARTEX_INTENT_BUDGET_TURNS"} {
		t.Setenv(k, "")
	}
	ctx, task := context.Background(), &Task{ID: "7"}
	noSteer := func() (string, bool) { return "", false }
	e := NewEngine(nil)
	wctx, meter := e.budgetIntent(ctx, task.ID)
	if e.budgetStop(ctx, task) || e.budgetBlocksClaim(task.ID) || e.budgetRoundCtx(ctx, task, false) != ctx || wctx != ctx || meter != nil {
		t.Fatal("no budget: a decision or a context changed")
	}
	if block, _, _ := (steerHooks{drain: noSteer}).PreToolUse(ctx, "Bash", nil); block {
		t.Fatal("no budget: a tool call was refused")
	}

	t.Setenv("ARTEX_TASK_BUDGET_TOKENS", "100000")
	e = NewEngine(nil)
	_, meter = e.budgetIntent(ctx, task.ID)
	meter.Record(llm.Usage{InputTokens: 60_000}) // remaining 40k < default worker estimate 50k
	h := steerHooks{drain: noSteer, budget: meter}
	if block, _, _ := h.PreToolUse(ctx, "Bash", nil); !e.budgetBlocksClaim(task.ID) || block {
		t.Fatal("budget: claim must be blocked, the running worker (above the reserve) not")
	}
	meter.Record(llm.Usage{OutputTokens: 20_000}) // reaches the 20k reserve
	if block, msg, _ := h.PreToolUse(ctx, "Bash", nil); !block || msg != agent.WorkerBudgetWrapup {
		t.Fatalf("budget: exhausted run not sent to wrap-up (block=%v msg=%q)", block, msg)
	}
	if block, _, _ := h.PreToolUse(ctx, "record_fact", nil); block {
		t.Fatal("budget: write-back tool refused during the wrap-up")
	}
}
