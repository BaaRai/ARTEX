package chatbridge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxUpdateAge is how old an update may be and still be acted on.
//
// Chat apps queue updates while the bridge is offline (Telegram keeps them for
// a day). Executing a backlog after a restart or an outage would run commands
// the operator typed long ago and has likely given up on, such as resuming a
// task they meant to keep paused. Anything older than this is dropped and
// logged instead.
const MaxUpdateAge = 2 * time.Minute

// maxRefusedChats bounds the memory of chats that were already refused once.
const maxRefusedChats = 1024

// Access is who may use a bridge.
type Access struct {
	// Chats are the allowed chat ids. Only these chats are served.
	Chats []string
	// Users are the allowed sender ids inside a shared (group) chat. A group
	// is served only for these senders, so joining an allowed group grants
	// nothing by itself. A private chat is its own user and needs no entry.
	Users []string
}

// Request is one authorized message handed to a command.
type Request struct {
	Kind     string // transport kind
	ChatID   string
	UserID   string
	UserName string
	Command  string // lower-case command name without the slash; "" for plain text
	Args     string // text after the command, or the whole text when Command is ""
}

// CommandFunc handles one command and returns the reply text. An empty reply
// sends nothing.
type CommandFunc func(ctx context.Context, req Request) string

// Command is one entry of the command table.
type Command struct {
	Name    string // without the slash, lower case
	Usage   string // argument hint shown by /help, for example "<id>"
	Summary string // one line shown by /help
	Run     CommandFunc
}

// Decision is what the Router did with an update, for logging and tests.
type Decision int

const (
	DecisionIgnored Decision = iota // no text, a bot sender, or a command for another bot
	DecisionStale                   // older than MaxUpdateAge
	DecisionRefused                 // unknown chat or group sender, refusal sent
	DecisionMuted                   // unknown chat or group sender, already refused once
	DecisionHandled                 // allowed chat, a command or text handler ran
)

func (d Decision) String() string {
	switch d {
	case DecisionStale:
		return "stale"
	case DecisionRefused:
		return "refused"
	case DecisionMuted:
		return "muted"
	case DecisionHandled:
		return "handled"
	default:
		return "ignored"
	}
}

// Router applies the transport-independent rules to an update and picks the
// handler. It is safe for concurrent use.
type Router struct {
	// BotUserName, when set, lets the router tell "/help@this_bot" from
	// "/help@another_bot" in a group. Commands addressed to another bot are
	// ignored.
	BotUserName string
	// Commands is the command table. /help is built in.
	Commands []Command
	// Text handles a message that is not a command. Nil replies with a hint to
	// use /help.
	Text CommandFunc
	// Now is the clock; nil uses time.Now.
	Now func() time.Time

	mu      sync.Mutex
	refused map[string]bool
}

// Route decides what to do with an update and returns the reply to send
// (possibly empty).
func (r *Router) Route(ctx context.Context, kind string, acc Access, u Update) (Decision, string) {
	if u.FromBot || strings.TrimSpace(u.Text) == "" {
		return DecisionIgnored, ""
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	if !u.Date.IsZero() && now().Sub(u.Date) > MaxUpdateAge {
		return DecisionStale, ""
	}
	if !contains(acc.Chats, u.ChatID) {
		return r.refuse(u.ChatID, "This chat is not allowed to use this ARTEX bot. If it is yours, add chat ID "+u.ChatID+" to the allowlist in ARTEX (System → Chat bot).")
	}
	if u.ChatType != ChatTypePrivate && (u.UserID == "" || !contains(acc.Users, u.UserID)) {
		// The chat is allowed but it is shared: serve only listed senders.
		// Anonymous group admins and channel posts carry no user to check.
		if u.UserID == "" {
			return DecisionIgnored, ""
		}
		return r.refuse(u.ChatID+"/"+u.UserID, "You are not allowed to use this ARTEX bot in this group. If you should be, add user ID "+u.UserID+" to the allowed users in ARTEX (System → Chat bot).")
	}

	name, args, forOther := parseCommand(u.Text, r.BotUserName)
	if forOther {
		return DecisionIgnored, ""
	}
	req := Request{Kind: kind, ChatID: u.ChatID, UserID: u.UserID, UserName: u.UserName, Command: name, Args: args}
	if name == "" {
		if r.Text == nil {
			return DecisionHandled, "Send /help to see what this bot can do."
		}
		return DecisionHandled, r.Text(ctx, req)
	}
	if name == "help" || name == "start" {
		return DecisionHandled, r.help()
	}
	for _, c := range r.Commands {
		if c.Name == name {
			return DecisionHandled, c.Run(ctx, req)
		}
	}
	return DecisionHandled, fmt.Sprintf("Unknown command /%s. Send /help to see the commands.", name)
}

// refuse answers an unknown chat, or an unknown sender in an allowed group,
// once and then stays silent, so a stranger who found the bot cannot use it to
// make it talk. The refusal names the id to allowlist, so the operator can add
// it when the chat or account is their own; it is the asker's own identifier
// and reveals nothing else. key is the chat id, or chat/user for a sender.
func (r *Router) refuse(key, text string) (Decision, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refused == nil {
		r.refused = map[string]bool{}
	}
	if r.refused[key] {
		return DecisionMuted, ""
	}
	if len(r.refused) >= maxRefusedChats {
		// A flood of distinct chats: start over rather than grow without
		// bound. Muting every new chat instead would also mute the operator's
		// own chat, which then never learns its ID. The cost is that a chat
		// refused before the reset can get one more refusal.
		clear(r.refused)
	}
	r.refused[key] = true
	return DecisionRefused, text
}

// KeepRefusals carries the refusal memory of a previous router over, so
// saving the configuration of the same bot does not refuse every stranger a
// second time. Call it before the router is in use.
func (r *Router) KeepRefusals(prev *Router) {
	if prev == nil || prev == r {
		return
	}
	prev.mu.Lock()
	defer prev.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refused = make(map[string]bool, len(prev.refused))
	for id := range prev.refused {
		r.refused[id] = true
	}
}

func (r *Router) help() string {
	var b strings.Builder
	b.WriteString("ARTEX bot commands:\n/help - this list")
	cmds := append([]Command(nil), r.Commands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	for _, c := range cmds {
		b.WriteString("\n/")
		b.WriteString(c.Name)
		if c.Usage != "" {
			b.WriteString(" ")
			b.WriteString(c.Usage)
		}
		if c.Summary != "" {
			b.WriteString(" - ")
			b.WriteString(c.Summary)
		}
	}
	return b.String()
}

// parseCommand splits "/name@bot args". It returns name "" for plain text,
// and forOther when the command is addressed to a different bot.
func parseCommand(text, botUserName string) (name, args string, forOther bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", text, false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, target, addressed := strings.Cut(head, "@")
	if head == "" {
		return "", text, false
	}
	if addressed && botUserName != "" && !strings.EqualFold(target, botUserName) {
		return "", "", true
	}
	return strings.ToLower(head), strings.TrimSpace(rest), false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
