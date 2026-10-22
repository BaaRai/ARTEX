---
name: bbot-recon
description: "Invoke this skill when a hunt task needs breadth-first recon on an authorized target: subdomain enumeration, subdomain-takeover surface, archived URLs, parameter mining, IP/ASN enrichment, or when one-shot tools (subfinder, httpx, nuclei) would need manual piping between steps. Runs bbot (recursive event-graph scanner, installed in the image) under hard program-policy gates: passive zero-touch mode when scanners are banned, active modules only when the program allows them."
---

# bbot-recon: recursive recon under program-policy gates

bbot (blacklanternsecurity/bbot, AGPL-3.0 like ARTEX, pinned v3.0.2 at
`/usr/local/bin/bbot`, venv `/opt/bbot`, module dependencies preinstalled in
the image) chains every discovery into an event graph and emits structured
JSON. Use it for breadth. Keep the single-purpose tools (subfinder, httpx,
katana, ffuf, nuclei) when you need exact control over individual requests or
a single host.

## Hard gates (read before running anything)

1. **Authorization**: targets and seeds must be inside the task's authorized
   scope. Third-party sources are queried about the target, not exploited.
2. **Program policy first**: read the program rules, then pick the mode.
   - Scanner ban, "no automated scanning", or a tight rate limit (<= 5 req/s):
     **Mode P only** (passive, zero-touch; the target is never contacted).
   - Active scanning allowed: Mode A is allowed; loud families still need the
     program to be explicit about aggressive scanning.
   - Loud/invasive families only under explicit permission, and prefer the
     dedicated single tools (ffuf, nmap, nuclei) when they exist, for rate
     control.
3. **Scope discipline**: pass in-scope targets with `-t`, add `-b` blacklist
   entries for anything excluded (logout/auth endpoints, specific subdomains).
   `--strict-scope` treats subdomains as out-of-scope unless listed. Events
   with scope_distance > 0 are intel only: never probe them.
4. **No API keys by default**: keyless modules only. If the task vault
   provides a key for a permitted source, pass it via
   `-c modules.<name>.api_key=...` and never echo it.
5. **Never** run the `kitchen-sink` preset on a live bounty target.
6. Module deps are preinstalled in the image, so every run passes `--no-deps`.
   If a module still complains about a dependency, record it as unavailable
   and continue without it: runtime pip does not work under egress allowlists.

## Modes and module sets (v3.0.2, all keyless)

**Mode P (passive, zero-touch)**: run with seeds only (`-s`, no `-t`) so
nothing enters scope and no module contacts the target. Sources: `crt`,
`crt_db`, `certspotter`, `dnsdumpster`, `hackertarget`, `anubisdb`,
`rapiddns`, `subdomaincenter`, `urlscan`, `myssl`, `wayback`, `asn`,
`shodan_idb` (InternetDB), `ipneighbor`; email family `dnscaa`, `dnstlsrpt`,
`emailformat`, `pgp`, `skymem`.

**Mode A (active-safe)**, when the program allows active scanning: adds
`dnsresolve`, `dnscommonsrv`, `baddns`, `baddns_direct`, `baddns_zone`
(takeover), `sslcert`, `robots`, `securitytxt`, `oauth`,
`graphql_introspection`, `fingerprintx`, `reflected_parameters`, `retirejs`,
`badsecrets`, `gowitness`, `iis_shortnames` (loud but targeted),
`filedownload`, `newsletters`, `hunt`, and the `http` spider (cap it, see
rate control).

**Loud/invasive**, explicit permission only: `paramminer_getparams`,
`paramminer_headers`, `paramminer_cookies`, `virtualhost`, `waf_bypass`,
`bypass403`, `dnsbrute`, `dnsbrute_mutations`, `portscan`, `lightfuzz`,
`nuclei`, `legba`, `medusa`, `host_header`, `url_manipulation`,
`generic_ssrf`.

## Recipes

Common tail for every run: `-om json,txt -o /app/data/tasks/<task_id>/bbot -n <intent-name> -y --no-deps`.
`--no-deps` is mandatory: module dependencies are baked into the image, and a
scan-time dependency check would run pip, which fails (or hangs) under a
program's egress allowlist. One scan per intent, named after it
(`passive-subdoms`, `takeover`, `params`).

0. Sanity first: `bbot --version && bbot -l | head -40` (the table lists each
   module's Needs-API-Key and flags; trust it over memory).
1. Passive subdomain sweep (P):
   `bbot -s example.com -m crt,crt_db,certspotter,dnsdumpster,hackertarget,anubisdb,rapiddns,subdomaincenter,urlscan -n passive-subdoms`
2. Archived URLs (P): `bbot -s example.com -m wayback -n wayback-urls`
3. IP/ASN enrichment (P): `bbot -s 203.0.113.0/24 -m shodan_idb,ipneighbor,asn -n ip-enrich`
4. Takeover pass (A): `bbot -t example.com -p baddns-heavy -n takeover`
5. Single-host deep look (A):
   `bbot -t host.example.com -m sslcert,robots,securitytxt,oauth,graphql_introspection,iis_shortnames -n host-deep`
6. Param mining (loud):
   `bbot -t https://host.example.com -m paramminer_getparams,paramminer_headers,paramminer_cookies -n params`
7. Email perimeter (P): `bbot -s example.com -m dnscaa,dnstlsrpt,emailformat,pgp,skymem -n emails`

Rate control: fewer modules per scan; `--exclude-cdn` to skip CDN/WAF
backends; identify yourself when the program asks for it
(`--user-agent "..."`); cap the spider with `-c spider_distance=1
spider_depth=2` on sensitive programs. When in doubt, split into two passive
scans rather than one broad active scan.

## Ingestion

After each scan read `<out_dir>/<scan_name>/output.json` (one JSON event per
line; also `scan.log` and `error.log` in the same directory as the audit
trail). Useful fields: `type`, `data`, `host`, `resolved_hosts`, `tags`,
`module` (source), `scope_description`, `scope_distance`, `timestamp`.

- `DNS_NAME` (resolved), `IP_ADDRESS`, `OPEN_TCP_PORT`, `URL` become facts,
  with tags as qualifiers (waf, cloudflare, cdn).
- `FINDING` / `VULNERABILITY` (baddns, badsecrets, iis_shortnames,
  aspnet_bin_exposure, telerik, ...) become finding candidates: attach the
  event description, the module, and the scan id. bbot findings are leads,
  not proofs; verify independently with the single tools before any report.
- `EMAIL_ADDRESS` becomes intel facts.

Record the scan id and output path in the task so every fact is traceable.

## Boundaries

- Never mix passive (Mode P) and active modules in the same run.
- Do not edit bbot's global config or secrets files; pass everything per scan
  via `-c` so runs stay reproducible.
- If a scan's error.log shows a module failed, retry once without it instead
  of widening the scan.
