-- Chat bridge, part 2 (issue #5): per-user access in groups, and the task each
-- chat works on.
--
-- allowed_users lists the user ids that may use the bot inside an allowed
-- group chat, a JSON array of strings. A group is a shared space: without
-- this list everyone added to an allowed group, now or later, would get the
-- control the bot gives over tasks. A private chat needs no entry, because
-- its chat id is the user. An empty list refuses every group message.
ALTER TABLE chat_bridges ADD COLUMN IF NOT EXISTS allowed_users JSONB NOT NULL DEFAULT '[]'::jsonb;

-- chat_bridge_chats keeps the task a chat selected with /task, so the
-- selection survives a restart. Deleting the task or the bridge removes it.
CREATE TABLE IF NOT EXISTS chat_bridge_chats (
    kind       TEXT NOT NULL REFERENCES chat_bridges(kind) ON DELETE CASCADE,
    chat_id    TEXT NOT NULL,
    task_id    BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (kind, chat_id)
);
CREATE INDEX IF NOT EXISTS chat_bridge_chats_task_idx ON chat_bridge_chats(task_id);
