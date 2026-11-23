# ARTEX LLM shim — OAuth reverse-proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A small Go binary `cmd/artex-llmshim` that reverse-proxies ARTEX's Anthropic Messages requests to `api.anthropic.com`, rewriting auth to a subscription OAuth token, merging the oauth beta header, and prepending the Claude Code system identity — so ARTEX can drive the subscription brain without any provider-code change.

**Architecture:** One `package main` under `cmd/artex-llmshim/`. Pure request-transform functions (auth/beta/identity) are isolated and unit-tested; a `httputil.ReverseProxy` wires them into a `Director` with unbuffered streaming (`FlushInterval=-1`) and a 502 `ErrorHandler`. Anthropic-in → Anthropic-out, loopback only. No format translation, no routing table, no changes to ARTEX code.

**Tech Stack:** Go stdlib only (`net/http`, `net/http/httputil`, `net/url`, `encoding/json`, `flag`), `testing` + `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-11-23-artex-llmshim-oauth-proxy-design.md`

## Global Constraints

- Go **stdlib only** — no external dependencies.
- `httputil.ReverseProxy` with `FlushInterval = -1` (unbuffered SSE passthrough).
- Identity injection is **idempotent**: re-running on an already-prefixed body does not duplicate.
- `anthropic-beta` is a comma-separated list: adding `oauth-2025-04-20` **preserves** any betas already present, no duplicates. `anthropic-version` is never modified.
- Identity constant, verbatim: `You are Claude Code, Anthropic's official CLI for Claude.`
- Beta value, verbatim: `oauth-2025-04-20`.
- Default listen address `127.0.0.1:8788` (loopback only). Default upstream `https://api.anthropic.com`.
- The token is **never** written to logs or into any response/error body.
- Upstream responses (including a 429 cyber-safeguard) pass through **unmodified** — status and body forwarded as-is, no retries, no content rewriting to defeat the classifier.

## Review Focus

- **Pre-existing `anthropic-beta` dropped** when adding the oauth beta (e.g. ARTEX sends `prompt-caching-2024-07-31`) → upstream loses caching → Task 1 `mergeBeta` test with an existing beta.
- **Non-idempotent identity** → double prefix across multi-turn / retried requests → Task 1 `injectIdentity` idempotency tests (string and array forms).
- **`system` as an array of content blocks** not handled (only the string form) → identity missing → upstream 429 → Task 1 array-case test.
- **Streaming buffered** (default `FlushInterval`) → SSE stalls, agent loop hangs → Task 2 streaming-passthrough test.
- **Token leaks** into a log line or the 502 error body → Task 2 log-secret test + `ErrorHandler` body asserted token-free.
- **Body not restored on non-JSON input** → forwarded request has an empty body → upstream 400 → Task 2 non-JSON-passthrough test.

---

### Task 1: Pure request transforms (`transform.go`)

**Files:**
- Create: `cmd/artex-llmshim/transform.go`
- Test: `cmd/artex-llmshim/transform_test.go`

**Interfaces:**
- Consumes: Go stdlib (`net/http` for `http.Header`).
- Produces (all `package main`):
  - `const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."`
  - `const oauthBeta = "oauth-2025-04-20"`
  - `func rewriteAuth(h http.Header, token string)` — deletes `X-Api-Key` and `Authorization`, then sets `Authorization` to `"Bearer "+token`.
  - `func mergeBeta(h http.Header)` — reads `Anthropic-Beta`, splits on `,` (trim spaces, drop empties), appends `oauthBeta` if absent, writes back a single comma-joined value. Empty/missing → just `oauthBeta`.
  - `func injectIdentity(body map[string]any) bool` — ensures `body["system"]` begins with `ClaudeCodeIdentity`; returns true iff it changed `body`. Cases:
    - key absent → set `body["system"] = ClaudeCodeIdentity` (string), return true.
    - `string` s: if `strings.HasPrefix(s, ClaudeCodeIdentity)` → no change, false; else `body["system"] = ClaudeCodeIdentity + "\n\n" + s`, true.
    - `[]any` arr: if `len(arr)>0` and `arr[0]` is a `map[string]any` with `"type"=="text"` and `"text"` a string starting with `ClaudeCodeIdentity` → no change, false; else prepend `map[string]any{"type":"text","text":ClaudeCodeIdentity}`, true.
    - any other type → no change, false.

