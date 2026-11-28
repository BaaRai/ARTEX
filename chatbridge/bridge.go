// Package chatbridge is the inbound side of the chat-app integration: an
// operator talks to ARTEX from a chat app (Telegram first) instead of the web
// UI. The outbound side, pushing findings, stays in package notify.
//
// The package is transport-agnostic. A Transport only knows how to fetch new
// updates, send a text reply and identify the bot; the Router owns every rule
// that does not depend on the chat app: the chat and user allowlists, the
// one-line refusal for unknown chats, dropping stale updates and command
// parsing.
//
// Like notify, the package imports nothing from db or server and transports
// are stateless: the configuration arrives as map[string]any (the decrypted
// chat_bridges.config row) on every call, so a token rotation takes effect on
// the next call without a restart.
package chatbridge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Update is one inbound event from a chat app, already normalized.
type Update struct {
	// ID is the transport's cursor. The next Poll asks for updates after it.
	ID int64
	// ChatID identifies the conversation (a private chat or a group). It is the
	// unit the allowlist works on.
	ChatID string
	// ChatType is ChatTypePrivate for a one-to-one chat with the bot. Any
	// other value (Telegram uses "group" and "supergroup") is a shared chat,
	// where the sender must also be an allowed user.
	ChatType string
	// UserID and UserName identify the sender inside the chat. UserID is
	// checked against the user allowlist in shared chats and kept for
	// attribution in logs.
	UserID   string
	UserName string
	// FromBot is set when the sender is itself a bot. Bot messages are ignored
	// so two bots in one group cannot drive each other.
	FromBot bool
	// Text is the message text. Empty for updates that carry no text (stickers,
	// photos, joins); those are ignored.
	Text string
	// Date is when the chat app received the message.
	Date time.Time
}

// ChatTypePrivate is the ChatType of a one-to-one chat with the bot.
// Transports map their own label for such chats to it.
const ChatTypePrivate = "private"

// Identity describes the bot account behind a configuration.
type Identity struct {
	ID       string `json:"id"`
	UserName string `json:"username"`
	Name     string `json:"name"`
}

// Transport is one chat app.
type Transport interface {
	// Kind is the stable identifier stored in chat_bridges.kind.
	Kind() string
	// Validate checks a configuration before it is saved or used.
	Validate(cfg map[string]any) error
	// SecretKeys names the configuration fields that are credentials. They
	// are sealed at rest by the db layer and masked in API responses.
	SecretKeys() []string
	// DestinationKeys names the fields that decide where the credentials are
	// sent. Changing one requires re-entering every secret.
	DestinationKeys() []string
	// ValidateChatID checks one chat allowlist entry.
	ValidateChatID(id string) error
	// ValidateUserID checks one user allowlist entry.
	ValidateUserID(id string) error
	// Identity asks the chat app which bot the configuration belongs to.
	Identity(ctx context.Context, cfg map[string]any) (Identity, error)
	// Poll returns the updates after cursor offset. It may block up to the
	// transport's long-poll window and returns early when ctx ends.
	Poll(ctx context.Context, cfg map[string]any, offset int64) ([]Update, error)
	// Send delivers a plain-text reply to a chat. Transports truncate text
	// that exceeds the chat app's message limit.
	Send(ctx context.Context, cfg map[string]any, chatID, text string) error
}

var registry = map[string]Transport{
	KindTelegram: telegramTransport{},
}

// Get returns the transport of a kind.
func Get(kind string) (Transport, bool) {
	t, ok := registry[kind]
	return t, ok
}

// Kinds lists the registered transport kinds in a stable order.
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MaxAllowedChats caps the allowlist. The bridge is an operator console, not
// a broadcast channel; a long list is more likely a mistake than a need.
const MaxAllowedChats = 50

// MaxAllowedUsers caps the user allowlist.
const MaxAllowedUsers = 100

// NormalizeAllowedChats trims, validates and de-duplicates a chat allowlist,
// keeping the operator's order.
func NormalizeAllowedChats(t Transport, ids []string) ([]string, error) {
	return normalizeIDs(ids, "chat", t.ValidateChatID, MaxAllowedChats)
}

// NormalizeAllowedUsers does the same for the user allowlist.
func NormalizeAllowedUsers(t Transport, ids []string) ([]string, error) {
	return normalizeIDs(ids, "user", t.ValidateUserID, MaxAllowedUsers)
}

func normalizeIDs(ids []string, what string, validate func(string) error, max int) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		if err := validate(id); err != nil {
			return nil, fmt.Errorf("%s id %q: %w", what, id, err)
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > max {
		return nil, fmt.Errorf("at most %d %ss can be allowed", max, what)
	}
	return out, nil
}

// The chat allowlist is mandatory in the sense that matters: the Router serves only
// listed chats, and every other chat gets one refusal and then silence. An
// empty allowlist is therefore a valid, closed state rather than an error: the
// bridge runs, serves nobody, and answers each chat with the refusal that names
// its chat ID. That is how an operator learns the ID to add.
