package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
)

// fakeBridgeTransport is an in-memory chat app. The runner tests use their
// own kind so they never read or write the real "telegram" row.
type fakeBridgeTransport struct {
	mu       sync.Mutex
	updates  []chatbridge.Update
	offsets  []int64
	sent     map[string][]string
	pollErr  error
	identity int
}

const fakeBridgeKind = "t-fake-bridge"

func (f *fakeBridgeTransport) Kind() string                  { return fakeBridgeKind }
func (f *fakeBridgeTransport) Validate(map[string]any) error { return nil }
func (f *fakeBridgeTransport) SecretKeys() []string          { return []string{"bot_token"} }
func (f *fakeBridgeTransport) DestinationKeys() []string     { return nil }
func (f *fakeBridgeTransport) ValidateChatID(string) error   { return nil }
func (f *fakeBridgeTransport) Identity(context.Context, map[string]any) (chatbridge.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identity++
	return chatbridge.Identity{ID: "1", UserName: "fake_bot"}, nil
}
func (f *fakeBridgeTransport) Poll(_ context.Context, _ map[string]any, offset int64) ([]chatbridge.Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offsets = append(f.offsets, offset)
	if f.pollErr != nil {
		return nil, f.pollErr
	}
	out := f.updates
	f.updates = nil
	return out, nil
}
func (f *fakeBridgeTransport) Send(_ context.Context, _ map[string]any, chatID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sent == nil {
		f.sent = map[string][]string{}
	}
	f.sent[chatID] = append(f.sent[chatID], text)
	return nil
}