- [ ] **Step 1: Write failing tests** in `transform_test.go`:

```go
func TestRewriteAuth(t *testing.T) {
    // h has X-Api-Key "sk-old" and Authorization "Bearer old"; rewriteAuth(h,"tok")
    // → Authorization=="Bearer tok", X-Api-Key=="" (absent)
}
func TestMergeBeta_Empty(t *testing.T)        // no header → "oauth-2025-04-20"
func TestMergeBeta_PreservesExisting(t *testing.T) // "prompt-caching-2024-07-31" → contains BOTH, comma-sep, no dup
func TestMergeBeta_Idempotent(t *testing.T)   // already "oauth-2025-04-20" → unchanged, single value
func TestInjectIdentity_Missing(t *testing.T) // {} → system==ClaudeCodeIdentity, ret true
func TestInjectIdentity_String(t *testing.T)  // system:"X" → "…identity…\n\nX", ret true
func TestInjectIdentity_StringIdempotent(t *testing.T) // system already identity-prefixed → unchanged, ret false
func TestInjectIdentity_Array(t *testing.T)   // system:[{type:text,text:"X"}] → arr[0]==identity text block, len+1, ret true
func TestInjectIdentity_ArrayIdempotent(t *testing.T) // arr[0] already identity text block → unchanged, ret false
func TestInjectIdentity_OtherType(t *testing.T) // system: 42 → unchanged, ret false
```

- [ ] **Step 2: Run tests to verify they fail** — `go test ./cmd/artex-llmshim/` → FAIL (undefined funcs).
- [ ] **Step 3: Implement** the two consts and three functions in `transform.go` per the Interfaces spec above.
- [ ] **Step 4: Run tests to verify they pass** — `go test ./cmd/artex-llmshim/` → PASS.
- [ ] **Step 5: Commit** — `feat(llmshim): request-transform helpers (auth, beta, identity)`.

---

### Task 2: Proxy assembly, entrypoint, systemd example (`proxy.go`, `main.go`)

**Files:**
- Create: `cmd/artex-llmshim/proxy.go`
- Create: `cmd/artex-llmshim/main.go`
- Create: `cmd/artex-llmshim/proxy_test.go`
- Create: `docs/examples/artex-llmshim.service`

**Interfaces:**
- Consumes: Task 1 (`rewriteAuth`, `mergeBeta`, `injectIdentity`, `ClaudeCodeIdentity`).
- Produces (all `package main`):
  - `func newProxy(upstream *url.URL, token string) *httputil.ReverseProxy` — `Director`:
    sets `req.URL.Scheme/Host = upstream.Scheme/Host` and `req.Host = upstream.Host` (path/query left as received, so `/v1/messages` forwards verbatim); calls `rewriteAuth(req.Header, token)` and `mergeBeta(req.Header)`; then rewrites the body (read all; `json.Unmarshal` into `map[string]any`; on success `injectIdentity`, re-marshal, set `req.Body=io.NopCloser(bytes.NewReader(b))`, `req.ContentLength=len(b)`, `req.Header.Set("Content-Length", …)`; **on unmarshal failure restore the original bytes** as the body). Sets `FlushInterval = -1` and an `ErrorHandler` that writes `502` + `{"error":"upstream unreachable"}` and logs without the token.
  - `type config struct { addr, upstream, tokenFile string }`
  - `func loadToken(tokenFile string) (string, error)` — if `tokenFile != ""` read+trim it (error on read failure or empty); else `os.Getenv("ARTEX_SHIM_TOKEN")`; empty from both → error `"no token: set ARTEX_SHIM_TOKEN or -token-file"`.
  - `func run(cfg config) error` — `loadToken`; parse `cfg.upstream` via `url.Parse`; `newProxy`; `http.ListenAndServe(cfg.addr, proxy)` (a request logger wrapping it is fine — method/path/status/latency only).
  - `func main()` — `flag` parsing (`-addr` default `127.0.0.1:8788`, `-upstream` default `https://api.anthropic.com`, `-token-file` default `""`); `if err := run(cfg); err != nil { log.Fatal(err) }`.

