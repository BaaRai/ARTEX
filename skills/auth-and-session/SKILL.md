---
name: auth-and-session
description: "Invoke this skill for authentication and session flaws outside the API-token layer — user enumeration, default and weak credentials, password-reset poisoning, registration abuse, MFA weaknesses, session fixation and rotation, CSRF, and remember-me tokens. Browser-session mechanics only, JWT and OAuth belong to api-security."
---
# Authentication and Session Testing

The layer that turns a read-only surface into an account. Under an **authorized** engagement only. Two standing rules: use accounts created for the test, and never spend a lockout budget you cannot restore.

Access-control proof needs two identities; `finding-validation` owns that check, this skill produces the halves.

## Hard gates

1. **Lockout budget first.** Read the policy; if silent, assume 5 failures per account before a 15-minute lockout. `hydra -t 1 -W 1` and a 10k list only on a dedicated account.
2. Registration is a write: confirm the program allows test registrations.
3. Password changes only on test accounts.
4. Reset-email volume: one request per candidate technique, not per attempt.
5. Every confirmed flaw gets a replayable request pair, including the clean baseline.

## Phase 1: user enumeration

- Login and registration error text/timing: "invalid user" vs "invalid password", differing status or latency.
- Reset flow: response for known vs unknown address.
- API variants (`/api/users`, `/api/auth/check`, GraphQL `userExists`).
- Confirm with a definitely-absent address as the control.
- A leak is a finding only with the control pair; note it as a prerequisite for spraying.

## Phase 2: default and weak credentials

- Product defaults first: `admin/admin`, `admin/password`, vendor install pages, `admin:admin@<host>` in docs.
- One spray across many users with a fixed password is cheaper than one user with many; state which direction was used.
- Order: `/usr/share/wordlists/passwords/500-worst-passwords.txt` against `admin` and named users, then `10k-most-common.txt` against `admin` only.
- Basic-auth endpoints: `hydra -l admin -P <list> -t 1 -W 1 <host> http-get /<path>`.
- Form login: `hydra -l <user> -P <list> -t 1 -W 1 <host> http-post-form "/login:user=^USER^&pass=^PASS^:F=Invalid"`.
- Stop the run when a valid pair is found; record whether the account was already known.

## Phase 3: password reset

- Token in URL: predictability (sequential, timestamp, short), reuse after success, expiry, invalidation of other sessions.
- Host-header poisoning: send `X-Forwarded-Host`/`Host` and inspect the link host in the email.
- Token leak: `Referer` to third parties, token in logs, token echoed in the response.
- Reset without token, or token bound to no user (change `user=` while keeping the token).
- Email change without re-auth.

## Phase 4: registration and account lifecycle

- Duplicate registration (case, whitespace, unicode homoglyph) creating two accounts for one identity.
- Email verification bypass: register unverified, access features anyway; re-register a victim's address.
- Role chosen at registration (`role=admin`, hidden `is_staff`).
- Account deletion and re-registration reusing the old identity.

## Phase 5: MFA

- Code reuse, code valid for another user, missing rate limit on the second factor.
- Response manipulation: submit empty code and flip `"mfa":false` in the JSON, or skip the step endpoint.
- Backup/recovery codes weak or reusable.
- Disable-MFA endpoint without password confirmation.

## Phase 6: session mechanics

- Cookie flags: `HttpOnly`, `Secure`, `SameSite`; session id entropy from two samples.
- Fixation: set a known cookie, log in, check whether it survived.
- Rotation: session id must change at login and at privilege change; old id must die.
- Logout server-side invalidation: replay the old cookie.
- Parallel session policy; concurrent login from two clients.
- Remember-me token: shape, expiry, reuse after logout and after password change.

## Phase 7: CSRF

- Every state-changing form and JSON endpoint; a token counts only if validated server-side.
- Remove the token, reuse a token from another session, keep the token and change the value.
- Method/Content-Type swaps: `POST` to `GET`, form to `text/plain`, JSON to form.
- `SameSite` absent or `None` without `Secure` is the enabler; state the browser requirement.
- Prove with a local auto-submitting HTML page in the browser, and record the resulting state change.

## Phase 8: login-adjacent logic

- "Remember me" granting more than session scope.
- Account switching / linked OAuth login without email verification.
- Password policy bypass through the reset path (weak password accepted there only).
- Username change without re-auth, or to an existing name.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| curl | enumeration, reset probes | one request in flight, >=500 ms |
| hydra | credential testing | `-t 1 -W 1` |
| playwright | CSRF proof, MFA flow | serial |
| arjun | hidden auth parameters | `--rate-limit 2` |

## False-positive traps

- Locked-out account read as "wrong password".
- Enumeration claimed off generic messages with no absent-address control.
- A CSRF token that is present but never validated reported as protection.
- `SameSite=Lax` called a bypass without a working cross-site scenario.
- Session id "changing" only because the app issued a new cookie in the same response.

## Completion self-check

- [ ] lockout budget respected and stated.
- [ ] every enumeration claim has a control pair.
- [ ] session rotation tested at login, not only at logout.
- [ ] CSRF proven by a state change, not by token removal alone.
- [ ] access-control material handed to `finding-validation` with both identities.