-- The idempotent schema supplies the DDL; this migration records the feature version.
-- claude-code: profiles served by the local Claude Code CLI (agent/claude_code.go).
ALTER TABLE llm_profiles DROP CONSTRAINT IF EXISTS llm_profiles_format_check;
ALTER TABLE llm_profiles ADD CONSTRAINT llm_profiles_format_check
    CHECK (format IN ('openai','anthropic','openai-responses','ollama','bedrock-gateway','claude-code'));
