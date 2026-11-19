# Third-party skill sources

The playbook packs under `skills/*-playbook/` and `skills/web-vuln/`,
`skills/payload-cookbook/`, `skills/post-exploit/`, `skills/ai-redteam/`
contain reference material derived from two upstream repositories.

## Upstreams

| Project | License | Use |
|---|---|---|
| [pikpikcu/airecon](https://github.com/pikpikcu/airecon) | MIT | `airecon/proxy/skills/**` — the primary corpus |
| [pikpikcu/airecon-skills](https://github.com/pikpikcu/airecon-skills) | none declared upstream | `skills/**` — community additions not present in the corpus above |

Both were read at commit `e5bb22b5089f21c73472461795abfe6b1f3f9b8f` (airecon) and
`884362265464e108bb229f17d85d51fc77eb6a7d` (airecon-skills).

`airecon-skills` ships a `skills/ctf/CREDITS.md` stating that its CTF material
originates from [ljagiello/ctf-skills](https://github.com/ljagiello/ctf-skills)
(MIT, © 2026 Lukasz Jagiello). That attribution is carried forward implicitly by
the `ctf-playbook` pack. MIT is compatible with this repository's AGPL-3.0.

## What we took

Only the Markdown playbook bodies. We did **not** take their loader, their
`skills.json` keyword-mapping mechanism (this repository discovers skills through
`SKILL.md` frontmatter and a per-agent visibility table instead), their installer
scripts, or their Docker assumptions.

## What we changed

The reference bodies are ported, not verbatim:

- **Frontmatter normalised.** Upstream had 168 files with no frontmatter at all,
  which our loader would have registered with an empty description and the
  `shipped_skills` test would have rejected. Every file now carries `name` and
  `description`.
- **Encoding fixed.** Several upstream files were cp1252, not UTF-8.
- **Duplicates collapsed.** 21 files appeared byte-identically in both upstream
  repositories; the `airecon` copy won.
- **Environment corrections.** Each pack's `SKILL.md` states what this image
  actually provides and flags upstream instructions that depend on tooling we do
  not ship, rather than letting an agent issue commands that cannot succeed.
- **Gates added.** The pack-level `SKILL.md` files carry this repository's
  authorization, evidence, and blast-radius discipline. The reference bodies
  themselves remain upstream methodology.

## Maintaining the port

Reference bodies are upstream content. Prefer re-syncing from upstream over
hand-editing them, and keep local corrections in the pack `SKILL.md` files so the
next sync stays mechanical.

## Knowledge corpus

The `knowledge/` package embeds 22 JSON files from
`airecon/proxy/data/` — the same upstream project, different artifact. They are
the machine-readable side of AIRecon: vulnerability ontology, WAF signatures,
technology correlations, port correlations, CVE correlations, endpoint
patterns, AB signals, attack chains, scan profiles, and the rest.

They were vendored as-is with one exception, `port_correlations.json`, and
become a Go package so the corpus is compiled into the binary rather than
read from disk at runtime.

### Vendored files

| File | Records | Notes |
|---|---|---|
| `vuln_ontology.json` | 77 classes | canonical + aliases; `buildClassIndex` folds alias → canonical at load |
| `ab_signals.json` | 24 signals | each with `objective_patches` |
| `attack_chains.json` | 36 chains | 1-indexed contiguous `order` in every chain |
| `escalation_patterns.json` | 8 patterns | `templates` has 10 objective keys |
| `unified_patterns.json` | 99 merged patterns | 6 objectives + `vuln_detection_patterns` |
| `patterns.json` | 35 behaviour rules | +1 deviant `waf_bypass_strategies` split at decode |
| `waf_signatures.json` | 100 header + 39 body | header patterns are presence or value-match; body are regex |
| `waff_bypass.json` | 6 strategy families | `SAFE_TEST_POLICY` etc. kept as `json.RawMessage` |
| `tech_correlations.json` | vendor profiles | keyed by vendor name; `paths` per profile |
| `port_correlations.json` | 103 ports | **trimmed** — see corrections below |
| `cve_correlations.json` | tech → CVEs | |
| `endpoint_patterns.json` | URL patterns | |
| `verification_patterns.json` | proof patterns | |
| `file_extensions.json` | extension map | |
| `fuzzer_data.json` | fuzzer corpus | |
| `scan_profiles.json` | 6 profiles | `standard` is `{}`, `quick`=200, `deep`=1200, `ctf`=800, `bugbounty`=1000 |
| `refusal_markers.json` | refusal signals | |
| `awaiting_input_markers.json` | clarification signals | |
| `compressors.json` | compressor signatures | |
| `tools.json` | 34 function schemas | `type: function`, no duplicates |
| `tools_meta.json` | 25 keys | `Raw` preserves all keys; typed subset in `ToolsMeta` |
| `skills.json` | 1180 keywords | maps keyword → path; **not used as a discovery mechanism**, provenance only |

### Corrections applied to `port_correlations.json`

Upstream had four data errors and one CJK comment. All are corrected in the
vendored copy; upstream was not notified.

| Port | Service | Upstream value | Correction | Rationale |
|---|---|---|---|---|
| 1755 | `MS Streaming (MMS)` | `MS RDP` | service name | 1755 is RTSP/streaming (Microsoft Media Server), not RDP |
| 1098 | `RMI Activation` | `TSDS` | service name | 1098 is Java RMI Activation, not TSDS |
| 162 | (note) | CJK string | English translation | only non-ASCII in the corpus; translated to `SNMP trap receiver; community string in v1/v2c` |
| 49152, 55555, 55556, 60000 | (severity) | various | `severity` key omitted | no reliable severity classification exists for these ports |

Additionally, the CVE and tool lists were **dropped entirely** per the project
policy that unverifiable claims should not be imported. The original had
CVE-2021-41773 on ProFTPD (actually Apache), CVE-2020-9951 on Pure-FTPd
(actually WebKit), and ~45% of tool strings naming binaries not in this image
(amass, masscan, naabu, gobuster, feroxbuster, dirsearch, whatweb, nikto, hydra,
crackmapexec, Impacket, msfconsole, wpscan, cloud CLIs). Only the verified
fields — `service`, `severity` (where present), and `notes` — survive.

### Folding rules

The ontology's 77 canonical classes and 295 alias tokens are folded at load
time through `knowledge.Class`. Rules:

1. A canonical class maps to itself (idempotent).
2. An alias that is also a canonical class is never rewritten.
3. An alias claimed by 2+ classes stays literal (16 ambiguities: `rce`,
   `script`, `metadata`, `credential`, `pickle`, `proto`, `protobuf`, `grpc`,
   `websocket`, `desync`, `double_spend`, `ai`, `chatbot`, `llm`, `dan_mode`,
   `ignore_previous`).

### Consumers

- `db.normRiskArea` folds risk-area strings through `knowledge.Class` so
  `"SQLi"` and `"SQL injection"` bucket together in the coverage ledger.
- `enrich.doHTTP` calls `knowledge.FingerprintTech` and `knowledge.IdentifyWAF`
  on every HTTP probe, storing technology names in `assets.technologies` and
  the WAF verdict in `assets.extra.waf`.
- `knowledge.TechPaths`, `knowledge.CVE`, `knowledge.Port` are available for
  future use; not yet wired.
