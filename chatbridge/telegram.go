package chatbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Autumn-27/artex/notify"
)

// KindTelegram is the Telegram Bot API transport.
const KindTelegram = "telegram"

const (
	telegramDefaultBase = "https://api.telegram.org"
	// telegramPollWindow is the long-poll timeout sent to getUpdates. The
	// client timeout must stay above it, or every idle poll ends as an error.
	telegramPollWindow   = 25 * time.Second
	telegramClientWindow = telegramPollWindow + 15*time.Second
	// telegramPollLimit caps one getUpdates batch.
	telegramPollLimit = 50
	// telegramTextLimit is where a reply is cut, in UTF-16 code units: the Bot
	// API limit is 4096 characters counted in UTF-16, and the cut is a little
	// earlier to leave a margin.
	telegramTextLimit = 4000
	// telegramBodyLimit caps a response body; 50 text updates fit well inside.
	telegramBodyLimit = 1 << 20
)

// telegramClient is shared by every Telegram call of the bridge. It carries
// notify's dial guard and refuses cross-host redirects: the bot token is in
// the URL path, so following a redirect to another host would hand it over.
var telegramClient = notify.NewGuardedClient(telegramClientWindow)

// A bot token is "<bot id>:<secret>". Rejecting anything else keeps a pasted
// value with a slash or whitespace from changing the request path.
var telegramTokenRe = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{20,100}$`)

// Chat ids are signed integers; groups and channels are negative.
var telegramChatIDRe = regexp.MustCompile(`^-?[0-9]{1,20}$`)

type telegramTransport struct{}

func (telegramTransport) Kind() string { return KindTelegram }

// The bot token is the whole credential. base_url decides where it is sent, so
// changing base_url requires entering the token again.
func (telegramTransport) SecretKeys() []string      { return []string{"bot_token"} }
func (telegramTransport) DestinationKeys() []string { return []string{"base_url"} }

func (telegramTransport) Validate(cfg map[string]any) error {
	token := cfgString(cfg, "bot_token")
	if token == "" {
		return errors.New("missing bot token")
	}
	if !telegramTokenRe.MatchString(token) {
		return errors.New(`the bot token should look like "123456789:AA…" as BotFather shows it`)
	}
	if base := cfgString(cfg, "base_url"); base != "" {
		if err := notify.ValidateEndpointURL(base); err != nil {
			return fmt.Errorf("invalid API URL: %w", err)
		}
		// The token is in the URL path, and the responses are the updates the
		// bridge acts on: over plain http both could be read or forged on the
		// network path. http is only for a local test server, behind the same
		// opt-in as other local targets.
		if u, err := url.Parse(base); err == nil && strings.EqualFold(u.Scheme, "http") && !notify.AllowLocalTargets() {
			return fmt.Errorf("the API URL must use https (http is allowed only with %s=1, for a local test server)", notify.AllowLocalTargetsEnv)
		}
	}
	return nil
}

// User ids are positive integers.
var telegramUserIDRe = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func (telegramTransport) ValidateUserID(id string) error {
	if !telegramUserIDRe.MatchString(id) {
		return errors.New("a Telegram user id is a positive number, for example 123456789 (send /whoami to the bot to see yours)")
	}
	return nil
}

func (telegramTransport) ValidateChatID(id string) error {
	if !telegramChatIDRe.MatchString(id) {
		return errors.New("a Telegram chat id is a number, for example 123456789 or -1001234567890")
	}
	return nil
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	UserName  string `json:"username"`
	FirstName string `json:"first_name"`
}

type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Date int64 `json:"date"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From *telegramUser `json:"from"`
		Text string        `json:"text"`
	} `json:"message"`
}

type telegramResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (t telegramTransport) Identity(ctx context.Context, cfg map[string]any) (Identity, error) {
	var me telegramUser
	if err := t.call(ctx, cfg, "getMe", nil, &me); err != nil {
		return Identity{}, err
	}
	return Identity{ID: strconv.FormatInt(me.ID, 10), UserName: me.UserName, Name: me.FirstName}, nil
}