- [ ] **Step 1: Write failing tests** in `proxy_test.go` (each spins an upstream `httptest.Server` capturing the received request; drive `newProxy(upstreamURL, "tok")` via `httptest.NewServer(proxy)` or `proxy.ServeHTTP`):

```go
func TestProxy_AuthRewritten(t *testing.T)   // upstream sees Authorization=="Bearer tok", no X-Api-Key
func TestProxy_BetaMerged(t *testing.T)       // client sends Anthropic-Beta: prompt-caching-2024-07-31 → upstream sees both
func TestProxy_IdentityInjected(t *testing.T) // client body system:"hi" → upstream body system starts with ClaudeCodeIdentity
func TestProxy_NonJSONBodyForwardedIntact(t *testing.T) // client body "not json" → upstream receives it byte-for-byte
func TestProxy_StreamingPassthrough(t *testing.T) // upstream writes 3 SSE chunks with Flush between; client reads all 3 in order
func TestProxy_429PassedThrough(t *testing.T)  // upstream returns 429 + body → client gets 429 + same body
func TestProxy_502OnUpstreamDown(t *testing.T) // upstream URL unreachable → client gets 502, body has no token
func TestProxy_TokenNotLogged(t *testing.T)    // log.SetOutput(buf); drive a request; buf must not contain "tok"
func TestLoadToken_Missing(t *testing.T)       // empty env + no file → error
func TestLoadToken_FileWins(t *testing.T)      // -token-file set → its trimmed contents, over env
```

- [ ] **Step 2: Run tests to verify they fail** — `go test ./cmd/artex-llmshim/` → FAIL.
- [ ] **Step 3: Implement** `proxy.go` (`newProxy`) and `main.go` (`config`, `loadToken`, `run`, `main`) per the Interfaces spec. For streaming, rely on `FlushInterval = -1`; for the body rewrite, always leave `req.Body` readable (rewritten JSON on success, original bytes on parse failure).
- [ ] **Step 4: Run tests to verify they pass** — `go test ./cmd/artex-llmshim/` → PASS; `go vet ./cmd/artex-llmshim/` clean; `go build ./cmd/artex-llmshim/` succeeds.
- [ ] **Step 5: Write `docs/examples/artex-llmshim.service`** — a systemd unit modeled on `docs/examples/artex.service`: runs the built binary, `EnvironmentFile=/etc/artex-llmshim.env` (holding `ARTEX_SHIM_TOKEN=`), `ExecStart=<path>/artex-llmshim -addr 127.0.0.1:8788`, restart-on-failure, loopback bind. No test (static config).
- [ ] **Step 6: Commit** — `feat(llmshim): reverse-proxy, entrypoint, systemd example`.

---

## Follow-on (not this plan)
Wire ARTEX to the shim at runtime (`ARTEX_LLM_BASE_URL=http://127.0.0.1:8788`, Anthropic format, dummy `ANTHROPIC_API_KEY`, real Opus model id) and a live smoke test — manual, operator-run. Draft PR against `main`. Note: offensive/hunting content still returns 429 at the API until Defense Access/CVP; the shim is plumbing for non-flagged traffic now and a flip-ready path post-CVP.
