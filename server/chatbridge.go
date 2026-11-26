package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
)

// The inbound chat bridge (issue #5): one long-poll loop per registered
// transport kind, started by server.New. The loop re-reads its row on every
// round, so saving the configuration (a new token, a new allowlist, enabling
// or disabling) takes effect without a restart; Reload interrupts a poll that
// is waiting so the change applies at once instead of after the poll window.
//
// Order of work for a batch: commit the cursor, then act. If ARTEX crashes in
// between, the updates of that batch are lost rather than executed twice. For
// an operator console that is the safe side: a lost "/pause" is retyped, a
// replayed one acts on a task the operator may have resumed since.

const (
	// chatBridgeIdleWait is how long a disabled or unconfigured bridge sleeps
	// before it looks at its row again. Saving the row wakes it earlier.
	chatBridgeIdleWait = 30 * time.Second
	chatBridgeMinRetry = 2 * time.Second
	chatBridgeMaxRetry = time.Minute
	chatBridgeSendWait = 15 * time.Second
	// chatBridgeEmptyPollWait is the pause after a poll that returned nothing.
	chatBridgeEmptyPollWait = time.Second
)

// chatBridgeStatus is the live state shown in the UI ("bot online").
type chatBridgeStatus struct {
	// State is off (disabled or not configured), connecting, online or error.
	State        string               `json:"state"`
	Bot          *chatbridge.Identity `json:"bot,omitempty"`
	LastError    string               `json:"last_error,omitempty"`
	LastErrorAt  *time.Time           `json:"last_error_at,omitempty"`
	LastPollAt   *time.Time           `json:"last_poll_at,omitempty"`
	LastUpdateAt *time.Time           `json:"last_update_at,omitempty"`
}

type chatBridgeState struct {
	wake   chan struct{}
	cancel context.CancelFunc // cancels the poll in flight; nil when none
	status chatBridgeStatus
	router *chatbridge.Router
	sig    string // hash of the config the router and identity were built for
	retry  time.Duration
}

type chatBridgeRunner struct {
	pg *db.DB
	// transport resolves a kind; tests substitute a fake.
	transport func(kind string) (chatbridge.Transport, bool)
	// commands builds the command table of a bridge; the server's commands
	// are added here as they land.
	commands func(kind string) []chatbridge.Command

	mu    sync.Mutex
	state map[string]*chatBridgeState
}

func newChatBridgeRunner(pg *db.DB) *chatBridgeRunner {
	return &chatBridgeRunner{
		pg:        pg,
		transport: chatbridge.Get,
		commands:  defaultChatBridgeCommands,
		state:     map[string]*chatBridgeState{},
	}
}

// defaultChatBridgeCommands is the command table. /help is built into the
// router. Task and approval commands follow in later changes.
func defaultChatBridgeCommands(string) []chatbridge.Command {
	return []chatbridge.Command{{
		Name:    "whoami",
		Summary: "show this chat's ID and your user ID",
		Run: func(_ context.Context, req chatbridge.Request) string {
			return fmt.Sprintf("Chat ID: %s\nYour user ID: %s", req.ChatID, req.UserID)
		},
	}}
}

func (r *chatBridgeRunner) get(kind string) *chatBridgeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.state[kind]
	if st == nil {
		st = &chatBridgeState{wake: make(chan struct{}, 1), status: chatBridgeStatus{State: "off"}}
		r.state[kind] = st
	}
	return st
}

// Status returns a copy of a bridge's live state.
func (r *chatBridgeRunner) Status(kind string) chatBridgeStatus {
	if r == nil {
		return chatBridgeStatus{State: "off"}
	}
	st := r.get(kind)
	r.mu.Lock()
	defer r.mu.Unlock()
	out := st.status
	if out.Bot != nil {
		b := *out.Bot
		out.Bot = &b
	}
	return out
}

