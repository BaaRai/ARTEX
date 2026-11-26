package chatbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/notify"
)

const testToken = "123456789:AAtest_token-abcdefghijklmnop"

// fakeTelegram records calls and answers from a per-method table.
type fakeTelegram struct {
	mu      sync.Mutex
	calls   []string
	bodies  map[string]map[string]any
	answers map[string]func(w http.ResponseWriter)
}

func newFakeTelegram(t *testing.T) (*fakeTelegram, map[string]any) {
	t.Helper()
	t.Setenv(notify.AllowLocalTargetsEnv, "1") // httptest listens on 127.0.0.1
	f := &fakeTelegram{bodies: map[string]map[string]any{}, answers: map[string]func(http.ResponseWriter){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/bot" + testToken + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":404,"description":"Not Found"}`)
			return
		}
		method := strings.TrimPrefix(r.URL.Path, prefix)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.bodies[method] = body
		answer := f.answers[method]
		f.mu.Unlock()
		if answer == nil {
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		answer(w)
	}))
	t.Cleanup(srv.Close)
	return f, map[string]any{"bot_token": testToken, "base_url": srv.URL}
}

func (f *fakeTelegram) on(method, body string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[method] = func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestTelegramValidate(t *testing.T) {
	tr := telegramTransport{}
	cases := map[string]map[string]any{
		"missing token":   {},
		"malformed token": {"bot_token": "not-a-token"},
		"path injection":  {"bot_token": "123:abcdefghijklmnopqrstuvwxyz/../x"},
		"bad base url":    {"bot_token": testToken, "base_url": "ftp://example.com"},
	}
	for name, cfg := range cases {
		if err := tr.Validate(cfg); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if err := tr.Validate(map[string]any{"bot_token": testToken}); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	if err := tr.Validate(map[string]any{"bot_token": testToken, "base_url": "https://bot-api.example.com"}); err != nil {
		t.Fatalf("https base_url refused: %v", err)
	}
}

func TestTelegramValidateRequiresHTTPS(t *testing.T) {
	tr := telegramTransport{}
	cfg := map[string]any{"bot_token": testToken, "base_url": "http://bot-api.example.com"}
	t.Setenv(notify.AllowLocalTargetsEnv, "")
	if err := tr.Validate(cfg); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http base_url: err = %v, want an https error", err)
	}
	t.Setenv(notify.AllowLocalTargetsEnv, "1")
	if err := tr.Validate(cfg); err != nil {
		t.Fatalf("plain http with the local opt-in: %v", err)
	}
}

func TestTruncateUTF16(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 5, "hello"},
		{"hello!", 5, "hell…"},
		{"éééééé", 5, "éééé…"},
		// An emoji is two UTF-16 units and is never split.
		{"ab😀cd", 5, "ab😀…"},
		{"ab😀cd", 4, "ab…"},
	}
	for _, c := range cases {
		if got := truncateUTF16(c.in, c.n); got != c.want {
			t.Errorf("truncateUTF16(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
		if got := truncateUTF16(c.in, c.n); utf16Len(got) > c.n {
			t.Errorf("truncateUTF16(%q, %d) is %d units", c.in, c.n, utf16Len(got))
		}
	}
}

func TestTelegramPollParsesAndSendsOffset(t *testing.T) {
	f, cfg := newFakeTelegram(t)
	f.on("getUpdates", `{"ok":true,"result":[
		{"update_id":41,"message":{"date":1763726400,"chat":{"id":-100123,"type":"group"},"from":{"id":7,"is_bot":false,"username":"op"},"text":"/help"}},
		{"update_id":42,"my_chat_member":{}}
	]}`, 200)
	got, err := telegramTransport{}.Poll(context.Background(), cfg, 41)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 updates (the second only advances the cursor), got %d", len(got))
	}
	u := got[0]
	if u.ID != 41 || u.ChatID != "-100123" || u.ChatType != "group" || u.UserID != "7" || u.UserName != "op" || u.Text != "/help" || u.FromBot {
		t.Fatalf("update = %+v", u)
	}
	if !u.Date.Equal(time.Unix(1763726400, 0)) {
		t.Fatalf("date = %v", u.Date)
	}
	if got[1].ID != 42 || got[1].Text != "" {
		t.Fatalf("non-message update = %+v", got[1])
	}
	body := f.bodies["getUpdates"]
	if body["offset"] != float64(41) || body["timeout"] != float64(25) {
		t.Fatalf("getUpdates body = %v", body)
	}
	if au, _ := body["allowed_updates"].([]any); len(au) != 1 || au[0] != "message" {
		t.Fatalf("allowed_updates = %v", body["allowed_updates"])
	}
}

func TestTelegramSendIsPlainTextAndTruncated(t *testing.T) {
	f, cfg := newFakeTelegram(t)
	long := strings.Repeat("é", telegramTextLimit+50)
	if err := (telegramTransport{}).Send(context.Background(), cfg, "100", long); err != nil {
		t.Fatal(err)
	}
	body := f.bodies["sendMessage"]
	if _, ok := body["parse_mode"]; ok {
		t.Fatal("replies must be sent as plain text")
	}
	text, _ := body["text"].(string)
	if n := utf16Len(text); n != telegramTextLimit || !strings.HasSuffix(text, "…") {
		t.Fatalf("text has %d UTF-16 units, suffix %q", n, text[len(text)-3:])
	}

	// Emoji count twice toward Telegram's limit.
	if err := (telegramTransport{}).Send(context.Background(), cfg, "100", strings.Repeat("😀", telegramTextLimit)); err != nil {
		t.Fatal(err)
	}
	text, _ = f.bodies["sendMessage"]["text"].(string)
	if n := utf16Len(text); n > telegramTextLimit || !strings.HasSuffix(text, "…") {
		t.Fatalf("emoji text has %d UTF-16 units, over the limit %d", n, telegramTextLimit)
	}
	if body["chat_id"] != "100" {
		t.Fatalf("chat_id = %v", body["chat_id"])
	}
	if err := (telegramTransport{}).Send(context.Background(), cfg, "@someone", "x"); err == nil {
		t.Fatal("a non-numeric chat id must be refused")
	}
}

func TestTelegramIdentity(t *testing.T) {
	f, cfg := newFakeTelegram(t)
	f.on("getMe", `{"ok":true,"result":{"id":123456789,"is_bot":true,"username":"artex_bot","first_name":"ARTEX"}}`, 200)
	id, err := telegramTransport{}.Identity(context.Background(), cfg)
	if err != nil || id.UserName != "artex_bot" || id.ID != "123456789" || id.Name != "ARTEX" {
		t.Fatalf("identity = %+v, %v", id, err)
	}
}

func TestTelegramErrorsNeverCarryTheToken(t *testing.T) {
	f, cfg := newFakeTelegram(t)
	f.on("getMe", `{"ok":false,"error_code":401,"description":"Unauthorized"}`, 401)
	_, err := telegramTransport{}.Identity(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "token was rejected") {
		t.Fatalf("401: %v", err)
	}
	f.on("getUpdates", `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`, 409)
	_, err = telegramTransport{}.Poll(context.Background(), cfg, 0)
	if err == nil || !strings.Contains(err.Error(), "another poller") {
		t.Fatalf("409: %v", err)
	}
	f.on("sendMessage", `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`, 429)
	err = telegramTransport{}.Send(context.Background(), cfg, "1", "x")
	var ra *RetryAfterError
	if !errors.As(err, &ra) || ra.After != 7*time.Second {
		t.Fatalf("429: %v", err)
	}

	// A transport failure: nothing listens on this address. The error must
	// name the host but never the path that carries the token.
	dead := map[string]any{"bot_token": testToken, "base_url": "http://127.0.0.1:1"}
	_, err = telegramTransport{}.Identity(context.Background(), dead)
	if err == nil {
		t.Fatal("want a dial error")
	}
	for _, e := range []error{err} {
		if strings.Contains(e.Error(), "AAtest_token") || strings.Contains(e.Error(), "/bot") {
			t.Fatalf("error leaks the token: %v", e)
		}
	}
}

func TestTelegramPollHonoursCancel(t *testing.T) {
	f, cfg := newFakeTelegram(t)
	release := make(chan struct{})
	f.mu.Lock()
	f.answers["getUpdates"] = func(w http.ResponseWriter) { <-release }
	f.mu.Unlock()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := telegramTransport{}.Poll(ctx, cfg, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel did not interrupt the long poll")
	}
}

func TestConfigMaskAndPrepare(t *testing.T) {
	stored := map[string]any{"bot_token": testToken}
	masked := MaskConfig(KindTelegram, stored)
	if s, _ := masked["bot_token"].(string); !notify.IsMasked(s) || strings.Contains(s, "AAtest") {
		t.Fatalf("masked = %v", masked)
	}
	// Masked value sent back keeps the stored token.
	got, err := PrepareConfigUpdate(KindTelegram, stored, map[string]any{"bot_token": masked["bot_token"]})
	if err != nil || got["bot_token"] != testToken {
		t.Fatalf("keep: %v %v", got, err)
	}
	// Changing base_url with a masked token is refused.
	if _, err := PrepareConfigUpdate(KindTelegram, stored, map[string]any{"bot_token": masked["bot_token"], "base_url": "https://tg.example.com"}); err == nil {
		t.Fatal("destination change with a masked token must be refused")
	}
	// ...and accepted with the token in clear.
	got, err = PrepareConfigUpdate(KindTelegram, stored, map[string]any{"bot_token": testToken, "base_url": "https://tg.example.com"})
	if err != nil || got["base_url"] != "https://tg.example.com" {
		t.Fatalf("destination change: %v %v", got, err)
	}
	if m := MaskConfig("nope", stored); len(m) != 0 {
		t.Fatalf("unknown kind must mask to empty, got %v", m)
	}
}