func bridgeTestDB(t *testing.T) *db.DB {
	t.Helper()
	skipWithoutDSN(t)
	// NewManager opens the database and loads the secrets key, as at startup.
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM chat_bridges WHERE kind=$1`, fakeBridgeKind)
		m.Close()
	})
	return m.pg
}

func TestChatBridgeRunnerStep(t *testing.T) {
	pg := bridgeTestDB(t)
	ctx := context.Background()
	fake := &fakeBridgeTransport{}
	r := newChatBridgeRunner(pg)
	r.transport = func(kind string) (chatbridge.Transport, bool) { return fake, kind == fakeBridgeKind }

	// Not configured: the bridge is off and sleeps.
	if wait := r.step(ctx, fakeBridgeKind); wait != chatBridgeIdleWait || r.Status(fakeBridgeKind).State != "off" {
		t.Fatalf("unconfigured: wait %v status %+v", wait, r.Status(fakeBridgeKind))
	}

	saved, err := pg.SaveChatBridge(ctx, &db.ChatBridge{
		Kind: fakeBridgeKind, Enabled: true,
		Config:       json.RawMessage(`{"bot_token":"x"}`),
		AllowedChats: []string{"100"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	fake.updates = []chatbridge.Update{
		{ID: 10, ChatID: "100", UserID: "7", Text: "/whoami", Date: now},
		{ID: 11, ChatID: "666", UserID: "9", Text: "/whoami", Date: now},
		{ID: 12, ChatID: "666", UserID: "9", Text: "again", Date: now},
		{ID: 13, ChatID: "100", UserID: "7", Text: "/whoami", Date: now.Add(-time.Hour)},
	}
	if wait := r.step(ctx, fakeBridgeKind); wait != 0 {
		t.Fatalf("online step: wait %v, status %+v", wait, r.Status(fakeBridgeKind))
	}
	st := r.Status(fakeBridgeKind)
	if st.State != "online" || st.Bot == nil || st.Bot.UserName != "fake_bot" {
		t.Fatalf("status = %+v", st)
	}
	if got := fake.sent["100"]; len(got) != 1 || !strings.Contains(got[0], "Chat ID: 100") {
		t.Fatalf("allowed chat replies = %q (the stale /whoami must not be answered)", got)
	}
	if got := fake.sent["666"]; len(got) != 1 || !strings.Contains(got[0], "not allowed") || strings.Contains(got[0], "Your user ID") {
		t.Fatalf("unknown chat replies = %q, want one refusal", got)
	}
	b, _ := pg.ChatBridgeByKind(ctx, fakeBridgeKind)
	if b.PollOffset != 14 {
		t.Fatalf("cursor = %d, want 14", b.PollOffset)
	}

	// The next poll asks from the committed cursor, and the identity is reused.
	r.step(ctx, fakeBridgeKind)
	if last := fake.offsets[len(fake.offsets)-1]; last != 14 || fake.identity != 1 {
		t.Fatalf("second poll offset %d, identity calls %d", last, fake.identity)
	}

	// A save during a poll: the cursor write is refused for the old version
	// and the batch is dropped, not acted on.
	fake.updates = []chatbridge.Update{{ID: 20, ChatID: "100", UserID: "7", Text: "/whoami", Date: time.Now()}}
	fake.pollErr = nil
	r.transport = func(kind string) (chatbridge.Transport, bool) {
		return &saveDuringPoll{fakeBridgeTransport: fake, pg: pg, saved: saved}, true
	}
	before := len(fake.sent["100"])
	r.step(ctx, fakeBridgeKind)
	if len(fake.sent["100"]) != before {
		t.Fatal("a batch polled under a replaced configuration was acted on")
	}

	// An empty allowlist: the bridge runs but serves no chat. Every chat,
	// including one that was allowed a moment ago, gets the refusal with its
	// chat ID and no command runs.
	r.transport = func(kind string) (chatbridge.Transport, bool) { return fake, true }
	if _, err := pg.SaveChatBridge(ctx, &db.ChatBridge{Kind: fakeBridgeKind, Enabled: true, Config: saved.Config}, false); err != nil {
		t.Fatal(err)
	}
	fake.sent = nil
	fake.updates = []chatbridge.Update{
		{ID: 30, ChatID: "100", UserID: "7", Text: "/whoami", Date: time.Now()},
		{ID: 31, ChatID: "777", UserID: "8", Text: "/whoami", Date: time.Now()},
	}
	if wait := r.step(ctx, fakeBridgeKind); wait != 0 || r.Status(fakeBridgeKind).State != "online" {
		t.Fatalf("empty allowlist: wait %v status %+v", wait, r.Status(fakeBridgeKind))
	}
	for _, chat := range []string{"100", "777"} {
		got := fake.sent[chat]
		if len(got) != 1 || !strings.Contains(got[0], "chat ID "+chat) || strings.Contains(got[0], "Your user ID") {
			t.Fatalf("empty allowlist, chat %s replies = %q, want one refusal naming the chat ID", chat, got)
		}
	}

	// Saving the same bot's configuration keeps the refusal memory: a chat
	// refused before the save is not refused a second time.
	r.Reload(fakeBridgeKind)
	fake.updates = []chatbridge.Update{{ID: 32, ChatID: "777", UserID: "8", Text: "hello?", Date: time.Now()}}
	r.step(ctx, fakeBridgeKind)
	if got := fake.sent["777"]; len(got) != 1 {
		t.Fatalf("after a save, refused chat replies = %q, want no second refusal", got)
	}

	// Disabling turns the bridge off.
	if _, err := pg.SaveChatBridge(ctx, &db.ChatBridge{Kind: fakeBridgeKind, Enabled: false, Config: json.RawMessage(`{}`)}, false); err != nil {
		t.Fatal(err)
	}
	if wait := r.step(ctx, fakeBridgeKind); wait != chatBridgeIdleWait || r.Status(fakeBridgeKind).State != "off" {
		t.Fatalf("disabled: wait %v status %+v", wait, r.Status(fakeBridgeKind))
	}
}

// saveDuringPoll saves the row while the poll is "in flight".
type saveDuringPoll struct {
	*fakeBridgeTransport
	pg    *db.DB
	saved *db.ChatBridge
}

func (s *saveDuringPoll) Poll(ctx context.Context, cfg map[string]any, offset int64) ([]chatbridge.Update, error) {
	out, err := s.fakeBridgeTransport.Poll(ctx, cfg, offset)
	if _, serr := s.pg.SaveChatBridge(ctx, &db.ChatBridge{Kind: fakeBridgeKind, Enabled: true, Config: s.saved.Config, AllowedChats: s.saved.AllowedChats}, false); serr != nil {
		return nil, serr
	}
	return out, err
}

func TestChatBridgeRunnerBackoff(t *testing.T) {
	pg := bridgeTestDB(t)
	ctx := context.Background()
	fake := &fakeBridgeTransport{pollErr: &chatbridge.RetryAfterError{After: 9 * time.Second, Msg: "rate limited"}}
	r := newChatBridgeRunner(pg)
	r.transport = func(string) (chatbridge.Transport, bool) { return fake, true }
	if _, err := pg.SaveChatBridge(ctx, &db.ChatBridge{Kind: fakeBridgeKind, Enabled: true, Config: json.RawMessage(`{}`), AllowedChats: []string{"1"}}, false); err != nil {
		t.Fatal(err)
	}
	if wait := r.step(ctx, fakeBridgeKind); wait != 9*time.Second {
		t.Fatalf("retry-after wait = %v", wait)
	}
	fake.pollErr = context.DeadlineExceeded
	w1 := r.step(ctx, fakeBridgeKind)
	w2 := r.step(ctx, fakeBridgeKind)
	if !(w2 > w1) || w2 > chatBridgeMaxRetry {
		t.Fatalf("backoff did not grow: %v then %v", w1, w2)
	}
	if st := r.Status(fakeBridgeKind); st.State != "error" || st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
	// A retry_after from the server is capped.
	fake.pollErr = &chatbridge.RetryAfterError{After: 3 * time.Hour, Msg: "rate limited"}
	if wait := r.step(ctx, fakeBridgeKind); wait != chatBridgeMaxRetry {
		t.Fatalf("huge retry-after wait = %v, want the cap %v", wait, chatBridgeMaxRetry)
	}
	r.Reload(fakeBridgeKind)
	fake.pollErr = nil
	// An empty poll pauses briefly, so an API that ignores the long-poll
	// timeout cannot spin the loop.
	if wait := r.step(ctx, fakeBridgeKind); wait != chatBridgeEmptyPollWait || r.Status(fakeBridgeKind).State != "online" {
		t.Fatalf("after reload: wait %v status %+v", wait, r.Status(fakeBridgeKind))
	}
}

func TestChatBridgeAPI(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.pg
	ctx := context.Background()
	if _, err := pg.ChatBridgeByKind(ctx, chatbridge.KindTelegram); err == nil {
		t.Skip("a telegram bridge is already configured in this test database; not touching it")
	}
	t.Cleanup(func() { _, _ = pg.Exec(`DELETE FROM chat_bridges WHERE kind=$1`, chatbridge.KindTelegram) })

	const token = "123456789:AAapi-test-token-abcdefghij"

	w := request(http.MethodGet, "/api/chat-bridges", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"telegram"`) || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}

	// Enabling with an empty allowlist is allowed: the bridge serves no chat
	// and only answers with the refusal that names the chat ID, which is how
	// the operator finds the ID to add.
	w = request(http.MethodPut, "/api/chat-bridges/telegram", `{"enabled":true,"config":{"bot_token":"`+token+`"}}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"allowed_chats":[]`) {
		t.Fatalf("enable with an empty allowlist = %d %s", w.Code, w.Body)
	}
	// A malformed chat id is refused.
	w = request(http.MethodPut, "/api/chat-bridges/telegram", `{"allowed_chats":["@me"]}`)
	if w.Code != 400 {
		t.Fatalf("bad chat id = %d %s", w.Code, w.Body)
	}
	// Unknown kind.
	if w = request(http.MethodPut, "/api/chat-bridges/carrier-pigeon", `{}`); w.Code != 404 {
		t.Fatalf("unknown kind = %d", w.Code)
	}

	w = request(http.MethodPut, "/api/chat-bridges/telegram", `{"enabled":false,"config":{"bot_token":"`+token+`"},"allowed_chats":["100"," 100 ","-1001"]}`)
	if w.Code != 200 {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "AAapi-test") {
		t.Fatalf("the token leaked into the response: %s", w.Body)
	}
	var dto chatBridgeDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	masked, _ := dto.Config["bot_token"].(string)
	if !dto.Configured || strings.Join(dto.AllowedChats, ",") != "100,-1001" || !strings.HasPrefix(masked, "__masked__") {
		t.Fatalf("dto = %+v", dto)
	}

	// Sending the masked token back keeps the stored one and enables the bridge.
	w = request(http.MethodPut, "/api/chat-bridges/telegram", `{"enabled":true,"config":{"bot_token":"`+masked+`"}}`)
	if w.Code != 200 {
		t.Fatalf("enable = %d %s", w.Code, w.Body)
	}
	b, err := pg.ChatBridgeByKind(ctx, chatbridge.KindTelegram)
	if err != nil || !b.Enabled || !strings.Contains(string(b.Config), token) {
		t.Fatalf("stored = %+v %v", b, err)
	}

	// Changing the API address with the masked token is refused.
	w = request(http.MethodPut, "/api/chat-bridges/telegram", `{"config":{"bot_token":"`+masked+`","base_url":"https://tg.example.com"}}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "bot token again") {
		t.Fatalf("destination change = %d %s", w.Code, w.Body)
	}

	// A new token clears the update cursor.
	if _, err := pg.SetChatBridgeOffset(ctx, chatbridge.KindTelegram, b.ConfigVersion, 500); err != nil {
		t.Fatal(err)
	}
	w = request(http.MethodPut, "/api/chat-bridges/telegram", `{"config":{"bot_token":"987654321:AAanother-token-abcdefghij"}}`)
	if w.Code != 200 {
		t.Fatalf("token change = %d %s", w.Code, w.Body)
	}
	if b, _ = pg.ChatBridgeByKind(ctx, chatbridge.KindTelegram); b.PollOffset != 0 {
		t.Fatalf("cursor after token change = %d", b.PollOffset)
	}
}
