---
name: engagement-intake
description: "Invoke this skill at the start of a task and whenever scope is ambiguous. Turns the program text into a written scope, rule-of-engagement and impact-budget file, sets credential and data handling, and verifies what actually resolves before testing begins."
---
# Engagement Intake (scope, rules, impact budget)

Everything downstream assumes a written scope. Without it, a redirect, a CDN hostname or a shared IP turns into an out-of-scope request, and a brute-force run turns into an unapproved load test.

Output is one artifact, `scope.md`, in the task work dir. It is short, factual and cited to the program text; ambiguity is recorded as a question, not resolved by optimism.

## Hard gates

1. **Written scope first.** No target request before `scope.md` exists, except a single DNS resolve used to confirm ownership of a declared name.
2. **Program policy beats this skill.** If the program bans scanners, brute force or specific paths, the stricter rule wins.
3. **Wildcards are not blanket permission.** A `*.domain` scope still excludes third-party SaaS, payment and auth providers unless named.
4. **Impact budget is explicit.** State what would degrade service and what is forbidden (DoS, password spraying volume, upload size, crawl depth).
5. **Credentials are recorded without secrets in the report.** Account references go in `scope.md`; secrets stay in the credential store and are referenced by name.

## `scope.md` template

```
# Scope — <task>

## In scope (verbatim from the program)
- hosts / CIDR / wildcards:
- paths or endpoints called out:
- mobile packages / repos / cloud accounts:

## Out of scope
- explicit exclusions:
- inferred exclusions (third-party SaaS, CDNs, payment, auth providers):
- ports and protocols excluded:

## Rules
- authentication allowed: yes/no — accounts:
- scanners allowed: passive/active/none
- destructive actions: none / test data only / named
- rate ceiling: 2 rps per host, one request in flight (program limit if stricter)
- time window / blackout:
- notification: who, when, channel

## Impact budget
- crawl depth, fuzz list size, upload size cap:
- brute force: hydra on <=2 endpoints, <=3 tasks
- sqlmap level/risk: 1/1 unless boolean pair found
- race tests: <=5 concurrent requests

## Data handling
- PII: read only / sample recorded in report
- evidence storage: work dir, filenames, retention

## Open questions
- ...
```

## Step 1: extract, do not interpret

Copy the program's own words into the in-scope and out-of-scope sections. Interpretation happens only where a hostname resolves to something the program did not anticipate (Phase 4).

## Step 2: credential inventory

List what exists: own registered accounts, provided accounts, API keys, tokens, test cards. Note role for each (needed later for `finding-validation` access-control proof). Mark which are safe to lock out.

## Step 3: boundary check by resolution

For each declared name: `dig +short` / `httpx -rl 2 -t 1` against the host list only. Confirm:

- the name resolves and answers,
- where it CNAMEs to (a CNAME to a third-party platform is usually still in scope for the host, but say so),
- whether a redirect lands out of scope; if yes, record "stop at the boundary, note the target".

## Step 4: impact budget

Derive the concrete numbers in the template from the policy. When the program says nothing, the platform default stands (2 rps, one in flight) and the artifact says "default, program silent".

## Step 5: open questions

Anything that would change the plan goes here and is raised before the affected phase: accounts unavailable, unclear wildcard ownership, no stated rate limit, ambiguity about destructive writes.

## Handoff

`scope.md` is the authority for every later skill. A skill that needs a decision not covered here returns to this file and adds a question rather than guessing. `web-vuln-testing`, `wordlists-and-content-discovery`, `cloud-and-container` and `ad-internal` all read it.

## Completion self-check

- [ ] `scope.md` exists and cites the program text for both lists.
- [ ] out-of-scope includes inferred third parties.
- [ ] credential inventory complete with roles, no secrets inline.
- [ ] impact budget has numbers, not adjectives.
- [ ] open questions listed before any scan.