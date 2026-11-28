package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Autumn-27/artex/chatbridge"
	"github.com/Autumn-27/artex/db"
)

// HTTP API of the inbound chat bridges. Mounted behind requireAuth like the
// other admin APIs. A bridge is addressed by its transport kind; v1 has one
// bot per deployment, so there is one row per kind.

// chatBridgeDTO is the external form of a bridge. Config is masked: the bot
// token comes back as a masked hint, and sending that hint back unchanged
// keeps the stored token (same convention as notification channels).
type chatBridgeDTO struct {
	Kind         string           `json:"kind"`
	Configured   bool             `json:"configured"`
	Enabled      bool             `json:"enabled"`
	Config       map[string]any   `json:"config"`
	AllowedChats []string         `json:"allowed_chats"`
	AllowedUsers []string         `json:"allowed_users"`
	SecretKeys   []string         `json:"secret_keys"`
	Status       chatBridgeStatus `json:"status"`
	UpdatedAt    *time.Time       `json:"updated_at,omitempty"`
}

type chatBridgeRequest struct {
	Enabled      *bool          `json:"enabled"`
	Config       map[string]any `json:"config"`
	AllowedChats *[]string      `json:"allowed_chats"`
	AllowedUsers *[]string      `json:"allowed_users"`
}

func (s *Server) chatBridgeDTO(kind string, b *db.ChatBridge) chatBridgeDTO {
	tr, _ := chatbridge.Get(kind)
	out := chatBridgeDTO{
		Kind:         kind,
		Config:       map[string]any{},
		AllowedChats: []string{},
		AllowedUsers: []string{},
		SecretKeys:   tr.SecretKeys(),
		Status:       s.chatBridges.Status(kind),
	}
	if b == nil {
		return out
	}
	var cfg map[string]any
	_ = json.Unmarshal(b.Config, &cfg)
	out.Configured = true
	out.Enabled = b.Enabled
	out.Config = chatbridge.MaskConfig(kind, cfg)
	out.AllowedChats = b.AllowedChats
	out.AllowedUsers = b.AllowedUsers
	updated := b.UpdatedAt
	out.UpdatedAt = &updated
	return out
}

// chatBridgeKind reads and checks the {kind} path value.
func chatBridgeKind(w http.ResponseWriter, r *http.Request) (chatbridge.Transport, bool) {
	kind := r.PathValue("kind")
	tr, ok := chatbridge.Get(kind)
	if !ok {
		writeErr(w, 404, fmt.Sprintf("unknown chat bridge %q, available: %s", kind, strings.Join(chatbridge.Kinds(), ", ")))
		return nil, false
	}
	return tr, true
}

func (s *Server) chatBridgeList(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	out := []chatBridgeDTO{}
	for _, kind := range chatbridge.Kinds() {
		b, err := pg.ChatBridgeByKind(r.Context(), kind)
		if err != nil && !errors.Is(err, db.ErrChatBridgeNotFound) {
			writeErr(w, 500, err.Error())
			return
		}
		out = append(out, s.chatBridgeDTO(kind, b))
	}
	writeJSON(w, 200, out)
}

func (s *Server) chatBridgeSave(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	tr, ok := chatBridgeKind(w, r)
	if !ok {
		return
	}
	kind := tr.Kind()
	var req chatBridgeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "request body is not valid JSON: "+err.Error())
		return
	}

	stored, err := pg.ChatBridgeByKind(r.Context(), kind)
	if err != nil && !errors.Is(err, db.ErrChatBridgeNotFound) {
		writeErr(w, 500, err.Error())
		return
	}
	storedCfg := map[string]any{}
	allowed, users := []string{}, []string{}
	enabled := false
	if stored != nil {
		_ = json.Unmarshal(stored.Config, &storedCfg)
		allowed, users = stored.AllowedChats, stored.AllowedUsers
		enabled = stored.Enabled
	}

	cfg := storedCfg
	if req.Config != nil {
		cfg, err = chatbridge.PrepareConfigUpdate(kind, storedCfg, req.Config)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if req.AllowedChats != nil {
		allowed, err = chatbridge.NormalizeAllowedChats(tr, *req.AllowedChats)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if req.AllowedUsers != nil {
		users, err = chatbridge.NormalizeAllowedUsers(tr, *req.AllowedUsers)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// A disabled bridge may hold a partial configuration (the form is saved
	// before the token is at hand); an enabled one must be complete.
	if enabled || cfgHas(cfg, tr.SecretKeys()) {
		if err := tr.Validate(cfg); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Update ids are per bot: a new token makes the old cursor meaningless,
	// so it is cleared in the same statement that stores the token.
	saved, err := pg.SaveChatBridge(r.Context(), &db.ChatBridge{Kind: kind, Enabled: enabled, Config: raw, AllowedChats: allowed, AllowedUsers: users}, secretsChanged(tr, storedCfg, cfg))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.chatBridges.Reload(kind)
	writeJSON(w, 200, s.chatBridgeDTO(kind, saved))
}

// chatBridgeTest asks the chat app which bot the stored configuration
// belongs to. It checks the token without sending any message.
func (s *Server) chatBridgeTest(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	tr, ok := chatBridgeKind(w, r)
	if !ok {
		return
	}
	b, err := pg.ChatBridgeByKind(r.Context(), tr.Kind())
	if errors.Is(err, db.ErrChatBridgeNotFound) {
		writeErr(w, 400, "save the bot token first")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var cfg map[string]any
	_ = json.Unmarshal(b.Config, &cfg)
	if err := tr.Validate(cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	start := time.Now()
	me, err := tr.Identity(r.Context(), cfg)
	if err != nil {
		// Transport errors are already free of the token (see chatbridge.telegramTransport.call).
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "bot": me, "latency_ms": time.Since(start).Milliseconds()})
}

func cfgHas(cfg map[string]any, keys []string) bool {
	for _, k := range keys {
		if s, _ := cfg[k].(string); strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

func secretsChanged(tr chatbridge.Transport, before, after map[string]any) bool {
	for _, k := range tr.SecretKeys() {
		if fmt.Sprint(before[k]) != fmt.Sprint(after[k]) {
			return true
		}
	}
	return false
}
