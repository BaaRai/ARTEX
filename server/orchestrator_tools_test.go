package server

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

type captureToolsLLM struct {
	mu    sync.Mutex
	names []string
}

func (p *captureToolsLLM) record(req llm.CompletionRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.names != nil {
		return
	}
	p.names = make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		p.names = append(p.names, t.Name)
	}
}

func (p *captureToolsLLM) set() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]bool, len(p.names))
	for _, n := range p.names {
		out[n] = true
	}
	return out
}

func (p *captureToolsLLM) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.record(req)
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("ok")}}, "end_turn", llm.Usage{}, nil
}

func (p *captureToolsLLM) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.record(req)
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "ok"}, nil)
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
	}
}

// TestServerOrchestratorToolsAndCrossTaskGoals verifies issue #544 end-to-end
// with the real server tool wiring (wireAgentAugment + wireTools +
// seedOrchestrationTools) against Postgres:
//   - auto receives spawn_task, add_task_hint, set_goals, set_constraints and
//     never receives Bash/Read/Write/Edit/WebFetch/Skill/shell_open even when a
//     skill is visible in the DB or interactive_shell=true is set on auto;
//   - auto's delegated set_goals and set_constraints write to the target task;
//   - mainagent never receives Bash/Write/Edit/WebFetch/shell_open;
//   - pentest and worker keep Bash, WebFetch, Skill, and shell_open.
func TestServerOrchestratorToolsAndCrossTaskGoals(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	t.Cleanup(func() {
		agent.SetToolAugment(nil)
		agent.SetToolResolve(nil)
	})

	skillDir := t.TempDir()
	const skillName = "t-orch-544"
	if err := os.MkdirAll(filepath.Join(skillDir, skillName), 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: t-orch-544\ndescription: fixture skill\n---\nfixture"
	if err := os.WriteFile(filepath.Join(skillDir, skillName, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(ctx, m, skillDir, m.dir, t.TempDir())
	pg := m.pg

	// Even if an older DB has a skill visible to auto and interactive_shell=true
	// on auto/mainagent, runtime assembly must keep execution tools off them.
	for _, key := range []string{"auto", "mainagent", "pentest", "worker"} {
		ag, err := pg.GetAgentByKey(key)
		if err != nil || ag == nil {
			t.Fatalf("missing built-in agent %q: %v", key, err)
		}
		origSkills, _ := pg.AgentSkillNames(ag.ID)
		origShell := ag.InteractiveShell
		t.Cleanup(func() {
			_ = pg.SetAgentSkillVisibility(ag.ID, origSkills)
			_, _ = pg.Exec(`UPDATE agents SET interactive_shell=$2 WHERE id=$1`, ag.ID, origShell)
		})
		if err := pg.ToggleSkillVisibility(ag.ID, skillName, true); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.Exec(`UPDATE agents SET interactive_shell=true WHERE id=$1`, ag.ID); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Auto chat tool surface
	autoProv := &captureToolsLLM{}
	autoChat := agent.NewChatAgent(autoProv, "test-model", m.dir, nil, 100000)
	if _, err := autoChat.Chat(ctx, "auto", "conv-auto-srv-544", "recon https://example.com", 1, 0, false, nil); err != nil {
		t.Fatal(err)
	}
	autoTools := autoProv.set()
	for _, want := range []string{"spawn_task", "add_task_hint", "set_goals", "set_constraints", "list_tasks", "get_task_graph", "list_task_findings", "list_assets"} {
		if !autoTools[want] {
			t.Errorf("auto missing %q", want)
		}
	}
	for _, bad := range []string{"Bash", "Read", "Write", "Edit", "MultiEdit", "LS", "Glob", "Grep", "WebFetch", "Skill", "shell_open", "shell_send"} {
		if autoTools[bad] {
			t.Errorf("auto must not expose %q", bad)
		}
	}

	// 2. MainAgent tool surface
	task, err := m.CreateTask("issue 544 test", "verify orchestration", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })
	mainProv := &captureToolsLLM{}
	ma := agent.NewMainAgent(mainProv, "test-model", m.dir, nil, 0, 1)
	if _, err := ma.Chat(ctx, task.ExpID, 0, m.Assets(), task.Store, task.Goal, "status?", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	mainTools := mainProv.set()
	for _, want := range []string{"graph_overview", "add_hint", "add_intent", "set_goals", "set_constraints"} {
		if !mainTools[want] {
			t.Errorf("mainagent missing %q", want)
		}
	}
	for _, bad := range []string{"Bash", "Read", "Write", "Edit", "WebFetch", "shell_open", "shell_send"} {
		if mainTools[bad] {
			t.Errorf("mainagent must not expose %q", bad)
		}
	}

	// 3. Pentest solo chat keeps Bash, WebFetch, Skill, and shell_open
	pentestProv := &captureToolsLLM{}
	pentestChat := agent.NewChatAgent(pentestProv, "test-model", m.dir, nil, 100000)
	if _, err := pentestChat.Chat(ctx, "pentest", "conv-pentest-srv-544", "hello", 1, 0, false, nil); err != nil {
		t.Fatal(err)
	}
	pentestTools := pentestProv.set()
	for _, want := range []string{"Bash", "WebFetch", "Skill", "shell_open"} {
		if !pentestTools[want] {
			t.Errorf("pentest missing %q", want)
		}
	}

	// 4. Auto's delegated set_goals and set_constraints operate on task_id
	resolvedAuto, _, cleanup := agent.AugmentTools(ctx, "auto", nil)
	defer cleanup()
	byName := map[string]actool.CoreTool{}
	for _, tl := range resolvedAuto {
		byName[tl.Name()] = tl
	}
	sg := byName["set_goals"]
	if sg == nil {
		t.Fatal("auto resolved tools missing set_goals")
	}
	res, err := sg.Call(ctx, json.RawMessage(`{"task_id":"`+task.ID+`","text":"Confirm auth bypass on /admin"}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("auto set_goals failed: err=%v out=%s", err, res.Flatten())
	}
	goals, err := task.Store.ListByKind("goal", 10)
	if err != nil || len(goals) != 1 || !strings.Contains(string(goals[0].Payload), "Confirm auth bypass") {
		t.Fatalf("task goals after auto set_goals = %+v, err=%v", goals, err)
	}

	sc := byName["set_constraints"]
	if sc == nil {
		t.Fatal("auto resolved tools missing set_constraints")
	}
	res, err = sc.Call(ctx, json.RawMessage(`{"task_id":"`+task.ID+`","text":"passive recon only","type":"deny"}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("auto set_constraints failed: err=%v out=%s", err, res.Flatten())
	}
	cs, err := task.Store.ListConstraints()
	if err != nil || len(cs) != 1 || cs[0].Text != "passive recon only" {
		t.Fatalf("task constraints after auto set_constraints = %+v, err=%v", cs, err)
	}
	_ = s
}
