package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// ChatBridge is the stored configuration of one inbound chat bridge
// (migrations 0015 and 0016). Config is the decrypted JSON; parsing it is
// left to package chatbridge, like notification channel configs.
type ChatBridge struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Enabled      bool            `json:"enabled"`
	Config       json.RawMessage `json:"config"`
	AllowedChats []string        `json:"allowed_chats"`
	// AllowedUsers are the senders served inside an allowed group chat.
	AllowedUsers []string `json:"allowed_users"`
	PollOffset   int64    `json:"poll_offset"`
	// ConfigVersion grows on every save; SetChatBridgeOffset only writes for
	// the version the poller read.
	ConfigVersion int64     `json:"config_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ErrChatBridgeNotFound is returned when no row exists for a kind.
var ErrChatBridgeNotFound = errors.New("chat bridge not configured")

const chatBridgeCols = `id, kind, enabled, config, allowed_chats, allowed_users, poll_offset, config_version, created_at, updated_at`

func scanChatBridge(sc interface{ Scan(...any) error }) (*ChatBridge, error) {
	var b ChatBridge
	var cfg, allowed, users string
	if err := sc.Scan(&b.ID, &b.Kind, &b.Enabled, &cfg, &allowed, &users, &b.PollOffset, &b.ConfigVersion, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	plain, err := openSecret(colChatBridgeConfig, cfg)
	if err != nil {
		return nil, err
	}
	b.Config = json.RawMessage(plain)
	if err := json.Unmarshal([]byte(allowed), &b.AllowedChats); err != nil {
		return nil, err
	}
	if b.AllowedChats == nil {
		b.AllowedChats = []string{}
	}
	if err := json.Unmarshal([]byte(users), &b.AllowedUsers); err != nil {
		return nil, err
	}
	if b.AllowedUsers == nil {
		b.AllowedUsers = []string{}
	}
	return &b, nil
}

// ChatBridgeByKind returns the bridge of a transport kind.
func (d *DB) ChatBridgeByKind(ctx context.Context, kind string) (*ChatBridge, error) {
	row := d.QueryRowContext(ctx, `SELECT `+chatBridgeCols+` FROM chat_bridges WHERE kind=$1`, kind)
	b, err := scanChatBridge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrChatBridgeNotFound
	}
	return b, err
}

// SaveChatBridge creates or replaces the configuration of b.Kind and bumps its
// config version. resetOffset clears the update cursor in the same statement;
// the caller sets it when the bot token changed, because update ids are per
// bot and the old cursor means nothing to the new one.
func (d *DB) SaveChatBridge(ctx context.Context, b *ChatBridge, resetOffset bool) (*ChatBridge, error) {
	if b.Config == nil {
		b.Config = json.RawMessage(`{}`)
	}
	if b.AllowedChats == nil {
		b.AllowedChats = []string{}
	}
	if b.AllowedUsers == nil {
		b.AllowedUsers = []string{}
	}
	cfg, err := sealSecret(colChatBridgeConfig, string(b.Config))
	if err != nil {
		return nil, err
	}
	allowed, err := json.Marshal(b.AllowedChats)
	if err != nil {
		return nil, err
	}
	users, err := json.Marshal(b.AllowedUsers)
	if err != nil {
		return nil, err
	}
	row := d.QueryRowContext(ctx, `INSERT INTO chat_bridges(kind, enabled, config, allowed_chats, allowed_users)
VALUES($1, $2, $3::jsonb, $4::jsonb, $6::jsonb)
ON CONFLICT (kind) DO UPDATE SET
    enabled=EXCLUDED.enabled, config=EXCLUDED.config, allowed_chats=EXCLUDED.allowed_chats,
    allowed_users=EXCLUDED.allowed_users,
    poll_offset=CASE WHEN $5 THEN 0 ELSE chat_bridges.poll_offset END,
    config_version=chat_bridges.config_version+1, updated_at=NOW()
RETURNING `+chatBridgeCols, b.Kind, b.Enabled, cfg, string(allowed), resetOffset, string(users))
	return scanChatBridge(row)
}

// SetChatBridgeOffset records the transport cursor for the configuration
// version the poller read. It only moves forward, and it does nothing once
// the configuration was saved again, so neither a slow writer nor a poller
// of a replaced token can move the cursor of the current bot. It reports
// whether the cursor was written.
func (d *DB) SetChatBridgeOffset(ctx context.Context, kind string, version, offset int64) (bool, error) {
	res, err := d.ExecContext(ctx, `UPDATE chat_bridges SET poll_offset=$3
WHERE kind=$1 AND config_version=$2 AND poll_offset < $3`, kind, version, offset)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ChatBridgeTask returns the task a chat selected with /task, or "" when it
// selected none (or the task was deleted since).
func (d *DB) ChatBridgeTask(ctx context.Context, kind, chatID string) (string, error) {
	var id int64
	err := d.QueryRowContext(ctx, `SELECT task_id FROM chat_bridge_chats WHERE kind=$1 AND chat_id=$2`, kind, chatID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// SetChatBridgeTask records the task a chat works on. The bridge row of kind
// must exist, and so must the task.
func (d *DB) SetChatBridgeTask(ctx context.Context, kind, chatID string, taskID int64) error {
	_, err := d.ExecContext(ctx, `INSERT INTO chat_bridge_chats(kind, chat_id, task_id) VALUES($1, $2, $3)
ON CONFLICT (kind, chat_id) DO UPDATE SET task_id=EXCLUDED.task_id, updated_at=NOW()`, kind, chatID, taskID)
	return err
}
