package agent

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/llm"
)

func TestTaskBriefAndCompetingMutatingIntentRejection(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("task-brief-test", "Exploit https://edge.lab.local/api", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	store := d.Exploration(task.ExplorationID)
	as := d.Assets()

	if _, err := store.AddGoal(map[string]any{"text": "Capture flag 2 on edge.lab.local"}, "operator"); err != nil {
		t.Fatal(err)
	}
	runningID, err := store.AddIntent(map[string]any{"summary": "Upload enrolment cert to https://edge.lab.local/api/v1/certificates/upload"}, 8, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimIntentToken(runningID, "work#1"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 7; i++ {
		if _, err := store.AddNode(db.KindFact, map[string]any{"summary": "Fact sample #" + itoa(int64(i))}, 0, "confirmed", "work#1", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AddNode(db.KindFinding, map[string]any{"summary": "Confirmed IDOR on supersedes"}, 0, "confirmed", "work#1", nil); err != nil {
		t.Fatal(err)
	}

	sg := intercept.NewStatefulTargetGuard()
	now := time.Now().UTC()
	sg.SetClock(func() time.Time { return now })
	taskStr := itoa(task.ID)
	if ok, _ := sg.AcquireMutationLease(taskStr, "edge.lab.local", runningID); !ok {
		t.Fatal("failed to acquire initial lease")
	}

	// 1. task_brief tool & prompt section render compact state (goals, intents+holder, leases, last 5 facts/findings).
	workerTS := NewToolSet(store, "work#2")
	workerTS.SetTaskID(task.ID)
	workerTS.SetAssetStore(as, as.Companies())
	workerTS.SetStatefulGuard(sg)

	brief := workerTS.taskBriefData(t.Context())
	if len(brief.ActiveGoals) != 1 || len(brief.OpenAndRunningIntents) != 1 || len(brief.LeaseStatus) != 1 {
		t.Fatalf("unexpected brief top fields: %+v", brief)
	}
	if brief.OpenAndRunningIntents[0]["holder"] != "work#1" {
		t.Fatalf("expected intent holder work#1 in brief, got %+v", brief.OpenAndRunningIntents[0])
	}
	if len(brief.LastFacts) != 5 || len(brief.LastFindings) != 1 {
		t.Fatalf("expected capped 5 last_facts and 1 last_findings, got facts=%d findings=%d", len(brief.LastFacts), len(brief.LastFindings))
	}

	rendered := renderWorkerTaskBrief(brief, newDataEnvelope())
	for _, want := range []string{"[task_brief", "Capture flag 2", "work#1", "edge.lab.local", "Confirmed IDOR on supersedes"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered task_brief missing %q: %s", want, rendered)
		}
	}

	res, err := workerTS.taskBriefTool().Call(t.Context(), json.RawMessage(`{}`), nil)
	if err != nil || res.IsError || !strings.Contains(res.Flatten(), "edge.lab.local") {
		t.Fatalf("task_brief tool failed: err=%v res=%s", err, res.Flatten())
	}

	// Verify task_brief is in WorkerTools, BuiltinToolSeeds, and noTargetTools.
	if !toolNameSet(workerTS.WorkerTools())["task_brief"] {
		t.Fatalf("WorkerTools missing task_brief")
	}
	var seeded bool
	for _, sd := range BuiltinToolSeeds() {
		if sd.Key == "task_brief" && slices.Contains(sd.Agents, "worker") {
			seeded = true
		}
	}
	if !seeded {
		t.Fatalf("BuiltinToolSeeds missing task_brief for worker")
	}

	// 2. Planner rejects competing mutating intent on host with active lease.
	plannerTS := NewToolSet(store, "planner")
	plannerTS.SetTaskID(task.ID)
	plannerTS.SetAssetStore(as, as.Companies())
	plannerTS.SetStatefulGuard(sg)

	competing := json.RawMessage(`{"summary":"POST new certificate payload to https://edge.lab.local/api/v1/deploy"}`)
	res, err = plannerTS.addIntent().Call(t.Context(), competing, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "active mutation lease") {
		t.Fatalf("expected competing mutating intent rejected, got err=%v res=%s", err, res.Flatten())
	}

	// Read-only intent on the same host is allowed.
	roIntent := json.RawMessage(`{"summary":"GET /docs and inspect OpenAPI spec on https://edge.lab.local/docs"}`)
	res, err = plannerTS.addIntent().Call(t.Context(), roIntent, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected read-only intent allowed while lease held, got err=%v res=%s", err, res.Flatten())
	}

	// Prose mention of wait_for_lease without structured wait_for_lease=true is STILL rejected.
	proseOnly := json.RawMessage(`{"summary":"Use wait_for_lease on https://edge.lab.local/ then POST deployment test"}`)
	res, err = plannerTS.addIntent().Call(t.Context(), proseOnly, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "active mutation lease") {
		t.Fatalf("expected prose-only wait_for_lease mention to be rejected without structured field, got err=%v res=%s", err, res.Flatten())
	}

	// Mutating intent with structured wait_for_lease: true is allowed.
	waitIntent := json.RawMessage(`{"summary":"POST deployment test on https://edge.lab.local/","wait_for_lease":true}`)
	res, err = plannerTS.addIntent().Call(t.Context(), waitIntent, nil)
	if err != nil || res.IsError {
		t.Fatalf("expected structured wait_for_lease=true mutating intent allowed, got err=%v res=%s", err, res.Flatten())
	}

	// Seed + planner overlap: lease acquired on same task blocks a competing planner mutating intent even if held by a seed intent.
	seedID, err := store.AddIntent(map[string]any{"summary": "Seed stateful probe on https://seed.lab.local/"}, 9, nil, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := sg.AcquireMutationLease(taskStr, "seed.lab.local", seedID); !ok {
		t.Fatal("failed to acquire seed lease")
	}
	seedOverlap := json.RawMessage(`{"summary":"PUT config update on https://seed.lab.local/api/config"}`)
	res, err = plannerTS.addIntent().Call(t.Context(), seedOverlap, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "active mutation lease") {
		t.Fatalf("expected seed-held lease to block competing planner mutating intent, got err=%v res=%s", err, res.Flatten())
	}

	// 3. End-to-end Worker.Execute includes [task_brief] in the worker launch prompt.
	intentNode, err := store.GetNode(runningID)
	if err != nil || intentNode == nil {
		t.Fatal(err)
	}
	capProv := &captureLaunchPromptProvider{}
	wk := NewWorker(capProv, "test-model", t.TempDir(), nil, 0, 1)
	wCtx := intercept.WithStatefulActor(intercept.WithStatefulGuard(t.Context(), sg), taskStr, runningID)
	if _, _, err := wk.Execute(wCtx, "work#2", task.ID, as, store, intentNode, nil, nil, nil, nil); err != nil {
		t.Fatalf("Worker.Execute failed: %v", err)
	}
	userMsg := capProv.firstUserText()
	for _, want := range []string{"[task_brief", "Capture flag 2", "edge.lab.local", "Confirmed IDOR on supersedes"} {
		if !strings.Contains(userMsg, want) {
			t.Fatalf("worker launch prompt missing %q: %s", want, userMsg)
		}
	}
}

type captureLaunchPromptProvider struct {
	mu       sync.Mutex
	userText string
}

func (p *captureLaunchPromptProvider) record(req llm.CompletionRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.userText != "" {
		return
	}
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == llm.RoleUser {
			for _, cb := range m.Content {
				b.WriteString(cb.Text)
			}
			b.WriteByte('\n')
		}
	}
	p.userText = b.String()
}

func (p *captureLaunchPromptProvider) firstUserText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.userText
}

func (p *captureLaunchPromptProvider) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.record(req)
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("done")}}, "end_turn", llm.Usage{}, nil
}

func (p *captureLaunchPromptProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.record(req)
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "done"}, nil)
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
	}
}
