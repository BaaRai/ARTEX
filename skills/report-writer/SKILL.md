---
name: report-writer
description: "Invoke this skill to turn validated findings into a report — structure, executive summary, per-finding sections with bound evidence, severity justification, remediation and reproduction. Requires findings that already passed finding-validation."
---
# Report Writing

The report is the deliverable. Under an **authorized** engagement only, and only for findings that survived `finding-validation` — a report cannot fix an unproven claim.

## Hard gates

1. Every finding cites its evidence artifact path and PoE level from validation; no re-derivation from memory.
2. Quote exact requests and responses; paraphrase only the surrounding prose.
3. Severity matches the validated PoE and reach; do not upgrade because the section reads weak.
4. No tool output pasted wholesale; the smallest excerpt that proves the point.
5. Client-visible language: no internal agent jargon, no "the model", no speculation labelled as fact.

## Structure

```
# Penetration Test Report — <scope> — <date>
## 1. Executive summary
## 2. Scope, rules and method
## 3. Findings overview (table: id, title, severity, affected asset, status)
## 4. Findings (one section each)
## 5. Attack chains
## 6. Hardening notes
## 7. Appendix A — reproduction environment
## 8. Appendix B — evidence index
```

## Section rules

**Executive summary** — 5-10 sentences: what was tested, what was found, the two or three findings that matter, the business consequence, and the overall posture. No payloads, no tool names.

**Scope** — take verbatim from `scope.md`; include the out-of-scope list and the test window. This section is also the disclaimer: what was not tested is stated.

**Overview table** — every finding once, ordered by severity then id. Status: open / retest-pending / fixed.

**Each finding** — fixed subsections:

1. Title — impact first (`Unauthenticated SQL injection in /search`), not class first.
2. Severity, PoE, CVSS vector (optional, must match severity).
3. Affected asset(s) and parameter.
4. Summary — 2-3 sentences, what and why it matters.
5. Preconditions — auth level, account, network position.
6. Reproduction — numbered steps, exact command or request, expected result.
7. Evidence — request/response excerpt, marker output, file paths.
8. Impact — concrete: which data, how many records if known, what an attacker does next.
9. Remediation — specific and testable; the framework-level fix, plus the compensating control.

**Attack chains** — findings that only pay off together, in order, with the joint impact. Reference finding ids; do not repeat their text.

**Hardening notes** — the gate-3/4 failures worth mentioning: missing headers, weak CSP, verbose errors, outdated-but-unreachable components. One line each.

**Appendices** — environment (tool versions relevant to reproduction, egress IP, accounts used) and an evidence index (path, finding id, hash or timestamp).

## Severity justification

Write one clause tying the level to the validated capability, e.g. "critical: pre-auth code execution as the web user" or "medium: authenticated user reads another tenant's invoices, one record proven". If the clause needs a second sentence to hold, re-check the PoE.

## Registering

- Use `report_finding` per finding first, then `update_finding_report` with the full text and the exact `evidence_quotes` copied from the bound traffic.
- One report per root cause; variants listed inside their parent finding.
- Keep finding ids stable across edits so retest links survive.

## Language traps

- "Could potentially" — decide.
- "The scanner found" — say what the scanner found and what you confirmed.
- Class names as titles (`IDOR`) — say what object and what data.
- "Critical" in the summary of a medium finding.

## Completion self-check

- [ ] every finding has a PoE and an evidence path.
- [ ] no claim without a reproduction step it points to.
- [ ] severity clause written per finding.
- [ ] out-of-scope and untested surfaces stated.
- [ ] chains reference, not duplicate, their findings.