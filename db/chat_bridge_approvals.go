package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Approval pushes of the chat bridges (migration 0019). Every function is
// scoped by the bridge kind, and the state changes are single statements, so
// a button press and the server closing the same message cannot both win.

// ChatBridgePush is one pending approval request to push to one chat: the
// pending request belongs to the task the chat selected.
type ChatBridgePush struct {
	ChatID         string
	ChatType       string
	PendingID      int64
	TaskID         string
	AgentName      string
	ToolName       string
	ToolInput      json.RawMessage
	Reason         string
	DecisionSource string
	TargetKey      string // host:port for a target request, "" otherwise
	CreatedAt      time.Time
}

// ChatBridgeApprovalsToPush lists pending requests of the tasks chats of kind
// selected that were not pushed to that chat yet, oldest first. Only chats in
// allowedChats are considered, and shared chats (any type but private) only
// when groups is set, so chats that cannot act on a push never fill the
// batch.
func (d *DB) ChatBridgeApprovalsToPush(ctx context.Context, kind string, allowedChats []string, groups bool, limit int) ([]ChatBridgePush, error) {
	if len(allowedChats) == 0 {
		return nil, nil
	}
	chats, err := json.Marshal(allowedChats)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `
SELECT c.chat_id, c.chat_type, p.id, p.task_id, p.agent_name, p.tool_name, p.tool_input, p.reason,
       COALESCE(p.decision_source,''), COALESCE(p.target_key,''), p.created_at
FROM intercept_pending p
JOIN chat_bridge_chats c ON c.kind=$1 AND p.task_id = c.task_id::text
WHERE p.status='pending'
  AND c.chat_id IN (SELECT jsonb_array_elements_text($3::jsonb))
  AND (c.chat_type='private' OR $4)
  AND NOT EXISTS (SELECT 1 FROM chat_bridge_approvals a WHERE a.kind=c.kind AND a.chat_id=c.chat_id AND a.pending_id=p.id)
ORDER BY p.id, c.chat_id
LIMIT $2`, kind, limit, string(chats), groups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatBridgePush
	for rows.Next() {
		var p ChatBridgePush
		var input []byte
		if err := rows.Scan(&p.ChatID, &p.ChatType, &p.PendingID, &p.TaskID, &p.AgentName, &p.ToolName, &input, &p.Reason, &p.DecisionSource, &p.TargetKey, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.ToolInput = json.RawMessage(input)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ChatBridgeApproval is one pushed approval message.
type ChatBridgeApproval struct {
	ID          int64
	Kind        string
	ChatID      string
	PendingID   int64
	MessageID   string
	MessageText string
	ExpiresAt   time.Time
	// PendingStatus and DecidedBy are filled by ChatBridgeApprovalsToClose.
	PendingStatus string
	DecidedBy     string
}

// CreateChatBridgeApproval records a push before it is sent, so a second
// pusher (or a restart) cannot send the same request to the same chat twice.
// created is false when the row already exists.
func (d *DB) CreateChatBridgeApproval(ctx context.Context, kind, chatID string, pendingID int64, nonceHash, text string, expires time.Time) (id int64, created bool, err error) {
	err = d.QueryRowContext(ctx, `
INSERT INTO chat_bridge_approvals(kind, chat_id, pending_id, nonce_hash, message_text, expires_at)
VALUES($1, $2, $3, $4, $5, $6)
ON CONFLICT (kind, chat_id, pending_id) DO NOTHING
RETURNING id`, kind, chatID, pendingID, nonceHash, text, expires).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// SetChatBridgeApprovalMessage stores the chat app's id of the sent message.
func (d *DB) SetChatBridgeApprovalMessage(ctx context.Context, id int64, messageID string) error {
	_, err := d.ExecContext(ctx, `UPDATE chat_bridge_approvals SET message_id=$2 WHERE id=$1`, id, messageID)
	return err
}

// UseChatBridgeApproval spends the nonce of a button press. It succeeds once,
// for the chat and message the buttons were sent to, before they expire, and
// closes the buttons in the same statement. It returns nil when the press
// does not count: unknown nonce, another chat or message, expired, or used.
func (d *DB) UseChatBridgeApproval(ctx context.Context, kind, chatID, messageID, nonceHash, by string) (*ChatBridgeApproval, error) {
	var a ChatBridgeApproval
	err := d.QueryRowContext(ctx, `
UPDATE chat_bridge_approvals SET closed_at=NOW(), closed_by=$5
WHERE kind=$1 AND chat_id=$2 AND message_id=$3 AND message_id<>'' AND nonce_hash=$4
  AND closed_at IS NULL AND expires_at > NOW()
RETURNING id, kind, chat_id, pending_id, message_id, message_text, expires_at`,
		kind, chatID, messageID, nonceHash, by).Scan(&a.ID, &a.Kind, &a.ChatID, &a.PendingID, &a.MessageID, &a.MessageText, &a.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ChatBridgeApprovalsToClose lists open pushes whose buttons should stop
// working: the request was decided elsewhere (in ARTEX, by a rule timeout or
// in another chat), the buttons expired, or the send never completed.
func (d *DB) ChatBridgeApprovalsToClose(ctx context.Context, kind string, limit int) ([]ChatBridgeApproval, error) {
	rows, err := d.QueryContext(ctx, `
SELECT a.id, a.kind, a.chat_id, a.pending_id, a.message_id, a.message_text, a.expires_at, p.status, COALESCE(p.decided_by,'')
FROM chat_bridge_approvals a
JOIN intercept_pending p ON p.id = a.pending_id
WHERE a.kind=$1 AND a.closed_at IS NULL
  AND (p.status <> 'pending' OR a.expires_at <= NOW()
       OR (a.message_id='' AND a.created_at < NOW() - INTERVAL '2 minutes'))
ORDER BY a.id
LIMIT $2`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatBridgeApproval
	for rows.Next() {
		var a ChatBridgeApproval
		if err := rows.Scan(&a.ID, &a.Kind, &a.ChatID, &a.PendingID, &a.MessageID, &a.MessageText, &a.ExpiresAt, &a.PendingStatus, &a.DecidedBy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CloseChatBridgeApproval closes the buttons of a push. It reports whether
// this call closed them; false means a press or another closer was first.
func (d *DB) CloseChatBridgeApproval(ctx context.Context, id int64, by string) (bool, error) {
	res, err := d.ExecContext(ctx, `UPDATE chat_bridge_approvals SET closed_at=NOW(), closed_by=$2 WHERE id=$1 AND closed_at IS NULL`, id, by)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
