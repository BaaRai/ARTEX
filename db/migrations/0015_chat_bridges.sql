-- Inbound chat bridges (issue #5): an operator drives ARTEX from a chat app.
-- One row per transport kind ('telegram' first). Global configuration: not
-- part of a task archive.
--
-- config is sealed at rest by db/secrets.go (it holds the bot token) and
-- masked in API responses. allowed_chats is the allowlist of chat ids, a JSON
-- array of strings. Only listed chats are served; an empty list serves none
-- and only tells each chat its ID. poll_offset is the transport's update
-- cursor, committed before an update is acted on so a restart never replays
-- a command.
--
-- config_version grows on every save. The poller writes its cursor only for
-- the version it read, so a cursor of the old bot can never land on a row
-- whose token was just replaced. updated_at is the time of the last save,
-- set by SaveChatBridge rather than a trigger, so cursor writes leave it alone.
CREATE TABLE IF NOT EXISTS chat_bridges (
    id             BIGSERIAL PRIMARY KEY,
    kind           TEXT NOT NULL UNIQUE,
    enabled        BOOLEAN NOT NULL DEFAULT false,
    config         JSONB NOT NULL DEFAULT '{}'::jsonb,
    allowed_chats  JSONB NOT NULL DEFAULT '[]'::jsonb,
    poll_offset    BIGINT NOT NULL DEFAULT 0,
    config_version BIGINT NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
