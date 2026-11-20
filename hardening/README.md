# hardening — operator approval for third-party code

Opt-in intercept rules that pause the agent and ask the operator before it
**acquires** third-party code — downloads from code/exploit hosts, `git clone`,
package installs, and remote content piped straight into a shell — plus a
`third-party-code-review` skill that tells the agent to hash, blocklist-check,
read and summarize the code before running it.

These rules are **not** seeded by the application; nothing changes until you
apply them. Code the agent writes itself is not gated — only the point where
untrusted code enters.

Apply (idempotent; restarts the `artex` service so the interceptor reloads its
rule cache):

    ./hardening/apply-intercept-rules.sh

Requests show up under Intercept → pending in the console. With no decision in
30 minutes the request is denied (the agent gets the refusal and takes another
path). Change the timeout or action in the console or in `intercept-rules.sql`.