func (t telegramTransport) Poll(ctx context.Context, cfg map[string]any, offset int64) ([]Update, error) {
	req := map[string]any{
		"timeout":         int(telegramPollWindow / time.Second),
		"limit":           telegramPollLimit,
		"allowed_updates": []string{"message"},
	}
	if offset > 0 {
		req["offset"] = offset
	}
	var raw []telegramUpdate
	if err := t.call(ctx, cfg, "getUpdates", req, &raw); err != nil {
		return nil, err
	}
	out := make([]Update, 0, len(raw))
	for _, r := range raw {
		u := Update{ID: r.UpdateID}
		if m := r.Message; m != nil {
			u.ChatID = strconv.FormatInt(m.Chat.ID, 10)
			u.ChatType = m.Chat.Type
			u.Text = m.Text
			u.Date = time.Unix(m.Date, 0)
			if m.From != nil {
				u.UserID = strconv.FormatInt(m.From.ID, 10)
				u.UserName = m.From.UserName
				u.FromBot = m.From.IsBot
			}
		}
		// Updates without a message still advance the cursor; the Router
		// ignores them because they carry no text.
		out = append(out, u)
	}
	return out, nil
}

func (t telegramTransport) Send(ctx context.Context, cfg map[string]any, chatID, text string) error {
	if err := t.ValidateChatID(chatID); err != nil {
		return err
	}
	body := map[string]any{
		"chat_id": chatID,
		"text":    truncateUTF16(text, telegramTextLimit),
		// Plain text on purpose: no parse_mode, so agent output can never be
		// rejected for a stray markup character or render as a link.
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	return t.call(ctx, cfg, "sendMessage", body, nil)
}

// call performs one Bot API method. Every error it returns is safe to log and
// to show in the UI: it never contains the request URL, and so never the token.
func (t telegramTransport) call(ctx context.Context, cfg map[string]any, method string, payload, result any) error {
	if err := t.Validate(cfg); err != nil {
		return err
	}
	base := strings.TrimRight(cfgString(cfg, "base_url"), "/")
	if base == "" {
		base = telegramDefaultBase
	}
	endpoint := base + "/bot" + cfgString(cfg, "bot_token") + "/" + method

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("telegram %s: build request: %w", method, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("telegram %s: invalid API address", method)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := telegramClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("telegram %s: %s", method, notify.RedactTransportError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, telegramBodyLimit))
	if err != nil {
		return fmt.Errorf("telegram %s: read response: %w", method, err)
	}

	var env telegramResponse[json.RawMessage]
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d, response is not Bot API JSON: %s", method, resp.StatusCode, notify.OneLine(string(raw), 120))
	}
	if !env.OK {
		return telegramError(method, resp.StatusCode, env)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, result); err != nil {
		return fmt.Errorf("telegram %s: unexpected result: %w", method, err)
	}
	return nil
}

// RetryAfterError asks the caller to wait before the next call.
type RetryAfterError struct {
	After time.Duration
	Msg   string
}

func (e *RetryAfterError) Error() string { return e.Msg }

func telegramError(method string, status int, env telegramResponse[json.RawMessage]) error {
	code := env.ErrorCode
	if code == 0 {
		code = status
	}
	desc := notify.OneLine(env.Description, 200)
	switch code {
	case http.StatusUnauthorized, http.StatusNotFound:
		return fmt.Errorf("telegram %s: the bot token was rejected (%d %s)", method, code, desc)
	case http.StatusConflict:
		return fmt.Errorf("telegram %s: another poller or a webhook is using this bot token (%s); give ARTEX its own bot or remove the webhook", method, desc)
	case http.StatusTooManyRequests:
		after := 5 * time.Second
		if env.Parameters != nil && env.Parameters.RetryAfter > 0 {
			after = time.Duration(env.Parameters.RetryAfter) * time.Second
		}
		return &RetryAfterError{After: after, Msg: fmt.Sprintf("telegram %s: rate limited, retry after %s", method, after)}
	}
	return fmt.Errorf("telegram %s: %d %s", method, code, desc)
}

func cfgString(cfg map[string]any, key string) string {
	s, _ := cfg[key].(string)
	return strings.TrimSpace(s)
}

// truncateUTF16 cuts s to at most n UTF-16 code units, marking the cut.
// Telegram counts the message limit in UTF-16 units, so a character outside
// the Basic Multilingual Plane (most emoji) counts twice. The cut never
// splits a character.
func truncateUTF16(s string, n int) string {
	if utf16Len(s) <= n {
		return s
	}
	used := 0
	for i, r := range s {
		// range yields U+FFFD for invalid UTF-8, so RuneLen is 1 or 2 here.
		w := utf16.RuneLen(r)
		if used+w > n-1 { // n-1: one unit is kept for the ellipsis
			return s[:i] + "…"
		}
		used += w
	}
	return s
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
