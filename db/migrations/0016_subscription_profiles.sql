-- Enable keyless subscription-backed profiles. Credentials remain in the
-- respective local login store and never enter llm_profiles.api_key.
ALTER TABLE llm_profiles DROP CONSTRAINT IF EXISTS llm_profiles_format_check;
ALTER TABLE llm_profiles ADD CONSTRAINT llm_profiles_format_check
    CHECK (format IN ('openai','anthropic','openai-responses','ollama','bedrock-gateway','chatgpt-plan','claude-code'));
