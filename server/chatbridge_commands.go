package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
)

// Task commands of the chat bridges (issue #5, part 2). Each command calls the
// same server function as the web UI, so a pause from the chat app is the
// same pause as the button: /pause and /resume go through applyTaskControl,
// and plain text goes through startTaskChat into the task's one main-agent
// conversation, which the web chat shows too.
//
// A chat selects the task it works on with /task <id>; the selection is
// stored per chat (chat_bridge_chats), so two operators in two chats can
// work on different tasks. The commands that take [id] act on that task
// when no id is given.
//
// What goes back to the chat is a summary and a link, never evidence or a
// credential: task and finding titles, statuses and counts, and the
// main agent's answer after chatbridge.SafeText.

const (
	// chatBridgeListLimit caps /tasks and /findings.
	chatBridgeListLimit = 10
	// chatBridgeTitleRunes caps a task or finding title in a list.
	chatBridgeTitleRunes = 80
	// chatBridgeAnswerRunes caps a relayed main-agent answer. The full answer
	// stays in ARTEX; the reply links to it.
	chatBridgeAnswerRunes = 1500
)

// chatBridgeHandlers is the command table and the text handler the bridge
// runner uses.
func (s *Server) chatBridgeHandlers(kind string) ([]chatbridge.Command, chatbridge.CommandFunc) {
	cmds := append(defaultChatBridgeCommands(kind),
		chatbridge.Command{Name: "tasks", Summary: "list recent tasks", Run: s.bridgeTasks},
		chatbridge.Command{Name: "task", Usage: "[id]", Summary: "select the task this chat works on, or show it", Run: s.bridgeTask},
		chatbridge.Command{Name: "pause", Usage: "[id]", Summary: "pause the task", Run: s.bridgePause},
		chatbridge.Command{Name: "resume", Usage: "[id]", Summary: "resume the task", Run: s.bridgeResume},
		chatbridge.Command{Name: "findings", Usage: "[id]", Summary: "the task's findings, most severe first", Run: s.bridgeFindings},
	)
	return cmds, s.bridgeText
}

const bridgeNoTaskReply = "No task selected. Send /tasks to see the tasks, then /task <id>. After that, messages without a slash go to that task's main agent."

// bridgeTaskFor resolves the task a command acts on: the id in arg, or the
// task the chat selected. It returns the task or the reply to send instead.
func (s *Server) bridgeTaskFor(ctx context.Context, req chatbridge.Request, arg string) (*Task, string) {
	id := strings.TrimPrefix(strings.TrimSpace(arg), "#")
	if id == "" {
		selected, err := s.m.PG().ChatBridgeTask(ctx, req.Kind, req.ChatID)
		if err != nil {
			log.Printf("%s read the selected task of chat %s: %v", chatBridgeLogTag(req.Kind), req.ChatID, err)
			return nil, "Could not read the selected task. Try again."
		}
		if selected == "" {
			return nil, bridgeNoTaskReply
		}
		id = selected
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return nil, "A task id is a number, for example /task 12. Send /tasks to see the tasks."
	}
	id = strconv.FormatInt(n, 10) // "012" is task 12
	t, ok := s.m.Task(id)
	if !ok {
		return nil, "Task #" + id + " was not found. Send /tasks to see the tasks."
	}
	return t, ""
}

func (s *Server) bridgeTasks(ctx context.Context, req chatbridge.Request) string {
	list := s.m.List()
	if len(list) == 0 {
		return "There are no tasks yet. Create one in ARTEX."
	}
	metrics, err := s.m.PG().TaskListMetricsAll()
	if err != nil {
		log.Printf("%s task list metrics: %v", chatBridgeLogTag(req.Kind), err)
	}
	selected, _ := s.m.PG().ChatBridgeTask(ctx, req.Kind, req.ChatID)
	var b strings.Builder
	fmt.Fprintf(&b, "Tasks (%d", min(len(list), chatBridgeListLimit))
	if len(list) > chatBridgeListLimit {
		fmt.Fprintf(&b, " of %d", len(list))
	}
	b.WriteString("):")
	for _, t := range list[:min(len(list), chatBridgeListLimit)] {
		fmt.Fprintf(&b, "\n#%s %s · %s", t.ID, s.resolvedTaskStatus(t), bridgeTaskTitle(t))
		if f := bridgeSeverityLine(metrics[t.ExpID].Findings, true); f != "" {
			b.WriteString(" · " + f)
		}
		if t.ID == selected {
			b.WriteString(" (selected)")
		}
	}
	b.WriteString("\n\nSend /task <id> to work on one.")
	return b.String()
}

