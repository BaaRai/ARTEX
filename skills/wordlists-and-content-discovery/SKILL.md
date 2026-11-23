---
name: wordlists-and-content-discovery
description: "Invoke this skill when a web target needs its hidden surface found — directories, files, backups, parameters, vhosts, API routes. Covers list selection, pacing under the quiet profile, soft-404 and wildcard handling, and the difference between a real hit and a CDN error page."
---
# Content and Parameter Discovery

Find what the app does not link to: directories, files, backups, config, admin panels, parameters, vhosts. Under an **authorized** engagement only. Discovery is read-only and always inside the rate profile; a fast scan that trips a WAF produces a false "nothing here".

## Hard gates

1. Paths and hosts in scope only. A vhost hit on an out-of-scope IP is noted, not scanned further.
2. Fixed lists from `tool-bootstrap`; never a hand-typed mini list.
3. One tool at a time per host; 2 rps; stop on 429/503 and halve again.
4. No `--recursion` deeper than two levels without a reason.
5. A hit is not a finding; it is a candidate for the classes in `web-vuln-testing`.

## Phase 0: baseline the 404

Before a single word, measure how the server answers a random path, twice:

```
curl -s -o /dev/null -w "%{http_code} %{size_download}\n" "https://<host>/artx-$(date +%s)"
```

- Same 200 and same body for random paths → wildcard/soft-404. Note the length; use `ffuf -ac` or `--filter-size`.
- A single 404 page with a stable length → subtract that length too.
- A CDN error page with 200 → discovery through the edge is unreliable; note it and prefer the origin (`cloudflare-bypass` Phase 4).

## Phase 1: files and directories

```
ffuf -u "https://<host>/FUZZ" -w /usr/share/wordlists/web/raft-medium-directories.txt \
  -rate 2 -t 1 -mc 200,204,301,302,307,401,403 -ac -of md -o fuzz-dirs.md

gobuster dir -u "https://<host>/" -w /usr/share/wordlists/web/common.txt \
  -t 1 --delay 500ms -x php,html,txt,bak,zip,old -k
```

Both are listed so one can substitute for the other; do not run them together.

Extensions are target-driven: PHP, ASPX, JSP, plus `bak`, `old`, `save`, `swp`, `zip`, `tar.gz`, `sql`, `.git/HEAD`, `.env`, `docker-compose.yml`.

High-value names to check by hand even if the list misses them: `/.git/HEAD`, `/.svn/entries`, `/.env`, `/server-status`, `/actuator/env`, `/swagger-ui.html`, `/phpinfo.php`, `/backup.zip`, `/.well-known/security.txt`.

## Phase 2: parameters

- Harvest from the crawl and from JS (`api-recon` does the deep version); then:
```
arjun -u "https://<host>/<path>" -m GET --rate-limit 2
```
- Guess by name on endpoints that visibly branch: `id`, `page`, `file`, `url`, `next`, `debug`, `admin`, `lang`, `format`, `download`.
- A new parameter is a new input; every one goes back through `web-vuln-testing` classification.

## Phase 3: vhosts

```
ffuf -u "https://<host>/" -H "Host: FUZZ.<domain>" -w /usr/share/wordlists/dns/subs-5000.txt \
  -rate 2 -t 1 -fs <baseline-size> -ac
```

Confirm a vhost hit by comparing the body to the default vhost, not by status alone; a shared default page answers 200 for every Host. Retry the hit with `curl --resolve`.

## Phase 4: API routes

`/usr/share/wordlists/api/api-endpoints.txt` against discovered base paths (`/api`, `/v1`, `/graphql`). Hand off to `api-security` once a live root is found; that skill owns methods and auth.

## Phase 5: triage hits

For each hit record: URL, status, length, content type, auth requirement, and one line of why it matters. Classify into:

- static asset (skip),
- new surface (feed to the matching class skill),
- exposure (backup, VCS, config, logs, keys — `secrets-and-exposure`),
- admin/alternate app (auth testing).

Delete or ignore: default server pages, CDN "not found" with 200, favicon variants.

## False-positive traps

- Soft 404 counted as hits — always filter by size/`-ac`.
- Wildcard DNS (`*.domain`) making every subdomain resolve.
- CDN cache answering for the origin: a 403 that is the edge, not the app.
- Rate-limit pages (429) counted as found paths.
- `.git/HEAD` returning 200 with the wrong body (a rewrite to index.html).

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| ffuf | directories, files, vhosts | `-rate 2 -t 1`, `-ac` or `-fs` |
| gobuster | alternate directory mode | `-t 1 --delay 500ms` |
| arjun | parameter discovery | `--rate-limit 2` |
| curl | baseline, confirmation | one request in flight |
| gau / katana | known-URL and crawl harvest | `-rl 2 -c 1` for katana |
| dnsx | resolve candidate subdomains | `-rl 2 -t 1` |

## Completion self-check

- [ ] soft-404 baselined and filtered before scanning.
- [ ] one list per phase, from the fixed paths.
- [ ] every hit triaged, none left unclassified.
- [ ] rate flags matched and 429s handled.
- [ ] exposures handed to `secrets-and-exposure`, surfaces to `web-vuln-testing`.