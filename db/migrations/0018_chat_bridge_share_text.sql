-- Chat bridge (issue #5): what may leave ARTEX as free-form text.
--
-- share_text off (the default) keeps every message the bot sends to fixed
-- wording, titles, statuses, counts and links: a main-agent answer is
-- announced with a link instead of relayed, and an unnamed task is not shown
-- by its description. On, the bot also sends filtered excerpts of free-form
-- text. The filter masks common credential shapes but cannot know every one,
-- which is why sending them is a per-bridge opt-in.
ALTER TABLE chat_bridges ADD COLUMN IF NOT EXISTS share_text BOOLEAN NOT NULL DEFAULT false;
