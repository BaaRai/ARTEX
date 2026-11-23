---
name: retest-regression
description: "Invoke this skill when a fix is claimed or a retest is scheduled — re-run the original PoC byte-for-byte, classify fixed/partial/variant, probe the root cause for incomplete fixes, and record the verdict with fresh evidence. Also covers regression around a remediation."
---
# Retest and Regression

A retest answers one question: does the original attack still work? Under an **authorized** engagement only, and only for the finding's original asset and parameter unless scope widened.

## Hard gates

1. Replay the stored PoC exactly; no "improved" payload that changes the comparison.
2. Fresh evidence with timestamps; never reuse the old capture as the new result.
3. Test only the finding's asset plus its scope-declared neighbours for variants.
4. A fix claim is not a fix; the PoC decides.
5. Verdicts are fixed / partial / not fixed / not reproducible — no adjectives.

## Phase 1: restore the baseline

- Pull the finding, its PoE, its request artifact, and the exact environment (account, role, egress).
- Confirm the asset still resolves and the endpoint still exists; a 404 may be a route change, not a fix.
- Confirm the account is still valid and has the original role.

## Phase 2: exact replay

- Send the stored request unmodified, in the original auth context.
- Capture request and response complete.
- Compare against the original expected result: same capability = not fixed.

## Phase 3: classify

| Result | Verdict |
|---|---|
| original capability reproduced | not fixed |
| no capability, endpoint healthy | fixed |
| capability reduced (read only, fewer records, narrower input) | partial |
| endpoint or precondition gone | not reproducible (state why) |

A WAF rule in front of a still-vulnerable app is **partial**, not fixed; prove the app is unchanged with a bypass or a direct-to-origin request (`cloudflare-bypass` Phase 4).

## Phase 4: incomplete-fix probe

Only where the verdict is fixed or partial, and only within the same root cause:

- Encoding and case variants of the same payload.
- Sibling parameters and the same sink on other routes.
- A second account/role for access-control findings.
- The same vulnerable component on another in-scope host.

Record which probe would have caught a partial fix, if one did.

## Phase 5: record

- New evidence under `evidence/retest-<date>-<finding-id>/`.
- Verdict, date, tester, exact difference from the original result.
- If fixed: one sentence on what the fix appears to be (from observation, not guess).
- If partial: the remaining capability, precisely.
- Update the finding status; keep the original evidence intact.

## False-positive traps

- 404 after a deploy read as fixed when the route moved.
- A block page read as a fix.
- Payload cached by a CDN reproducing the old behaviour.
- Different account/role changing the result.
- "Fixed" claimed from one successful request without a control.

## Completion self-check

- [ ] original artifact replayed unmodified.
- [ ] fresh evidence with timestamp.
- [ ] verdict from the table, no hedging.
- [ ] partial fixes carry the remaining capability.
- [ ] status updated, original evidence untouched.