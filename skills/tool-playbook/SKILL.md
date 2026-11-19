---
name: tool-playbook
description: "Driving a specific security tool correctly: nmap, nuclei, sqlmap, dalfox, fuzzing strategy, hashcat/john, Impacket, Metasploit, semgrep, source review. Index of 18 references, each annotated with whether this image can actually run it."
---

# Tool playbook (index)

Eighteen tool references under `references/`. Read one when you know *what* you
want to test but not the exact invocation, or when a tool is behaving in a way
you did not expect.

Every reference here was written against a different toolchain. The first
section of each warns what it assumes; this index states plainly what this image
can run.

## Hard gates

1. **Tool defaults are not policy.** `nmap -T4`, `ffuf` at 200 threads, and
   `sqlmap --level 5 --risk 3` all exceed what most programs permit. Derive the
   rate from the program, then configure the tool to it.
2. **No runtime installs.** Egress is allowlisted. If a tool is missing, use the
   substitute named below; do not attempt `pip install`, `apt-get`, or
   `go install`.
3. **Template and script supply chain.** `nuclei` templates and `nmap` scripts run
   code and send traffic you did not write. Only use templates bundled with the
   pinned version; do not fetch new ones mid-engagement.
4. **Output is a lead.** Every tool in this pack produces candidates. None
   produces findings. Verification is a separate step with a separate request.
5. **Secrets in output.** Cracked hashes, tokens, and keys found by these tools
   are sensitive. Record the minimum, redact in reports.

## What is installed

| Tool | Version | Use |
|---|---|---|
| `nuclei` | 3.11.1 | templated checks, tech detection |
| `httpx` | 1.12.0 | probing, response metadata |
| `subfinder` | 2.17.0 | passive subdomain sources |
| `katana` | 1.8.0 | crawling and endpoint discovery |
| `ffuf` | 2.3.0 | content and parameter fuzzing |
| `dalfox` | 3.2.4 | XSS scanning and DOM sinks |
| `sqlmap` | 1.9.9 | SQL injection confirmation |
| `nmap` | distro | port and service scanning, NSE |
| `bbot` | 3.0.2 | recursive recon (see `bbot-recon`) |
| `playwright-cli` | 1.64.1 | browser automation (see `playwright-cli`) |
| `curl`, `jq`, `ripgrep`, `git`, `python3`, Node 22 | — | glue |

**Not installed:** Metasploit (`msfconsole`, `msfvenom`), Impacket, `netexec`,
`hashcat`, `john`, `hydra`, `semgrep`, `wapiti`, Caido, `mitmproxy`, `gobuster`,
`feroxbuster`, `dirsearch`, `masscan`, `naabu`, `whatweb`.

## Choosing a reference

| Need | Read | Usable here? |
|---|---|---|
| Port and service scanning | `references/nmap.md` | Yes |
| Templated vulnerability checks | `references/nuclei.md` | Yes |
| Fuzzing content and parameters well | `references/advanced-fuzzing.md` | Yes, with `ffuf` |
| SQL injection confirmation | `references/sqlmap.md` | Yes |
| XSS scanning | `references/dalfox.md` | Yes |
| Browser-driven testing | `references/browser-automation.md` | Yes, via `playwright-cli` |
| What tools exist and what they do | `references/tool-catalog.md` | Reference only |
| Writing your own tooling | `references/scripting.md` | Yes, `python3`/Node |
| Hash identification and cracking | `references/hashcat-john.md` | Method only — no cracker |
| SMB/AD protocol work | `references/impacket.md` | Method only |
| Exploitation framework | `references/metasploit.md` | Method only |
| Static analysis | `references/semgrep.md` | Method only — use `ripgrep` |
| Source audit workflow | `references/source-audit.md` | Yes, see `codeaudit` |
| Code review discipline | `references/code-review.md` | Yes |
| Intercepting proxies | `references/caido.md` | Method only — use `traffic_*` |
| Reporting | `references/reporting.md` | Adapt to `report_finding` |
| Installing tools | `references/install.md` | **Do not run** — fixed image |
| Vulnerability scanner orchestration | `references/wapiti.md` | Method only — not installed |

## Method

1. **State the question before invoking.** "Is 8443 serving the same app on a
   different build?" beats "run nuclei".
2. **Set the rate from policy**, not from the tool's default.
3. **Read output critically.** A `nuclei` match is a template firing on a
   heuristic; re-request by hand and confirm the response before escalating.
4. **Log the exact command** in the finding evidence. Reproducibility is part of
   the finding.

## Boundaries

- `references/install.md` is included for completeness and must not be executed.
  The image is immutable by design.
- Method-only references are still useful for sequencing and for interpreting
  output; just do not present a step as performed when the tool was unavailable.
- Do not tune a tool until it "finds something". Tune it until its output is
  trustworthy.
