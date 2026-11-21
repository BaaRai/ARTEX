-- A re-confirm clears withdrawn_at. Keep the previous timestamp on the row so
-- the withdrawal is still visible. schema.sql carries the same column for a
-- fresh install. Existing rows get an empty history.
ALTER TABLE rsi_lesson_evidence
    ADD COLUMN IF NOT EXISTS withdrawn_history TIMESTAMPTZ[] NOT NULL DEFAULT '{}';