func (s *Server) bridgeTask(ctx context.Context, req chatbridge.Request) string {
	if strings.TrimSpace(req.Args) == "" {
		t, reply := s.bridgeTaskFor(ctx, req, "")
		if t == nil {
			return reply
		}
		return s.bridgeTaskSummary(t, "Selected task")
	}
	t, reply := s.bridgeTaskFor(ctx, req, req.Args)
	if t == nil {
		return reply
	}
	id, _ := strconv.ParseInt(t.ID, 10, 64)
	if err := s.m.PG().SetChatBridgeTask(ctx, req.Kind, req.ChatID, id); err != nil {
		log.Printf("%s select task %s in chat %s: %v", chatBridgeLogTag(req.Kind), t.ID, req.ChatID, err)
		return "Could not select task #" + t.ID + ". Try again."
	}
	log.Printf("%s chat %s selected task %s (user %s)", chatBridgeLogTag(req.Kind), req.ChatID, t.ID, req.UserID)
	return s.bridgeTaskSummary(t, "Selected task") + "\n\nMessages without a slash now go to this task's main agent."
}

// bridgeTaskSummary is the status card of one task.
func (s *Server) bridgeTaskSummary(t *Task, heading string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s #%s: %s\nStatus: %s", heading, t.ID, bridgeTaskTitle(t), s.resolvedTaskStatus(t))
	if m, err := s.m.PG().TaskListMetrics(t.ExpID); err == nil {
		if m.Goals.Total > 0 {
			fmt.Fprintf(&b, " · goals %d/%d met", m.Goals.Met, m.Goals.Total)
		}
		if m.RunningIntents > 0 {
			fmt.Fprintf(&b, " · %d worker(s) running", m.RunningIntents)
		}
		fmt.Fprintf(&b, "\nFindings: %s", bridgeSeverityLine(m.Findings, false))
	}
	if link := s.bridgeTaskLink(t.ID); link != "" {
		b.WriteString("\n" + link)
	}
	return b.String()
}

func (s *Server) bridgePause(ctx context.Context, req chatbridge.Request) string {
	return s.bridgeControl(ctx, req, "pause")
}

func (s *Server) bridgeResume(ctx context.Context, req chatbridge.Request) string {
	return s.bridgeControl(ctx, req, "resume")
}

func (s *Server) bridgeControl(ctx context.Context, req chatbridge.Request, action string) string {
	t, reply := s.bridgeTaskFor(ctx, req, req.Args)
	if t == nil {
		return reply
	}
	result, err := s.applyTaskControl(t, action)
	if err != nil {
		// applyTaskControl's errors are the operator-facing reasons the web UI
		// shows too ("task is already paused").
		return fmt.Sprintf("Could not %s task #%s: %v.", action, t.ID, err)
	}
	log.Printf("%s user %s in chat %s: %s task %s", chatBridgeLogTag(req.Kind), req.UserID, req.ChatID, action, t.ID)
	switch {
	case action == "pause":
		return "Task #" + t.ID + " paused. Its main agent still answers messages."
	case result.Queued:
		return "Task #" + t.ID + " resumed. It is queued and starts when a run slot frees up."
	default:
		return "Task #" + t.ID + " resumed."
	}
}

