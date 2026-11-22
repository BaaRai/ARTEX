---
name: http-protocol
description: "Invoke this skill for HTTP-layer flaws — request smuggling (CL.TE, TE.CL, TE.TE, HTTP/2 downgrade), cache poisoning and cache deception, race conditions on single-use logic, method override and absolute-form abuse. Header-level work that sits below the app."
---
# HTTP Protocol Flaws (smuggling, cache, race)

Flaws in how the chain parses and caches requests, not in what the app renders. Under an **authorized** engagement only. Highest false-positive rate of any class here; every claim needs a repeat and a control.

Infrastructure prerequisite: a front end and a back end that disagree (CDN/ALB + origin, proxy + app). On a single-server target most of this is not applicable — check `server`, `via`, `x-cache`, `cf-ray` first.

## Hard gates

1. Smuggling probes change the connection state; never run them in parallel with other scanners on the same host.
2. Cache poisoning needs a second, clean request to prove one user poisoned what another reads.
3. Race tests stay at <=5 concurrent requests.
4. Keep raw bytes; curl alone is not enough for chunked work — use `--data-binary` with files or a python raw-socket script.

## Phase 1: topology check

```
curl -sS -D - -o /dev/null "https://<host>/"
```

- Two layers (CDN headers, `via`, `x-cache`, differing `server` on 4xx) → applicable.
- Single `nginx`/`apache` with no front end → note and skip.
- Whether HTTP/2 is offered at the edge: smuggling via h2 downgrade only exists if h2 is accepted and the origin speaks h1.

## Phase 2: request smuggling

Send raw requests with explicit lengths; classify by the *observed desync*, not by one response.

- **CL.TE**: `Content-Length` wins at the front end, `Transfer-Encoding` at the back end.
```
POST / HTTP/1.1
Host: <host>
Content-Length: 6
Transfer-Encoding: chunked

0

G
```
- **TE.CL**: reverse of the above.
- **TE.TE**: obfuscated header (`Transfer-Encoding: xchunked`, `Transfer-Encoding : chunked`, duplicate TE) so one hop ignores it.
- **HTTP/2 downgrade**: send `Transfer-Encoding: chunked` as an h2 header; a front end translating to h1.1 can double-handle the body.

Confirmation ladder:

1. A smuggled prefix makes the *next* request return a 404/other path's body.
2. Repeat three times; only a repeatable desync counts.
3. Escalate from desync to impact: bypass a front-end auth rule, or poison the next user's request. The impact is the finding; the desync is the mechanism.

## Phase 3: cache poisoning

- Identify the cache: `x-cache: HIT/MISS`, `age`, `cf-cache-status`.
- Find unkeyed inputs that reflect into the response: `X-Forwarded-Host`, `X-Host`, `X-Forwarded-Scheme`, `X-Original-URL`, `X-Rewrite-URL`.
- Confirm the header is not in the cache key: poison, wait for `HIT`, then request without the header from a clean context.
- Impact: absolute-URL/script injection into the cached page, redirect, or auth-rule bypass.
- **Cache deception**: request `<authenticated-path>/artx.css` (and `.jpg`, `.js`, `.txt`); if the personalized page is stored as a static asset, a later unauthenticated request reads it. Prove with a second clean session.

## Phase 4: race conditions

Target single-use logic: coupon redemption, gift-card balance, follow/like counters, withdrawal, invite acceptance, MFA verify.

- Warm the endpoint, prepare the exact request.
- Fire 5 identical requests in parallel (python `threading` with a shared barrier or `asyncio.gather`).
- Success criterion: more effect than requests allowed (two orders for one coupon, balance below zero).
- Repeat twice; a single lucky overlap is not proof.
- For tight timing, align by sending after a fixed delay from a common start; a single-packet attack is not available here.

## Phase 5: method and URL-form abuse

- `X-HTTP-Method-Override: PUT/DELETE` on endpoints that only expose GET/POST.
- Absolute-form target (`GET https://internal/ HTTP/1.1`) reaching internal vhosts.
- `X-Original-URL`/`X-Rewrite-URL` path-rule bypass on front-end ACLs.
- Trailing-dot, double-slash and case path variants against admin prefixes (`/Admin/`, `//admin/`, `/admin/.`).

## Recording

Raw request bytes, both responses (poisoned and clean), timing for races, and the number of attempts. Smuggling evidence without the repeat count is not evidence.

## False-positive traps

- One desync read as confirmed; no repeat.
- Cache "poisoning" where the header *is* in the key (every request with the header differs anyway).
- Cache deception on a page that was never personalized.
- Race won by ordinary retries because the app was idempotent already.
- Method override accepted by the app but no rule actually bypassed.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| curl | header and override probes | `--data-binary @file`, serial |
| python3 | raw sockets, race harness | serial or <=5 threads |
| playwright | authenticated-vs-clean cache proof | serial |

## Completion self-check

- [ ] topology established before any probe.
- [ ] every desync repeated three times.
- [ ] cache claims proven from a clean second context.
- [ ] race claims repeated and counted.
- [ ] impact stated, not just the mechanism.