// Reload makes the loop of kind re-read its configuration now, interrupting
// a poll in flight.
func (r *chatBridgeRunner) Reload(kind string) {
	if r == nil {
		return
	}
	st := r.get(kind)
	r.mu.Lock()
	if st.cancel != nil {
		st.cancel()
	}
	st.sig = "" // rebuild identity and router from the saved row
	st.retry = 0
	r.mu.Unlock()
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

// Run starts one loop per registered transport and returns when ctx ends.
func (r *chatBridgeRunner) Run(ctx context.Context) {
	if r == nil || r.pg == nil {
		return
	}
	var wg sync.WaitGroup
	for _, kind := range chatbridge.Kinds() {
		wg.Add(1)
		go func(kind string) {
			defer wg.Done()
			r.loop(ctx, kind)
		}(kind)
	}
	wg.Wait()
}

func (r *chatBridgeRunner) loop(ctx context.Context, kind string) {
	st := r.get(kind)
	for ctx.Err() == nil {
		wait := r.step(ctx, kind)
		if wait <= 0 {
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-st.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// step runs one round for kind: load the row, make sure the bot identity is
// known, poll once and act on the batch. It returns how long to wait before
// the next round (0 = poll again at once).
func (r *chatBridgeRunner) step(ctx context.Context, kind string) time.Duration {
	tag := chatBridgeLogTag(kind)
	tr, ok := r.transport(kind)
	if !ok {
		return chatBridgeIdleWait
	}
	b, err := r.pg.ChatBridgeByKind(ctx, kind)
	if errors.Is(err, db.ErrChatBridgeNotFound) || (err == nil && !b.Enabled) {
		r.setOff(kind)
		return chatBridgeIdleWait
	}
	if err != nil {
		return r.fail(kind, fmt.Errorf("load configuration: %w", err))
	}
	var cfg map[string]any
	if err := json.Unmarshal(b.Config, &cfg); err != nil {
		return r.fail(kind, fmt.Errorf("configuration is not valid JSON: %w", err))
	}
	if err := tr.Validate(cfg); err != nil {
		return r.fail(kind, err)
	}

	st := r.get(kind)
	sig := configSig(b.Config)
	r.mu.Lock()
	known := st.sig == sig && st.router != nil
	router := st.router
	prevBot := st.status.Bot
	r.mu.Unlock()
	if !known {
		r.setState(kind, "connecting")
		me, err := tr.Identity(ctx, cfg)
		if err != nil {
			return r.fail(kind, err)
		}
		prev := router
		router = &chatbridge.Router{BotUserName: me.UserName, Commands: r.commands(kind)}
		if prevBot != nil && prevBot.ID == me.ID {
			// Same bot, new settings: chats it already refused stay refused.
			router.KeepRefusals(prev)
		}
		r.mu.Lock()
		st.sig, st.router = sig, router
		st.status.Bot = &me
		r.mu.Unlock()
		log.Printf("%s online as @%s, %d allowed chat(s)", tag, me.UserName, len(b.AllowedChats))
	}

	pollCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	st.cancel = cancel
	r.mu.Unlock()
	updates, err := tr.Poll(pollCtx, cfg, b.PollOffset)
	r.mu.Lock()
	st.cancel = nil
	r.mu.Unlock()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		if errors.Is(err, context.Canceled) {
			return 0 // Reload interrupted the poll: re-read the row at once
		}
		var ra *chatbridge.RetryAfterError
		if errors.As(err, &ra) {
			r.fail(kind, err)
			// The wait comes from the server. Cap it so a broken or hostile
			// API address cannot park the bridge for hours; polling early
			// only earns another 429 with a fresh retry_after.
			return min(max(ra.After, chatBridgeMinRetry), chatBridgeMaxRetry)
		}
		return r.fail(kind, err)
	}
	now := time.Now()
	r.mu.Lock()
	st.status.State, st.status.LastPollAt, st.retry = "online", &now, 0
	r.mu.Unlock()
	if len(updates) == 0 {
		// A real long poll returns empty only after its window, so this
		// pause costs nothing there; it keeps a self-hosted Bot API that
		// ignores the timeout from spinning the loop.
		return chatBridgeEmptyPollWait
	}

	next := b.PollOffset
	for _, u := range updates {
		if u.ID >= next {
			next = u.ID + 1
		}
	}
	wrote, err := r.pg.SetChatBridgeOffset(ctx, kind, b.ConfigVersion, next)
	if err != nil {
		// Acting without a committed cursor would repeat the batch on the
		// next poll. Skip it and try again.
		return r.fail(kind, fmt.Errorf("save update cursor: %w", err))
	}
	if !wrote {
		// The configuration was saved while this poll ran (a new token, a
		// new allowlist, or the bridge was disabled). The batch belongs to
		// the old configuration: drop it and start over from the saved row.
		log.Printf("%s configuration changed during a poll, %d update(s) dropped", tag, len(updates))
		return 0
	}
	r.mu.Lock()
	st.status.LastUpdateAt = &now
	r.mu.Unlock()

	for _, u := range updates {
		decision, reply := router.Route(ctx, kind, b.AllowedChats, u)
		switch decision {
		case chatbridge.DecisionRefused:
			log.Printf("%s refused chat %s (user %s @%s): not in the allowlist", tag, u.ChatID, u.UserID, u.UserName)
		case chatbridge.DecisionStale:
			log.Printf("%s dropped a message from chat %s sent %s ago (older than %s)", tag, u.ChatID, now.Sub(u.Date).Round(time.Second), chatbridge.MaxUpdateAge)
		case chatbridge.DecisionHandled:
			// The message text is not logged: it can carry anything the
			// operator typed, including target details.
			log.Printf("%s handled a message in chat %s from user %s", tag, u.ChatID, u.UserID)
		}
		if reply == "" {
			continue
		}
		sendCtx, cancelSend := context.WithTimeout(ctx, chatBridgeSendWait)
		if err := tr.Send(sendCtx, cfg, u.ChatID, reply); err != nil {
			log.Printf("%s reply to chat %s failed: %v", tag, u.ChatID, err)
		}
		cancelSend()
	}
	return 0
}

func (r *chatBridgeRunner) setOff(kind string) {
	st := r.get(kind)
	r.mu.Lock()
	defer r.mu.Unlock()
	st.status = chatBridgeStatus{State: "off"}
	st.sig, st.router, st.retry = "", nil, 0
}

func (r *chatBridgeRunner) setState(kind, state string) {
	st := r.get(kind)
	r.mu.Lock()
	st.status.State = state
	r.mu.Unlock()
}

// fail records an error and returns the backoff before the next round:
// doubling from chatBridgeMinRetry up to chatBridgeMaxRetry, reset by the
// next successful poll or a Reload.
func (r *chatBridgeRunner) fail(kind string, err error) time.Duration {
	st := r.get(kind)
	now := time.Now()
	r.mu.Lock()
	first := st.status.LastError != err.Error()
	st.status.State = "error"
	st.status.LastError = err.Error()
	st.status.LastErrorAt = &now
	if st.retry == 0 {
		st.retry = chatBridgeMinRetry
	} else if st.retry < chatBridgeMaxRetry {
		st.retry *= 2
		if st.retry > chatBridgeMaxRetry {
			st.retry = chatBridgeMaxRetry
		}
	}
	wait := st.retry
	r.mu.Unlock()
	if first {
		// Log a changed error once rather than on every retry.
		log.Printf("%s %v (retrying in %s)", chatBridgeLogTag(kind), err, wait)
	}
	return wait
}

func configSig(cfg []byte) string {
	sum := sha256.Sum256(cfg)
	return hex.EncodeToString(sum[:8])
}

// chatBridgeLogTag is the log prefix of a bridge: [tgbot] for Telegram as
// issue #5 proposes, [chatbridge:<kind>] for later transports.
func chatBridgeLogTag(kind string) string {
	if kind == chatbridge.KindTelegram {
		return "[tgbot]"
	}
	return "[chatbridge:" + kind + "]"
}
