package agent

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

// scriptedDecomposerProvider replays a sequence of scripted stream turns and
// records every CompletionRequest it receives.
type scriptedDecomposerProvider struct {
	mu    sync.Mutex
	turns [][]llm.StreamEvent
	reqs  []llm.CompletionRequest
}

func (p *scriptedDecomposerProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	idx := len(p.reqs)
	p.reqs = append(p.reqs, req)
	var evs []llm.StreamEvent
	if idx < len(p.turns) {
		evs = p.turns[idx]
	} else if len(p.turns) > 0 {
		evs = p.turns[len(p.turns)-1]
	}
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, ev := range evs {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (p *scriptedDecomposerProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for ev, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(ev)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func (p *scriptedDecomposerProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

func plainTextTurn(text string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.SETextDelta, Text: text},
		{Type: llm.SEMessageDelta, StopReason: "end_turn"},
	}
}

func toolCallTurn(id, name, inputJSON string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.SEToolUseStart, ToolID: id, ToolName: name},
		{Type: llm.SEToolInputJSON, Text: inputJSON},
		{Type: llm.SEMessageDelta, StopReason: "tool_use"},
	}
}

func TestDecomposeGoalsPlainTextToolCallFailsAfterSingleRetry(t *testing.T) {
	ts := guardTaskStore(t)
	prov := &scriptedDecomposerProvider{
		turns: [][]llm.StreamEvent{
			plainTextTurn(`set_goals({"goals":[{"text":"Audit auth on lab.local"}]})`),
			plainTextTurn(`set_goals({"goals":[{"text":"Audit auth on lab.local"}]})`),
			plainTextTurn(`should never be reached`),
		},
	}

	got, err := DecomposeGoals(t.Context(), prov, t.TempDir(), "Audit auth on lab.local", "planning only", nil, ts, 0, nil)
	if !errors.Is(err, ErrGoalDecompositionContract) {
		t.Fatalf("expected ErrGoalDecompositionContract, got goals=%+v err=%v", got, err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no returned goals, got %+v", got)
	}
	if prov.calls() != 2 {
		t.Fatalf("expected exactly 2 LLM calls (initial + 1 retry), got %d", prov.calls())
	}
	// Verify the second call received the stricter retry prompt.
	second := prov.reqs[1]
	foundRetry := false
	for _, msg := range second.Messages {
		for _, blk := range msg.Content {
			if strings.Contains(blk.Text, "structured set_goals tool") {
				foundRetry = true
			}
		}
	}
	if !foundRetry {
		t.Fatalf("second request did not include the stricter retry instruction: %+v", second.Messages)
	}
	nodes, lerr := ts.ListByKind(db.KindGoal, 100)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected 0 materialized goal nodes in DB, got %d", len(nodes))
	}
}

func TestDecomposeGoalsSingleRetrySucceeds(t *testing.T) {
	ts := guardTaskStore(t)
	prov := &scriptedDecomposerProvider{
		turns: [][]llm.StreamEvent{
			plainTextTurn(`set_goals({"goals":[{"text":"Identify SQLi on /login"}]})`),
			toolCallTurn("tc_1", "set_goals", `{"goals":[{"text":"Identify SQLi on /login","vulnclass":"SQLi"}]}`),
			plainTextTurn("Goals submitted."),
		},
	}

	got, err := DecomposeGoals(t.Context(), prov, t.TempDir(), "Test /login for SQLi", "scope: lab.local", nil, ts, 0, nil)
	if err != nil {
		t.Fatalf("expected single retry to succeed, got err=%v", err)
	}
	if len(got) != 1 || got[0].Text != "Identify SQLi on /login" || got[0].VulnClass != "SQLi" {
		t.Fatalf("unexpected goals: %+v", got)
	}
	// Turn 1: plain text; Turn 2: retry tool call; Turn 3: post-tool completion.
	if prov.calls() != 3 {
		t.Fatalf("expected 3 provider turns (plain text, retry tool_use, final), got %d", prov.calls())
	}
	nodes, lerr := ts.ListByKind(db.KindGoal, 100)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 persisted goal node, got %d", len(nodes))
	}
}

func TestDecomposeGoalsValidStructuredToolCallAccepted(t *testing.T) {
	ts := guardTaskStore(t)
	prov := &scriptedDecomposerProvider{
		turns: [][]llm.StreamEvent{
			toolCallTurn("tc_1", "set_goals", `{"goals":[{"text":"Confirm auth bypass on admin panel","vulnclass":"AuthBypass"},{"text":"Verify IDOR on /api/users","vulnclass":"IDOR"}]}`),
			plainTextTurn("Done."),
		},
	}

	got, err := DecomposeGoals(t.Context(), prov, t.TempDir(), "Audit admin panel and /api/users", "", nil, ts, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 goals, got %+v", got)
	}
	// Only 2 turns (tool_use + final end_turn) — no retry was needed.
	if prov.calls() != 2 {
		t.Fatalf("expected 2 turns with no retry, got %d", prov.calls())
	}
}

