package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
)

// bridgeCommandServer is a Server with one task and an enabled fake bridge
// that allows chat "100". Its service context starts cancelled, so no engine
// or background loop runs while the commands are exercised.
func bridgeCommandServer(t *testing.T) (*Server, *Task, *fakeBridgeTransport) {
	t.Helper()
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	task, err := m.CreateTask("chat bridge commands fixture", "check the login form", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM chat_bridges WHERE kind=$1`, fakeBridgeKind)
		_, _ = m.pg.DeleteFindingsByTask(tid)
		_ = m.pg.DeleteTask(tid)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	s.archiveWG.Wait()
	<-s.side.done

	fake := &fakeBridgeTransport{}
	s.chatBridges.transport = func(kind string) (chatbridge.Transport, bool) { return fake, kind == fakeBridgeKind }
	if _, err := m.pg.SaveChatBridge(context.Background(), &db.ChatBridge{
		Kind: fakeBridgeKind, Enabled: true, Config: json.RawMessage(`{"bot_token":"x"}`), AllowedChats: []string{"100"},
	}, false); err != nil {
		t.Fatal(err)
	}
	return s, task, fake
}

func runBridgeCommand(t *testing.T, s *Server, name, args string) string {
	t.Helper()
	cmds, text := s.chatBridgeHandlers(fakeBridgeKind)
	req := chatbridge.Request{Kind: fakeBridgeKind, ChatID: "100", UserID: "7", Command: name, Args: args}
	if name == "" {
		return text(context.Background(), req)
	}
	for _, c := range cmds {
		if c.Name == name {
			return c.Run(context.Background(), req)
		}
	}
	t.Fatalf("no /%s command", name)
	return ""
}

func TestChatBridgeTaskCommands(t *testing.T) {
	s, task, _ := bridgeCommandServer(t)
	ctx := context.Background()

	// Nothing selected yet: commands that need a task say how to pick one,
	// and plain text is not sent anywhere.
	for _, name := range []string{"task", "pause", "findings", ""} {
		if got := runBridgeCommand(t, s, name, ""); got != bridgeNoTaskReply {
			t.Fatalf("/%s without a task = %q", name, got)
		}
	}

	if got := runBridgeCommand(t, s, "tasks", ""); !strings.Contains(got, "#"+task.ID+" ") || !strings.Contains(got, "chat bridge commands fixture") {
		t.Fatalf("/tasks = %q", got)
	}
	if got := runBridgeCommand(t, s, "task", "abc"); !strings.Contains(got, "is a number") {
		t.Fatalf("/task abc = %q", got)
	}
	if got := runBridgeCommand(t, s, "task", "999999999"); !strings.Contains(got, "was not found") {
		t.Fatalf("/task missing = %q", got)
	}

	// Selecting is stored per chat and shown by /task and /tasks.
	got := runBridgeCommand(t, s, "task", "#"+task.ID)
	if !strings.Contains(got, "Selected task #"+task.ID) || !strings.Contains(got, "main agent") {
		t.Fatalf("/task id = %q", got)
	}
	if sel, err := s.m.PG().ChatBridgeTask(ctx, fakeBridgeKind, "100"); err != nil || sel != task.ID {
		t.Fatalf("stored selection = %q, %v", sel, err)
	}
	if got := runBridgeCommand(t, s, "task", ""); !strings.Contains(got, "Selected task #"+task.ID) {
		t.Fatalf("/task = %q", got)
	}
	if got := runBridgeCommand(t, s, "tasks", ""); !strings.Contains(got, "#"+task.ID+" ") || !strings.Contains(got, "(selected)") {
		t.Fatalf("/tasks after select = %q", got)
	}

	// /pause and /resume are the web UI's control.
	if got := runBridgeCommand(t, s, "pause", ""); !strings.Contains(got, "paused") {
		t.Fatalf("/pause = %q", got)
	}
	if !task.lifecycleSnapshot().Paused {
		t.Fatal("the task is not paused")
	}
	if got := runBridgeCommand(t, s, "pause", ""); !strings.Contains(got, "Could not pause") || !strings.Contains(got, "already paused") {
		t.Fatalf("second /pause = %q", got)
	}
	if got := runBridgeCommand(t, s, "resume", task.ID); !strings.Contains(got, "resumed") {
		t.Fatalf("/resume = %q", got)
	}
	if task.lifecycleSnapshot().Paused {
		t.Fatal("the task is still paused")
	}

	// /findings: titles, severities and statuses, never the evidence.
	tid, _ := strconv.ParseInt(task.ID, 10, 64)
	if got := runBridgeCommand(t, s, "findings", ""); !strings.Contains(got, "No findings yet") {
		t.Fatalf("/findings empty = %q", got)
	}
	if _, err := s.m.PG().AddFinding(tid, 0, "sqli", "SQL injection in login", "high", "the username field is injectable", "POST /login user=admin'-- SECRET-EVIDENCE", "w1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.PG().AddFinding(tid, 0, "info-leak", "", "low", "", "", "w1", nil); err != nil {
		t.Fatal(err)
	}
	got = runBridgeCommand(t, s, "findings", "")
	if !strings.Contains(got, "[high] SQL injection in login") || !strings.Contains(got, "[low] info-leak") || strings.Index(got, "[high]") > strings.Index(got, "[low]") {
		t.Fatalf("/findings = %q", got)
	}
	if strings.Contains(got, "SECRET-EVIDENCE") || strings.Contains(got, "injectable") {
		t.Fatalf("/findings leaked evidence or summary: %q", got)
	}
	// Links appear once a public address is configured.
	if err := s.m.PG().SetSetting(settingNotifyPublicBaseURL, "https://artex.example/"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.PG().Exec(`DELETE FROM settings WHERE key=$1`, settingNotifyPublicBaseURL) })
	if got := runBridgeCommand(t, s, "findings", ""); !strings.Contains(got, "https://artex.example/function/findings/detail?id=") {
		t.Fatalf("/findings links = %q", got)
	}
	if got := runBridgeCommand(t, s, "task", ""); !strings.Contains(got, "https://artex.example/function/tasks/detail?id="+task.ID) {
		t.Fatalf("/task link = %q", got)
	}

	// Plain text goes to the task's main agent: here the rule handler, as no
	// LLM is configured, so the reply comes back at once. The message is in
	// the shared main-agent transcript.
	got = runBridgeCommand(t, s, "", "how is it going?")
	if !strings.Contains(got, "rule mode") {
		t.Fatalf("text = %q", got)
	}
	acts, _, err := task.Store.ActivityList(nil, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		if a.Worker == "mainagent" && a.Kind == "user" && a.Summary == "how is it going?" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the message is not in the main-agent transcript: %+v", acts)
	}
}

func TestChatBridgeRelayAnswer(t *testing.T) {
	s, task, fake := bridgeCommandServer(t)
	// The relay runs on the service context; give it a live one.
	s.ctx = t.Context()
	tid, _ := strconv.ParseInt(task.ID, 10, 64)
	if _, err := s.m.PG().UpsertTaskCredential(tid, db.TaskCredentialInput{Domain: "app.example", Username: "admin", Password: "Correct-Horse-9", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	req := chatbridge.Request{Kind: fakeBridgeKind, ChatID: "100", UserID: "7"}

	answer := "Logged in as admin with Correct-Horse-9.\n```http\nGET /admin HTTP/1.1\nCookie: sid=abcdef123456\n```\nNext I will map the admin panel."
	s.bridgeRelayAnswer(req, task.ID, answer, nil)
	got := fake.sent["100"]
	if len(got) != 1 {
		t.Fatalf("relayed %d messages, want 1: %q", len(got), got)
	}
	msg := got[0]
	if !strings.HasPrefix(msg, "Task #"+task.ID+", main agent:") || !strings.Contains(msg, "Next I will map the admin panel.") {
		t.Fatalf("relay = %q", msg)
	}
	for _, leak := range []string{"Correct-Horse-9", "abcdef123456", "GET /admin"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("relay leaked %q: %q", leak, msg)
		}
	}

	// A turn that ended without an answer gets a fixed reason, not the error.
	fake.sent = nil
	s.bridgeRelayAnswer(req, task.ID, "", agent.AbortChatPausedWithTask)
	if got := fake.sent["100"]; len(got) != 1 || !strings.Contains(got[0], "the task was paused") {
		t.Fatalf("paused relay = %q", got)
	}

	// An answer for a chat that was removed from the allowlist meanwhile is
	// dropped.
	if _, err := s.m.PG().SaveChatBridge(context.Background(), &db.ChatBridge{Kind: fakeBridgeKind, Enabled: true, Config: json.RawMessage(`{"bot_token":"x"}`), AllowedChats: []string{"200"}}, false); err != nil {
		t.Fatal(err)
	}
	fake.sent = nil
	s.bridgeRelayAnswer(req, task.ID, "late answer", nil)
	if len(fake.sent) != 0 {
		t.Fatalf("an answer reached a chat that is no longer allowed: %q", fake.sent)
	}
}
