---
name: client-side
description: "Invoke this skill for browser-side flaws that are not XSS — CORS misconfiguration, clickjacking, postMessage origin checks, CSP quality, tabnabbing and client-side storage exposure. Includes the local PoC page pattern and per-class false-positive traps."
---
# Client-Side Flaws (CORS, framing, postMessage, CSP)

Server-side misconfigurations that only bite in a browser. Under an **authorized** engagement only. None of these is a finding until it is demonstrated from an attacker-origin page, not from curl alone.

## Hard gates

1. Two browsers contexts: victim (authenticated) and attacker page on a local origin.
2. Local PoCs are served from `127.0.0.1` — a local origin is enough to prove cross-origin behavior.
3. Read-only unless the CORS/CSRF path writes; no mass data pulls.
4. Every claim states the browser and whether cookies were involved.

## Phase 1: CORS

Test on an authenticated endpoint returning sensitive JSON. Send `Origin` and read the response headers:

```
curl -sS -D - -o body.json -H "Origin: https://artx-evil.test" -b jar.txt "https://<host>/api/me"
```

Look at `Access-Control-Allow-Origin` (ACAO), `Access-Control-Allow-Credentials` (ACAC).

| Response | Verdict |
|---|---|
| `ACAO: https://artx-evil.test` + `ACAC: true` | reflected with credentials — exploitable |
| `ACAO: *` + `ACAC: true` | usually not exploitable; `*` cannot carry credentials |
| `ACAO: *` no credentials | exploitable only for public data |
| `null` accepted + credentials | exploitable from sandboxed iframes/file |
| one of `*.domain` accepted | test a lookalike subdomain (unclaimed takeover target) |

Variants: suffix/prefix tricks (`https://<domain>.evil.test`, `https://evil-<domain>`), case, port, scheme (`http://`), trailing dot, `Origin: null`.

PoC shape (never runs automatically, open by hand):

```html
<script>
fetch("https://<host>/api/me", {credentials:"include"})
 .then(r=>r.text()).then(t=>document.body.innerText=t)
</script>
```

Prove data appeared in the attacker page with the victim logged in. Record the exact `Origin` used.

## Phase 2: clickjacking

- Check `X-Frame-Options` and `Content-Security-Policy: frame-ancestors` on state-changing pages.
- Neither header, or a permissive value (`ALLOW-FROM *`, `frame-ancestors *` or a wide wildcard), is a candidate.
- Prove with a framed PoC over the real action button:

```html
<iframe src="https://<host>/account/email" width="800" height="600"></iframe>
```

- Make it real: frame the page, align the button under the cursor (opacity overlay), have a victim click and confirm the server-side change.
- A page that only shows data is low; a page that changes email/password/role is the finding.

## Phase 3: postMessage

- Enumerate handlers: `grep` the JS for `addEventListener("message"`, `postMessage`, `event.origin`, `event.source`.
- A handler without an origin check, or a check with `endsWith`/`includes`, is a candidate.
- Send a probe from the attacker page:

```js
const w = document.getElementById("t").contentWindow;
w.postMessage(JSON.stringify({type:"auth", token:"artx"}), "*");
```

- Outcomes that matter: login/token injection, navigation (`location = data.url`), HTML injection into a renderer, data request with the frame's credentials.
- Record handler file:line and the accepted payload shape.

## Phase 4: CSP quality

- Presence alone is not protection; read the directives.
- Weak: `unsafe-inline` with no nonce, `unsafe-eval`, `*`, wide `script-src` allowlists with JSONP hosts, `object-src *`, missing `base-uri`.
- A weak CSP upgrades XSS impact; hand the chain to `web-vuln-testing`, do not report CSP alone as a vulnerability.
- `report-uri`/`report-to` endpoints can leak paths; note only.

## Phase 5: client-side storage and tabnabbing

- Tokens or PII in `localStorage`/`sessionStorage`/IndexedDB, readable after logout.
- Secrets or internal hosts in JS bundles (`api-recon` harvests these).
- `target="_blank"` without `rel="noopener"` on links the user can influence.
- Autofill leakage on shared fields; note only.

## PoC hygiene

- One HTML file per finding under `poc/`, named for the class, with the target URL and the victim precondition in a comment.
- Serve with `python3 -m http.server` on a fixed port and state the port.
- Never host the PoC on a third-party site without saying so.

## False-positive traps

- Wildcard `ACAO` without credentials called exploitable.
- Clickjacking judged from headers alone while a frame-busting script exists — test the actual frame.
- postMessage origin checks that *look* weak but validate `event.source`.
- CSP absence reported as a vulnerability rather than a finding enabler.
- CORS proven with curl only, no browser credential context.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| curl | header probes | one request in flight |
| playwright | frame, postMessage and CORS proof | serial, headed when a real origin is needed |
| python3 http.server | serve local PoC | local |

## Completion self-check

- [ ] every candidate proven from a cross-origin page.
- [ ] CORS claims name the exact accepted Origin and cookie state.
- [ ] clickjacking proof includes the state change, not just the frame.
- [ ] postMessage handler cited by file:line.
- [ ] PoC files stored and the serving port recorded.