# Security Policy

## Supported Versions

ARTEX is an autonomous penetration-testing tool. Security fixes are provided only for the latest official release line. The `main` branch is not considered a stable release and may change at any time.

| Version | Supported          |
| ------- | ------------------ |
| 0.5.x   | :white_check_mark: |
| 0.4.x   | :x:                |
| < 0.4   | :x:                |

Notes:
- Please upgrade to the latest release before reporting a vulnerability.
- Forks, custom builds from arbitrary commits, and modified versions are not covered by official support.

## Reporting a Vulnerability

**Do not open a public issue for a security vulnerability.**

Preferred channels:
1. Use **GitHub Security Advisories** → **Security** tab → **Report a vulnerability** in the `[owner]/ARTEX` repository.
2. If GitHub Advisories are unavailable, email **security@[your-domain]**.  
   PGP key: `[link or fingerprint]`

Please include:
- A description of the vulnerability and its expected impact.
- The ARTEX version and/or commit hash.
- Runtime environment (OS, Docker/local, PostgreSQL, LLM provider, etc.).
- Minimal reproduction steps (PoC).
- Logs, screenshots, or sample traffic with **sensitive data removed**.
- Suggested remediation, if available.

**Do not send** real third-party data, credentials, tokens, customer data, or unredacted sensitive content.

## Scope and Authorized Testing

ARTEX is an offensive-security tool. Test only against:
- Your own ARTEX deployment in a lab or local environment;
- Systems you own;
- Targets explicitly authorized in writing (bug bounty, pentest contract, etc.).

Testing, scanning, or exploiting third-party systems without explicit authorization is strictly prohibited. Reports that violate this principle may be closed without action.

## What to Expect

We aim to respond on a best-effort basis, as an open-source project:

| Stage | Expected timeframe |
|---|---|
| Acknowledgement of report | within 3 business days |
| Initial triage / severity classification | within 7 business days |
| Progress update | every 7–14 days, or when there is a material change |
| Fix for Critical severity | target 7–14 days |
| Fix for High severity | target 30 days |
| Fix for Medium/Low severity | aligned with release schedule, typically 60–90 days |

These timelines may change depending on complexity, third-party dependencies, or maintainer capacity.

## If a Report Is Accepted

- We will open a private **GitHub Security Advisory** for coordination.
- We will identify the release containing the fix when it is published.
- We may request a **CVE** if the vulnerability qualifies.
- We will credit you or your organization in the advisory if you wish.
- Default disclosure timing: **90 days** after a fix is available, or earlier by mutual agreement. If the vulnerability is being exploited in the wild, we may disclose earlier.

## If a Report Is Declined

We will explain the reason, for example:
- It is not a security vulnerability but intended behavior.
- It is outside ARTEX’s scope (e.g., an issue in an LLM provider, PostgreSQL, Docker, or a browser).
- There is insufficient information to reproduce it.
- The report only states that “the tool can be misused” without identifying a specific technical flaw in ARTEX.

Where appropriate, we may convert the report into a public hardening issue or feature request.

## Out of Scope

The following are generally **not** considered ARTEX security vulnerabilities:
- Attacks or scans against systems you do not own or are not authorized to test.
- Self-XSS, spam, or brute-force without real impact.
- Large-scale DoS/DDoS or resource-exhaustion attacks.
- Social engineering targeting maintainers or users.
- Issues that exist only in third-party dependencies and do not affect ARTEX.
- Reports that “ARTEX can be used to attack systems” when the user has valid authorization — this is the nature of a penetration-testing tool, not a vulnerability.
- Missing headers or hardening configuration without a concrete security impact.

## Safe Harbor

We welcome ethical security research. If you follow this policy and test only within authorized scope, we will not pursue legal action against you for good-faith research. This does not exempt you from applicable laws in your jurisdiction.

## Disclosure Policy

- Do not publicly disclose vulnerability details before a fix is available or before the coordinated disclosure period ends.
- Do not publish a public PoC without maintainer consent.
- If you do not receive a response within a reasonable time, please ping the contact channel above before disclosing.

Thank you for helping make ARTEX safer.
