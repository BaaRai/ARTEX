package agent

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

// captureToolNamesProvider records the tool names offered to the model on its
// first completion/stream request and ends the turn immediately.
type captureToolNamesProvider struct {
	mu    sync.Mutex
	names []string
}

func (p *captureToolNamesProvider) record(req llm.CompletionRequest) {
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

func (p *captureToolNamesProvider) seen() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]bool, len(p.names))
	for _, n := range p.names {
		out[n] = true
	}
	return out
}

func (p *captureToolNamesProvider) Complete(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.record(req)
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("ok")}}, "end_turn", llm.Usage{}, nil
}

func (p *captureToolNamesProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.record(req)
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "ok"}, nil)
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
	}
}

func dummyTool(name string) actool.CoreTool {
	return readTool(name, "test fixture "+name, obj(map[string]any{}), func(context.Context, json.RawMessage) (actool.Result, error) {
		return actool.Text("ok"), nil
	})
}

func toolNameSet(tools []actool.CoreTool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, t := range tools {
		out[t.Name()] = true
	}
	return out
}

// TestOrchestratorAgentsExcludeExecutionTools pins issue #544:
//   - auto receives only its orchestration/domain tools (no DefaultTools, no WebFetch, no Skill);
//   - mainagent receives MainAgentTools only (no DefaultTools, no WebFetch);
//   - solo pentest, custom chat assistants, and workers keep Bash and WebFetch.
func TestOrchestratorAgentsExcludeExecutionTools(t *testing.T) {
	oldAug, oldRes := ToolAugment(), ToolResolve()
	t.Cleanup(func() {
		SetToolAugment(oldAug)
		SetToolResolve(oldRes)
	})
	SetToolResolve(nil)
	SetToolAugment(func(_ context.Context, agentKey string) ([]actool.CoreTool, DeferredInfo, func()) {
		if agentKey == "auto" {
			return []actool.CoreTool{
				dummyTool("spawn_task"),
				dummyTool("add_task_hint"),
				dummyTool("set_goals"),
				dummyTool("set_constraints"),
				dummyTool("list_tasks"),
			}, DeferredInfo{}, func() {}
		}
		return nil, DeferredInfo{}, func() {}
	})

	workDir := t.TempDir()
	forbiddenForOrchestrators := []string{"Bash", "Read", "Write", "Edit", "MultiEdit", "LS", "Glob", "Grep", "WebFetch", "Skill"}

	t.Run("auto chat gets orchestration tools only", func(t *testing.T) {
		prov := &captureToolNamesProvider{}
		c := NewChatAgent(prov, "test-model", workDir, nil, 100000)
		if _, err := c.Chat(t.Context(), "auto", "conv-auto-544", "recon https://example.com", 1, 0, false, nil); err != nil {
			t.Fatal(err)
		}
		got := prov.seen()
		for _, want := range []string{"spawn_task", "add_task_hint", "set_goals", "set_constraints", "list_tasks"} {
			if !got[want] {
				t.Errorf("auto missing orchestration tool %q (got: %v)", want, got)
			}
		}
		for _, bad := range forbiddenForOrchestrators {
			if got[bad] {
				t.Errorf("auto must not have execution tool %q", bad)
			}
		}
	})

	t.Run("pentest solo chat keeps Bash and WebFetch", func(t *testing.T) {
		prov := &captureToolNamesProvider{}
		c := NewChatAgent(prov, "test-model", workDir, nil, 100000)
		if _, err := c.Chat(t.Context(), "pentest", "conv-pentest-544", "hello", 1, 0, false, nil); err != nil {
			t.Fatal(err)
		}
		got := prov.seen()
		for _, want := range []string{"Bash", "Read", "Write", "Edit", "WebFetch"} {
			if !got[want] {
				t.Errorf("pentest missing expected tool %q", want)
			}
		}
	})

	t.Run("custom chat agent keeps Bash and WebFetch", func(t *testing.T) {
		prov := &captureToolNamesProvider{}
		c := NewChatAgent(prov, "test-model", workDir, nil, 100000)
		if _, err := c.Chat(t.Context(), "custom-helper", "conv-custom-544", "hello", 1, 0, false, nil); err != nil {
			t.Fatal(err)
		}
		got := prov.seen()
		for _, want := range []string{"Bash", "WebFetch"} {
			if !got[want] {
				t.Errorf("custom chat agent missing expected tool %q", want)
			}
		}
	})

	t.Run("mainagent has MainAgentTools and no Bash/Write/Edit/WebFetch", func(t *testing.T) {
		ts := NewToolSet(nil, "human")
		tools, _, cleanup := AugmentTools(t.Context(), "mainagent", ts.DropCoverageTools(ts.MainAgentTools()))
		defer cleanup()
		got := toolNameSet(tools)
		for _, want := range []string{"graph_overview", "add_hint", "add_intent", "steer_work", "set_goals", "set_constraints", "list_findings"} {
			if !got[want] {
				t.Errorf("mainagent missing domain tool %q", want)
			}
		}
		for _, bad := range forbiddenForOrchestrators {
			if got[bad] {
				t.Errorf("mainagent must not have execution tool %q", bad)
			}
		}
	})

	t.Run("mainagent live chat advertises no Bash/Write/Edit/WebFetch", func(t *testing.T) {
		d := testDB(t)
		t.Cleanup(func() { _ = d.Close() })
		expID, err := d.CreateExploration("mainagent tools", "verify orchestration-only")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })
		prov := &captureToolNamesProvider{}
		m := NewMainAgent(prov, "test-model", workDir, nil, 0, 1)
		if _, err := m.Chat(t.Context(), expID, 0, nil, d.Exploration(expID), "verify goal", "status?", nil, nil, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		got := prov.seen()
		for _, want := range []string{"graph_overview", "add_hint", "add_intent", "set_goals", "set_constraints"} {
			if !got[want] {
				t.Errorf("mainagent chat missing %q", want)
			}
		}
		for _, bad := range forbiddenForOrchestrators {
			if got[bad] {
				t.Errorf("mainagent chat must not have %q", bad)
			}
		}
	})

	t.Run("worker keeps Bash and WebFetch", func(t *testing.T) {
		d := testDB(t)
		t.Cleanup(func() { _ = d.Close() })
		expID, err := d.CreateExploration("worker tools", "verify execution tools")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })
		es := d.Exploration(expID)
		payload := map[string]any{"summary": "probe"}
		intentID, err := es.AddNode("intent", payload, 0, "running", "planner", nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(payload)
		prov := &captureToolNamesProvider{}
		w := NewWorker(prov, "test-model", workDir, nil, 0, 1)
		if _, _, err := w.Execute(t.Context(), "work#1", expID, nil, es, &db.Node{ID: intentID, Payload: raw}, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		got := prov.seen()
		for _, want := range []string{"Bash", "WebFetch", "record_fact", "report_finding"} {
			if !got[want] {
				t.Errorf("worker missing expected tool %q", want)
			}
		}
	})
}