func TestDecomposeGoalsRejectsEmptyTaskGoalAndEmptyToolArgs(t *testing.T) {
	ts := guardTaskStore(t)
	prov := &scriptedDecomposerProvider{
		turns: [][]llm.StreamEvent{
			toolCallTurn("tc_1", "set_goals", `{"goals":[{"text":"   "}]}`),
			plainTextTurn("ok"),
			toolCallTurn("tc_2", "set_goals", `{"goals":[{"text":""}]}`),
			plainTextTurn("ok"),
		},
	}

	if got, err := DecomposeGoals(t.Context(), prov, t.TempDir(), "   ", "desc", nil, ts, 0, nil); !errors.Is(err, ErrGoalDecompositionEmptyInput) || len(got) != 0 {
		t.Fatalf("empty goalText: got goals=%+v err=%v", got, err)
	}
	if prov.calls() != 0 {
		t.Fatalf("empty goalText must not call LLM, got %d calls", prov.calls())
	}

	if got, err := DecomposeGoals(t.Context(), prov, t.TempDir(), "Real task goal", "", nil, ts, 0, nil); !errors.Is(err, ErrGoalDecompositionContract) || len(got) != 0 {
		t.Fatalf("blank tool args: expected ErrGoalDecompositionContract, got goals=%+v err=%v", got, err)
	}
	nodes, err := ts.ListByKind(db.KindGoal, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("blank tool args must not materialize empty goal nodes, got %d", len(nodes))
	}
}

func TestPlannerRefusesEmptyContextAndOverviewOmitsEmptyFields(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("", "", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := d.Exploration(task.ExplorationID)

	// Even if a blank goal node exists in the graph, overview must drop it and
	// omit empty task description/goal, and Planner.Plan must refuse to plan.
	if _, err := ts.AddNode(db.KindGoal, map[string]any{"text": "   "}, 0, "open", "system", nil); err != nil {
		t.Fatal(err)
	}

	tsx := NewToolSet(ts, "planner")
	overview := tsx.graphOverviewData()
	if goals, _ := overview["goals"].([]map[string]any); len(goals) != 0 {
		t.Fatalf("graphOverviewData materialized empty goals: %+v", goals)
	}
	if _, hasTask := overview["task"]; hasTask {
		t.Fatalf("graphOverviewData materialized empty task block: %+v", overview["task"])
	}

	prov := &scriptedDecomposerProvider{
		turns: [][]llm.StreamEvent{plainTextTurn("should not run")},
	}
	planner := NewPlanner(prov, "test-model", t.TempDir(), nil, 0, 2)

	var logBuf bytes.Buffer
	prevOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	if _, _, err := planner.Plan(t.Context(), task.ID, nil, nil, "", nil, nil, nil); !errors.Is(err, ErrPlannerEmptyContext) {
		t.Fatalf("nil store: expected ErrPlannerEmptyContext, got %v", err)
	}
	if _, _, err := planner.Plan(t.Context(), task.ID, nil, ts, "   ", nil, nil, nil); !errors.Is(err, ErrPlannerEmptyContext) {
		t.Fatalf("empty context: expected ErrPlannerEmptyContext, got %v", err)
	}
	if prov.calls() != 0 {
		t.Fatalf("planner must not call LLM on empty context, got %d calls", prov.calls())
	}
	if !strings.Contains(logBuf.String(), "refusing to plan on empty context") {
		t.Fatalf("expected clear refusal log, got: %q", logBuf.String())
	}

	// Verify graphOverviewData includes non-empty goal and task fields normally.
	if _, err := ts.AddNode(db.KindGoal, map[string]any{"text": "Audit /admin"}, 0, "open", "goals", nil); err != nil {
		t.Fatal(err)
	}
	overview = tsx.graphOverviewData()
	goals, _ := overview["goals"].([]map[string]any)
	if len(goals) != 1 || goals[0]["text"] != "Audit /admin" {
		t.Fatalf("expected 1 non-empty goal in overview, got %+v", overview["goals"])
	}
	// With a valid goal node present, Planner.Plan proceeds even if the explicit
	// goal arg was empty.
	if _, _, err := planner.Plan(t.Context(), task.ID, nil, ts, "", nil, nil, nil); err != nil {
		t.Fatalf("planner with valid goal node should proceed, got err=%v", err)
	}
	if prov.calls() != 1 {
		t.Fatalf("expected 1 LLM call when context is valid, got %d", prov.calls())
	}
}
func TestDecomposeGoalsNoProviderReturnsSentinel(t *testing.T) {
	ts := guardTaskStore(t)
	got, err := DecomposeGoals(t.Context(), nil, t.TempDir(), "Audit target", "desc", nil, ts, 0, nil)
	if !errors.Is(err, ErrGoalDecompositionNoProvider) {
		t.Fatalf("expected ErrGoalDecompositionNoProvider, got goals=%+v err=%v", got, err)
	}
	if errors.Is(err, ErrGoalDecompositionContract) {
		t.Fatalf("no-provider error must not match ErrGoalDecompositionContract: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no goals returned, got %+v", got)
	}
}
