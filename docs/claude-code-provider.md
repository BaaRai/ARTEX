# Claude Code subscription provider (experimental)

ARTEX can use the unmodified Claude Code CLI as a local provider with the
operator's own `claude.ai` subscription login. Claude Code owns sign-in and
credential storage; ARTEX does not read Claude credential files or tokens.

1. Install a current Claude Code CLI (version 2.1.248 or newer) on the same
   machine and under the same OS account that runs ARTEX.
2. Run `claude auth login` in a terminal and choose the Claude subscription
   account. `claude auth status` should report `loggedIn: true` and
   `authMethod: "claude.ai"`. ARTEX does not accept `api_key`, `third_party`, or
   `oauth_token` for this profile.
3. Add a `claude-code` LLM profile in ARTEX. The model can be a CLI alias such
   as `sonnet` or `opus`, or a full Claude model ID. No API key or base URL is
   needed. If `claude` is not on ARTEX's `PATH`, set `ARTEX_CLAUDE_CODE_BIN` to
   the executable path before starting ARTEX.
4. Put the `claude-code` and ChatGPT plan profiles in a pool in your desired
   order. ARTEX can try the next profile when the first returns a recognized
   subscription usage-limit error before emitting a reply.

The adapter runs `claude -p` with restricted tools, no MCP servers, a neutral
temporary working directory, and no CLI session persistence. It passes ARTEX's
conversation through stdin and asks Claude Code for one structured action:
either a reply or a request for one ARTEX tool. ARTEX executes any requested
tool and calls the provider again with the result. The adapter removes
inherited API-key and third-party provider environment overrides from the
Claude child process so they cannot silently supersede the subscription login.

## Limits

- This bridge uses Claude Code's structured output to select an ARTEX tool. It
  does not use the native MCP tool relay in Hermes; tool choice may be less
  reliable, especially with many or complex tool schemas.
- The CLI finishes each action before ARTEX receives it. ARTEX's stream events
  are therefore buffered, and there is one tool request per CLI invocation.
- Claude Code's own prompt/session handling may differ from ARTEX's native API
  providers. The adapter does not honor ARTEX HTTP proxy, max-token, thinking,
  reasoning-effort, or request-rate settings.
- Subscription usage and limits are governed by Anthropic. The adapter
  recognizes explicit session, weekly, or usage-limit errors for profile
  fallback; transient rate limits remain ordinary provider errors. If paid
  extra usage credits are enabled, Claude Code may continue on those credits
  instead of returning a limit error, so fallback would not occur at the end
  of the plan's included allowance.

For CLI behavior, see Anthropic's [CLI reference](https://code.claude.com/docs/en/cli-reference),
[programmatic use](https://code.claude.com/docs/en/headless), and
[authentication terms](https://code.claude.com/docs/en/legal-and-compliance).
Anthropic describes conditions for running its unmodified binary with an end
user's own login in a product; check those terms before distributing a hosted
integration.