func TestAutoBuiltinSeedsAndPromptContract(t *testing.T) {
	byKey := map[string]ToolSeed{}
	for _, s := range BuiltinToolSeeds() {
		byKey[s.Key] = s
	}
	for _, key := range []string{"set_goals", "set_constraints"} {
		s, ok := byKey[key]
		if !ok {
			t.Fatalf("missing seed %q", key)
		}
		if !slices.Contains(s.Agents, "auto") {
			t.Errorf("%s default agents = %v, want auto included", key, s.Agents)
		}
		if !slices.Contains(s.Agents, "mainagent") || !slices.Contains(s.Agents, "goals") {
			t.Errorf("%s lost existing default agents: %v", key, s.Agents)
		}
	}

	autoPrompt := BuiltinPromptSeeds()["auto"]
	for _, want := range []string{"spawn_task", "seed_first_intent=true", "add_task_hint", "set_goals", "set_constraints"} {
		if !strings.Contains(autoPrompt, want) {
			t.Errorf("autoDefaultTmpl missing %q", want)
		}
	}
	if strings.Contains(autoPrompt, "update_skill ") || strings.Contains(autoPrompt, "update_skill to") {
		t.Errorf("autoDefaultTmpl references stale tool name update_skill instead of update_skill_file")
	}
}
