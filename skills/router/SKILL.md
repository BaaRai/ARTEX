---
name: router
description: "Invoke this skill at the start of any intent when it is unclear which skill applies. Maps the observed signal to the correct skill, fixes the lifecycle order, and states precedence for classes that appear in more than one skill."
---
# Skill Router

Pick the skill from the signal, not from the task title. If two rows match, the earlier lifecycle stage wins; if none match, the fallback at the bottom applies.

## Order of work

```
engagement-intake → tool-bootstrap → recon (bbot-recon, api-recon, wordlists-and-content-discovery)
→ classification per surface → class skills → finding-validation → report-writer
→ retest-regression
```

Never start a class skill before `scope.md` exists. Never send a finding to `report-writer` before `finding-validation`.

## Signal to skill

| Signal | Skill |
|---|---|
| Task start, no scope file | `engagement-intake` |
| Tool named in a skill does not resolve | `tool-bootstrap` |
| Subdomains, ASN, passive breadth | `bbot-recon` |
| Frontend bundle, API endpoints, routes | `api-recon` |
| Hidden paths, backups, parameters, vhosts | `wordlists-and-content-discovery` |
| Cloudflare challenge, 1020, cf_clearance | `cloudflare-bypass` |
| Query/body parameter, reflected input | `web-vuln-testing` |
| REST/GraphQL/WebSocket, JWT, OAuth, BOLA | `api-security` |
| Login, reset, MFA, session cookie, CSRF | `auth-and-session` |
| CORS, iframe, postMessage, CSP | `client-side` |
| Duplicate headers, cache, race, method override | `http-protocol` |
| .git, .env, backups, source maps, keys, buckets | `secrets-and-exposure` |
| Dangling CNAME, provider 404 | `subdomain-takeover` |
| wp-login, wp-json, plugin path | `wordpress-cms` |
| Serialized blob, ViewState, phar:// | `deserialization` |
| AWS/Azure/GCP credential, Docker/K8s API | `cloud-and-container` |
| Domain, LDAP, SMB, Kerberos | `ad-internal` |
| APK/IPA/HAP package | `mobile-app` |
| Shell on a host | `privesc-postexploit` |
| Lockfile, CI workflow, base image | `supply-chain-ci` |
| Firmware image, IoT device | `firmware-iot` |
| Source tree, no live target | `codeaudit` |
| Vendor patch, binary diff, N-day | `patch-diff` |
| ELF/PE/APK static only | `rea` |
| Local binary/parser needs crash hunting, no CVE | `coverage-guided-fuzzing` |
| Confirmed crash or control-flow influence to weaponize | `exploit-dev` |
| LFI on PHP target | `php2shell` (after `web-vuln-testing` confirms the LFI) |
| Candidate ready for report | `finding-validation` |
| Validated findings to deliver | `report-writer` |
| Fix claimed | `retest-regression` |
| EDR telemetry question | `edr-bypass-re` |
| ScopeSentry platform operations | `scopesentry` |

## Precedence for shared classes

| Class | Owner | Other |
|---|---|---|
| SSRF mechanics | `web-vuln-testing` | `api-security` frames API-only variants |
| XXE | `web-vuln-testing` | `api-security` only for XML content-type swaps |
| IDOR/BOLA | `api-security` for APIs, `web-vuln-testing` second look for web | `finding-validation` proves with a second account |
| Rate-limit behavior | `api-security` (mechanism) | `cloudflare-bypass` (1015 handling) |
| Auth tokens in cookies | `api-security` for JWT, `auth-and-session` for opaque sessions | — |
| Asset discovery | `bbot-recon` for breadth, `wordlists-and-content-discovery` for a known host | `scopesentry` only when the platform holds the scope |

## No match

1. Restate the observation in one line.
2. Check the table again against the observation, not the task wording.
3. If still unmatched: read `web-vuln-testing` Phase 0 and classify the surface there; most unmatched web signals are input or exposure classes.
4. Record the gap in the task facts — an unmatched signal is a candidate for a new skill.

## Anti-patterns

- Starting a scanner before scope exists.
- Using `codeaudit` on a live target with no source.
- Jumping to `php2shell` before the LFI is confirmed.
- Running `report-writer` to "see what sticks".
- Two class skills against the same parameter in parallel.