package agent

import (
	"context"
	"fmt"
	"time"
)

// TaskClock carries a task's absolute deadline and total timeout budget into a
// worker/planner run so the run can clamp its wall-clock budget, pick the right
// wrap-up words, and surface remaining_seconds to the planner.
type TaskClock struct {
	DeadlineUnix  int64 // absolute deadline (unix seconds); 0 = no task timeout
	BudgetSeconds int   // total configured task timeout_seconds; 0 = unbounded
	Final         bool  // coordinator-driven FINAL planner round (task ending now)
}

type taskClockKey struct{}
type seedBudgetCapKey struct{}
type seedWriteNotifyKey struct{}

// WithTaskClock attaches a TaskClock to ctx for the run.
func WithTaskClock(ctx context.Context, tc TaskClock) context.Context {
	return context.WithValue(ctx, taskClockKey{}, tc)
}

// taskClockFrom reads the TaskClock (zero value if none attached).
func taskClockFrom(ctx context.Context) TaskClock {
	if v, ok := ctx.Value(taskClockKey{}).(TaskClock); ok {
		return v
	}
	return TaskClock{}
}

// WithSeedBudgetCap attaches a wall-clock cap for an initial seed intent
// (typically 20% of the task or worker budget so the seed never consumes the
// entire task before decomposition).
func WithSeedBudgetCap(ctx context.Context, capDur time.Duration) context.Context {
	if capDur <= 0 {
		return ctx
	}
	return context.WithValue(ctx, seedBudgetCapKey{}, capDur)
}

func seedBudgetCapFrom(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(seedBudgetCapKey{}).(time.Duration); ok {
		return d
	}
	return 0
}

// WithSeedWriteNotify attaches a callback invoked once when the seed worker
// writes its first fact/asset/finding, waking the planner immediately after
// the first seed result rather than waiting for the seed intent to terminate.
func WithSeedWriteNotify(ctx context.Context, fn func()) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, seedWriteNotifyKey{}, fn)
}

func seedWriteNotifyFrom(ctx context.Context) func() {
	if fn, ok := ctx.Value(seedWriteNotifyKey{}).(func()); ok {
		return fn
	}
	return nil
}

// DefaultSeedIntentBudget caps a seed intent when neither task timeout nor
// worker runTimeout is configured, avoiding open-ended seed loops.
const DefaultSeedIntentBudget = 180 * time.Second

// SeedIntentBudget computes the capped duration (20% of the task budget, or
// 20% of workerRunTimeout when the task has no overall timeout) for a seed intent.
// When both taskTimeoutSeconds and workerRunTimeout are 0, it falls back to
// DefaultSeedIntentBudget.
func SeedIntentBudget(taskTimeoutSeconds int, workerRunTimeout time.Duration) time.Duration {
	if taskTimeoutSeconds > 0 {
		capDur := time.Duration(taskTimeoutSeconds) * time.Second / 5
		if capDur < time.Second {
			capDur = time.Second
		}
		if workerRunTimeout > 0 && workerRunTimeout < capDur {
			return workerRunTimeout
		}
		return capDur
	}
	if workerRunTimeout > 0 {
		capDur := workerRunTimeout / 5
		if capDur < time.Second {
			capDur = time.Second
		}
		return capDur
	}
	return DefaultSeedIntentBudget
}

// clampMaxDuration folds a task deadline into a run's own wall-clock budget.
//   - ownBudget = the agent's own run_seconds (0 = unlimited).
//   - Returns eff = the MaxDuration to use (floored at 1s so we never pass ≤0, which
//     harness reads as "unlimited"), and clamped = whether the TASK deadline is the
//     binding constraint (remaining ≤ ownBudget, or ownBudget unlimited). When there
//     is no deadline, returns (ownBudget, false) unchanged.
func clampMaxDuration(deadlineUnix int64, ownBudget time.Duration) (eff time.Duration, clamped bool) {
	if deadlineUnix <= 0 {
		return ownBudget, false
	}
	remaining := time.Until(time.Unix(deadlineUnix, 0))
	if remaining < time.Second {
		remaining = time.Second // max(1, …): never pass ≤0 (harness treats 0 as unlimited)
	}
	// clamped when the task deadline binds this run's time: remaining ≤ own budget,
	// or the agent has no own time budget (then remaining always binds).
	clamped = ownBudget <= 0 || remaining <= ownBudget
	eff = remaining
	if ownBudget > 0 && ownBudget < remaining {
		eff = ownBudget
	}
	return eff, clamped
}

func plannerTimeBudgetBlockNow(tc TaskClock) string {
	return plannerTimeBudgetBlock(tc, time.Now())
}

func plannerTimeBudgetBlock(tc TaskClock, now time.Time) string {
	if tc.DeadlineUnix <= 0 {
		return ""
	}
	rem := int64(time.Unix(tc.DeadlineUnix, 0).Sub(now) / time.Second)
	if rem < 0 {
		rem = 0
	}
	if tc.BudgetSeconds > 0 && rem*4 < int64(tc.BudgetSeconds) {
		return fmt.Sprintf("\n\n[Task time budget]: remaining_seconds=%d (budget_seconds=%d; below 25%% of budget — focus mode: dispatch focused exploitation/verification intents only, NOT broad exploration seeds).", rem, tc.BudgetSeconds)
	}
	if tc.BudgetSeconds > 0 {
		return fmt.Sprintf("\n\n[Task time budget]: remaining_seconds=%d (budget_seconds=%d).", rem, tc.BudgetSeconds)
	}
	return fmt.Sprintf("\n\n[Task time budget]: remaining_seconds=%d.", rem)
}
