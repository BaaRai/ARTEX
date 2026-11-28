-- Chat bridge, part 3 (issue #5): approval requests pushed to chats with
-- Allow and Block buttons.
--
-- One row per (chat, pending request). nonce_hash is the SHA-256 of the
-- one-time nonce carried in the buttons' callback data. The nonce itself is
-- never stored, so reading this table does not give a usable button press. A
-- press counts once, only from the chat and message it was sent to, and only
-- before expires_at. closed_at records when the buttons stopped working, and
-- closed_by why: the user who pressed, "decided" when ARTEX decided first,
-- "expired", or "send-failed".
CREATE TABLE IF NOT EXISTS chat_bridge_approvals (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL REFERENCES chat_bridges(kind) ON DELETE CASCADE,
    chat_id      TEXT NOT NULL,
    pending_id   BIGINT NOT NULL REFERENCES intercept_pending(id) ON DELETE CASCADE,
    nonce_hash   TEXT NOT NULL UNIQUE,
    message_id   TEXT NOT NULL DEFAULT '',
    message_text TEXT NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ NOT NULL,
    closed_at    TIMESTAMPTZ,
    closed_by    TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (kind, chat_id, pending_id)
);
CREATE INDEX IF NOT EXISTS chat_bridge_approvals_open_idx ON chat_bridge_approvals(kind) WHERE closed_at IS NULL;

-- The chat type decides whether a chat can act on a push: a group needs at
-- least one allowed user to press the buttons. Rows written before this
-- migration have no type and count as groups.
ALTER TABLE chat_bridge_chats ADD COLUMN IF NOT EXISTS chat_type TEXT NOT NULL DEFAULT '';
