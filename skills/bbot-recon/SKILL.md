---
name: bbot-recon
description: "Invoke this skill for OSINT and asset-discovery passes with bbot: passive subdomain enumeration, archived-URL harvesting, IP/ASN enrichment, email perimeter, takeover checks, and targeted per-host module runs, all paced to the program's rate profile."
---

# bbot recon (OSINT and asset discovery)

bbot carries its own module graph, its own interactsh client and its own rate controls, so one well-chosen run replaces a dozen chained CLIs. Choose modules by what the program allows, not by what looks interesting.

## Scope and permissions

- Passive modules touch only third-party data sources; safe on any in-scope root domain.
- Active-safe modules connect to in-scope hosts; they need the program to allow active scanning.
- Loud/invasive modules are brute-force or high-volume; explicit permission only.
- Keep every seed inside the written scope. A scan that wanders to a third-party host needs a scope-distance note.
- bbot is followed by other tools; leave the target in a state where a second scanner can start without hitting a rate debt.

## Rate model under the quiet profile

- Do not use `--fast-mode`; it raises concurrency above the profile.
- Cap the web spider and the fuzzer explicitly. The platform profile (2 rps, one request in flight per host) wins over bbot defaults.
- One bbot scan per intent, and never two scans against the same host in parallel.
- On 429 or 503 from the target, stop and back off; do not raise the rate.

## Phase 0: sanity

1. `bbot --version && bbot -l | head -40` — the table lists each module with its flags (passive / safe / active / invasive / loud).
2. Confirm every module you plan to pass to `-m` exists in this build. A name from memory may be internal-only (it runs anyway but cannot be selected).
3. Confirm the seed is in scope and resolves before spending a run on it (`dig`, `dnsx`).

## Module tiers

**Internal, always on** (never pass to `-m`): `aggregate`, `cloudcheck`, `dnsresolve`, `excavate`, `python`, `speculate`, `unarchive`. They run with every scan; `dnsresolve` is therefore always active and needs no opt-in.

**Passive (P)** — always allowed: `crt`, `crt_db`, `certspotter`,
`dnsdumpster`, `hackertarget`, `anubisdb`, `rapiddns`, `subdomaincenter`,
`urlscan`, `myssl`, `wayback`, `asn`,
`shodan_idb` (InternetDB), `ipneighbor`; email family `dnscaa`, `dnstlsrpt`,
`emailformat`, `pgp`, `skymem`.

**Mode A (active-safe)**, when the program allows active scanning: adds
`dnscommonsrv`, `baddns`, `baddns_direct`, `baddns_zone`
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
(`passive-subdoms`, `takeover`, `params`). The dependency binaries
(`gowitness`, `legba`, `medusa`, `fingerprintx`, `masscan`, `massdns`) are on
`PATH` as well, so they can be driven directly when a one-off is faster than a
scan.

0. Sanity first: `bbot --version && bbot -l | head -40`.
1. Passive subdomains (P): `bbot -s example.com -m crt,crt_db,certspotter,dnsdumpster,hackertarget,anubisdb,rapiddns,subdomaincenter,urlscan -n passive-subdoms`
2. Archived URLs (P): `bbot -s example.com -m wayback -n wayback-urls`
3. IP/ASN enrichment (P): `bbot -s 203.0.113.0/24 -m shodan_idb,ipneighbor,asn -n ip-enrich`
4. Takeover pass (A): `bbot -t example.com -p baddns-heavy -n takeover`

5. Host deep dive (A): `bbot -t host.example.com -m sslcert,robots,securitytxt,oauth,graphql_introspection,iis_shortnames -n host-deep`

6. Parameter mining (L): `bbot -t https://host.example.com -m paramminer_getparams,paramminer_headers,paramminer_cookies -n params`
7. Email perimeter (P): `bbot -s example.com -m dnscaa,dnstlsrpt,emailformat,pgp,skymem -n emails`

## Outputs and bookkeeping

- Every run writes JSON and txt under `/app/data/tasks/<task_id>/bbot/<scan-name>/`; keep that layout so a later intent can re-read the raw events.
- Register newly discovered hosts and services with `insert_assets`; a scan result that lives only on disk is lost to the graph.
- Record a fact for each scan: seed, modules, flags, start/end, and what it produced. Later intents must not repeat a scan blindly.
- Takeover candidates go through the subdomain-takeover verification rules; a `baddns` hit is a lead, not a finding.
- Do not paste whole event dumps into the report; cite the file and the specific event.

## Checklist

- [ ] seed in scope and resolving
- [ ] module tier matches what the program allows
- [ ] rate capped, one scan at a time
- [ ] `-om json,txt` and a named output directory
- [ ] new assets inserted, scan recorded as a fact
- [ ] takeover leads handed to the takeover skill, not reported raw
