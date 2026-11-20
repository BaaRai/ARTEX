-- Operator approval before acquiring third-party code.
-- Opt-in: apply with hardening/apply-intercept-rules.sh (restarting the artex
-- service reloads the rule cache). Idempotent: re-running replaces the
-- [hardening] rules and the skill-visibility rows.
BEGIN;

DELETE FROM intercept_rules WHERE name LIKE '[hardening]%';

-- The patterns match the JSON-string body of the command/url/text/paste field
-- (tool_input is the raw JSON of the call). (?:[^"\\]|\\.)*? walks inside the
-- JSON string without stopping at an escaped quote. RE2 syntax (no lookahead).

INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action) VALUES
('[hardening] Third-party code download from code/exploit hosts', true, 95, 'tool_input', 'regex',
 $re$(?i)"(?:command|url|text|paste)"\s*:\s*"(?:[^"\\]|\\.)*?(?:raw\.githubusercontent\.com|gist\.githubusercontent\.com|gist\.github\.com|codeload\.github\.com|objects\.githubusercontent\.com|github\.com/[^\s"'\\]+/(?:raw|archive|releases/download|blob)/|gitlab\.com/[^\s"'\\]+/-/(?:raw|archive)/|bitbucket\.org/[^\s"'\\]+/(?:raw|get)/|pastebin\.com/(?:raw|dl)|paste\.ee/r/|hastebin|transfer\.sh|exploit-db\.com/(?:raw|download)|packetstormsecurity\.com|dl\.packetstormsecurity)$re$,
 'ask', 'Third-party code: waiting for operator approval. If denied or timed out, do NOT fetch the same code by another route; record the source and take another approach (skill third-party-code-review).', true, 1800, 'deny'),

('[hardening] git clone', true, 95, 'tool_input', 'regex',
 $re$(?i)"(?:command|text|paste)"\s*:\s*"(?:[^"\\]|\\.)*?\bgit\s+(?:-[^\s]+\s+)*(?:clone|submodule\s+update|fetch\s+https?://)$re$,
 'ask', 'Cloning a third-party repository requires operator approval. If denied, take another approach (skill third-party-code-review).', true, 1800, 'deny'),

('[hardening] Package installation', true, 95, 'tool_input', 'regex',
 $re$(?i)"(?:command|text|paste)"\s*:\s*"(?:[^"\\]|\\.)*?(?:\b(?:pip3?|pipx|uv\s+pip|python[0-9.]*\s+-m\s+pip)\s+install\b|\bnpm\s+(?:i|install|exec)\b|\bnpx\s|\bpnpm\s+(?:add|install|dlx)\b|\byarn\s+(?:add|dlx)\b|\bgo\s+(?:install|get)\b|\bgem\s+install\b|\bcargo\s+install\b|\bcomposer\s+(?:require|create-project|global)\b|\bapt(?:-get)?\s+install\b)$re$,
 'ask', 'Installing a package requires operator approval. Prefer the tools already in the image. If denied, take another approach.', true, 1800, 'deny'),

('[hardening] Remote content piped to a shell', true, 96, 'tool_input', 'regex',
 $re$(?i)"(?:command|text|paste)"\s*:\s*"(?:[^"\\]|\\.)*?(?:\|\s*(?:sudo\s+)?(?:ba|z|da|k)?sh\b|(?:ba|z)?sh\s+(?:<|\\u003c)\(\s*(?:curl|wget)|\b(?:python[0-9.]*|perl|ruby|php|node)\s+(?:<|\\u003c)\(\s*(?:curl|wget)|\bbase64\s+(?:-d|--decode)\b[^|]*\|\s*(?:ba|z)?sh\b|\b(?:curl|wget)\b[^|]*\|\s*(?:python[0-9.]*|perl|ruby|php|node)\s*(?:-\s*)?(?:$|[;&)"]|\\n|\\u0026))$re$,
 'ask', 'Executing remote or decoded content directly in an interpreter requires approval. Download to a file, review it (skill third-party-code-review), and request approval.', true, 1800, 'deny');

-- Make the review skill visible to the executor agents. Only enabled=true rows
-- are visible (db/config.go). Adjust the agent names to your deployment.
INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT a.id, 'third-party-code-review', true
FROM agents a
WHERE a.name IN ('Main', 'Worker', 'Auto', 'Penetration Testing', 'Vulnerability Retest')
ON CONFLICT (agent_id, skill_name) DO UPDATE SET enabled = true;

COMMIT;
