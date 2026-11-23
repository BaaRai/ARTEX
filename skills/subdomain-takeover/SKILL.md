---
name: subdomain-takeover
description: "Invoke this skill when DNS shows a dangling record — a CNAME or NS/MX target pointing at a deprovisioned third-party service. Covers detection with dnsx, provider fingerprints, claim verification with a unique marker, and the difference between a takeover and an unused record."
---
# Subdomain Takeover

A DNS record points at a service nobody owns any more; whoever claims the service controls the hostname. Under an **authorized** engagement only, and the claim must use a free account.

A takeover is proven only when **you** serve content from the hostname, not when a fingerprint matches.

## Hard gates

1. Scan only names in scope; a dangling CNAME to a third party is still an in-scope *name*.
2. Claim with a free tier and a unique marker (`artx-<random>`), never a paid plan.
3. Claim one candidate at a time; release nothing until the finding is recorded.
4. If the program forbids third-party signups, a fingerprint-only result is a note, not a finding.
5. Do not take over a name someone else is actively using — check whether it currently serves real content.

## Phase 1: candidate discovery

- Sources: `subs-5000.txt` and bbot output, certificate transparency (`crt.sh`), JS bundles, archived URLs.
- Resolve and keep only dangling records:

```
dnsx -l candidates.txt -rl 2 -t 1 -cname -resp -o cnames.txt
```

- A CNAME to a provider domain is the candidate; an A record to a provider IP is not.
- Also check `NS` delegation to an unclaimed zone and `MX` to a dead provider.

## Phase 2: provider fingerprint

Fetch the hostname and match the provider's "unclaimed" page:

```
curl -sS -D - "https://<name>/" | head -40
```

| Provider | CNAME pattern | Fingerprint |
|---|---|---|
| GitHub Pages | `<user>.github.io` | "There isn't a GitHub Pages site here" |
| Heroku | `*.herokudns.com`, `*.herokuapp.com` | "No such app" |
| AWS S3 website | `*.s3-website-*.amazonaws.com` | `NoSuchBucket` |
| Azure | `*.cloudapp.azure.com`, `*.azurewebsites.net` | 404 "Web App - Unavailable" |
| Netlify | `*.netlify.app` | "Not Found - Request ID" |
| Vercel | `*.vercel.app` | "The deployment could not be found" |
| Fastly | `*.fastly.net` | "Fastly error: unknown domain" |
| Shopify | `*.myshopify.com` | "Sorry, this shop is currently unavailable" |
| Surge | `*.surge.sh` | "project not found" |
| Pantheon | `*.pantheonsite.io` | "The gods are wise" |

Nuclei also has takeover templates; run them paced (`-rate-limit 2 -c 1 -t http/takeovers/`) as a cross-check, not the proof.

## Phase 3: confirm it is actually dangling

- The unclaimed page must be the provider default, not the app's own 404.
- `dig +short <name>` still points at the provider.
- No other record (A/AAAA) shadows it.
- The hostname is not already serving the app behind a proxy (no `cf-ray`, no `x-served-by` to a live origin).

## Phase 4: claim with a marker

- Free account on the provider, create the resource with exactly the dangling target name (`<user>.github.io` repo named `<user>.github.io`, S3 bucket named exactly the CNAME target, etc.).
- Publish a page whose title/body is the unique marker.
- Verify through the target hostname (DNS propagation up to a few minutes):

```
curl -sS "https://<name>/" | grep -i artx
```

- Record: marker, provider, resource id, timestamp, HTTP response through the hostname.

## Phase 5: impact

State what the takeover can do beyond defacement:

- cookies scoped to the parent domain (session fixation, cookie toss),
- OAuth redirect URIs and CORS `Origin` trust on that host,
- password-reset links and email sender reputation,
- service-worker scope if the parent domain is shared.

A takeover on a hostname used in auth flows is high; a marketing subdomain is low/medium.

## False-positive traps

- Provider default page on a hostname the provider still holds for someone else.
- Wildcard DNS (`*.domain`) making every name resolve — takeover only applies to the specific dangling record.
- A parked page from a registrar counted as takeover-ready.
- Claims that never resolve through the target hostname.
- An expired certificate read as a takeover signal.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| dnsx | resolve and list CNAMEs | `-rl 2 -t 1` |
| dig | confirm records | serial |
| curl | fingerprint, marker verification | one in flight |
| nuclei | takeover template cross-check | `-rate-limit 2 -c 1 -t http/takeovers/` |

## Completion self-check

- [ ] each candidate is a CNAME to a provider, not just a 404.
- [ ] provider fingerprint matches that provider's real default page.
- [ ] marker served through the target hostname and captured.
- [ ] impact named in terms of cookies/auth/redirects.
- [ ] claim recorded with resource id so it can be released later.