func (s *Server) bridgeFindings(ctx context.Context, req chatbridge.Request) string {
	t, reply := s.bridgeTaskFor(ctx, req, req.Args)
	if t == nil {
		return reply
	}
	list, err := s.m.PG().ListFindingsFiltered(db.FindingFilter{TaskID: t.ID, Sort: "severity"}, chatBridgeListLimit)
	if err != nil {
		log.Printf("%s findings of task %s: %v", chatBridgeLogTag(req.Kind), t.ID, err)
		return "Could not read the findings. Try again."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Findings of task #%s", t.ID)
	if m, err := s.m.PG().TaskListMetrics(t.ExpID); err == nil {
		b.WriteString(": " + bridgeSeverityLine(m.Findings, false))
	}
	if len(list) == 0 {
		b.WriteString("\nNo findings yet.")
	}
	for _, f := range list {
		title := f.Name
		if title == "" {
			title = f.VulnClass
		}
		fmt.Fprintf(&b, "\n[%s] %s · %s", strings.ToLower(f.Severity), bridgeCut(oneLine(title), chatBridgeTitleRunes), strings.ReplaceAll(f.Status, "_", " "))
		if link := s.bridgeFindingLink(f.ID); link != "" {
			b.WriteString("\n" + link)
		}
	}
	if link := s.bridgeTaskLink(t.ID); link != "" && len(list) == 0 {
		b.WriteString("\n" + link)
	}
	return b.String()
}

// bridgeText sends a message without a slash to the main agent of the chat's
// task. The answer comes later; it is relayed by bridgeRelayAnswer.
func (s *Server) bridgeText(ctx context.Context, req chatbridge.Request) string {
	t, reply := s.bridgeTaskFor(ctx, req, "")
	if t == nil {
		return reply
	}
	agentMessage, err := composeChatMentionMessage(s.m.pg, req.Args)
	if err != nil {
		return "Could not send the message: " + err.Error()
	}
	mode, ruleReply, err := s.startTaskChat(t, taskChatTurn{
		Message:      req.Args,
		AgentMessage: agentMessage,
		Done:         func(answer string, err error) { s.bridgeRelayAnswer(req, t.ID, answer, err) },
	})
	switch {
	case errors.Is(err, errTaskChatBusy):
		return "The main agent of task #" + t.ID + " is still answering the previous message. Send yours again when that answer arrives."
	case err != nil:
		return "Task #" + t.ID + " cannot take messages now: " + err.Error() + "."
	}
	log.Printf("%s user %s in chat %s sent a message to the main agent of task %s", chatBridgeLogTag(req.Kind), req.UserID, req.ChatID, t.ID)
	if mode == "rule" {
		return chatbridge.SafeText(ruleReply, nil, chatBridgeAnswerRunes)
	}
	return "Sent to the main agent of task #" + t.ID + ". Its answer will follow here."
}

// bridgeRelayAnswer sends the end of a main-agent turn back to the chat that
// started it.
func (s *Server) bridgeRelayAnswer(req chatbridge.Request, taskID, answer string, turnErr error) {
	var b strings.Builder
	switch {
	case turnErr != nil:
		fmt.Fprintf(&b, "The main agent of task #%s stopped without an answer: %s.", taskID, bridgeTurnEndReason(turnErr))
	case strings.TrimSpace(answer) == "":
		fmt.Fprintf(&b, "The main agent of task #%s finished without a text answer.", taskID)
	default:
		fmt.Fprintf(&b, "Task #%s, main agent:\n\n%s", taskID, chatbridge.SafeText(answer, s.bridgeTaskSecrets(taskID), chatBridgeAnswerRunes))
	}
	if link := s.bridgeTaskLink(taskID); link != "" {
		b.WriteString("\n\nFull conversation: " + link)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := s.chatBridges.Deliver(ctx, req.Kind, req.ChatID, b.String()); err != nil && s.ctx.Err() == nil {
		log.Printf("%s answer of task %s to chat %s not delivered: %v", chatBridgeLogTag(req.Kind), taskID, req.ChatID, err)
	}
}

// bridgeTurnEndReason names why a turn ended without an answer. Only fixed
// texts go out: an LLM error message can carry a provider URL or a request
// excerpt.
func bridgeTurnEndReason(err error) string {
	switch {
	case errors.Is(err, agent.AbortChatPausedWithTask):
		return "the task was paused"
	case errors.Is(err, agent.AbortChatStoppedByUser):
		return "the turn was stopped in ARTEX"
	case errors.Is(err, context.Canceled):
		return "the turn was cancelled"
	default:
		return "it hit an error, see the task in ARTEX"
	}
}

// bridgeTaskSecrets are the stored credentials of a task, masked in what the
// bridge relays.
func (s *Server) bridgeTaskSecrets(taskID string) []string {
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return nil
	}
	creds, err := s.m.PG().TaskCredentials(id)
	if err != nil {
		log.Printf("[chatbridge] read credentials of task %s to mask them: %v", taskID, err)
		return nil
	}
	out := make([]string, 0, len(creds))
	for _, c := range creds {
		if c.Password != "" {
			out = append(out, c.Password)
		}
	}
	return out
}

// bridgeTaskLink and bridgeFindingLink point at the web UI through the
// public address configured for notification back-links. Without one the
// replies carry no link.
func (s *Server) bridgeTaskLink(taskID string) string {
	base := trimTrailingSlash(notifyPublicBaseURL(s.m.PG()))
	if base == "" {
		return ""
	}
	return base + "/function/tasks/detail?id=" + taskID
}

func (s *Server) bridgeFindingLink(id int64) string {
	base := trimTrailingSlash(notifyPublicBaseURL(s.m.PG()))
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/function/findings/detail?id=%d", base, id)
}

// bridgeTaskTitle is the task name, or its description or goal when unnamed.
func bridgeTaskTitle(t *Task) string {
	title := t.lifecycleSnapshot().Name
	if strings.TrimSpace(title) == "" {
		title = t.Description
	}
	if strings.TrimSpace(title) == "" {
		title = t.Goal
	}
	if strings.TrimSpace(title) == "" {
		return "(unnamed)"
	}
	return bridgeCut(oneLine(title), chatBridgeTitleRunes)
}

// bridgeSeverityLine renders finding counts. compact skips zero buckets and
// returns "" when there are none.
func bridgeSeverityLine(c db.FindingSeverityCounts, compact bool) string {
	parts := []struct {
		n    int
		name string
	}{{c.Critical, "critical"}, {c.High, "high"}, {c.Medium, "medium"}, {c.Low, "low"}}
	var out []string
	for _, p := range parts {
		if compact && p.n == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%d %s", p.n, p.name))
	}
	if compact && len(out) == 0 {
		return ""
	}
	return strings.Join(out, ", ")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func bridgeCut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
