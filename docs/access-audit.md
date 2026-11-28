# Access audit (implementation PR 2 of 4)

An operator investigating an unexpected URL can use **Traffic → Access audit**
or authenticated `GET /api/access-audit`. Select a local time interval and search
a full URL, literal path substring, exact hostname, task ID or source. API times
are RFC3339 with an explicit timezone; intervals are `[from, to)`. The default is
24 hours, the maximum interval 31 days, and pages contain at most 200 events.
`before` is the last event ID returned as `next`; keep the other filters and time
bounds fixed when paging. New observations do not shift older pages.

This is the first implementation in the four-PR plan (#553, #554, #555). It is
an observational feature, not destination enforcement or a complete network
ledger. The gateway/action authorization and Go/bubblewrap backend remain the
next two PRs. This PR must remain **Draft** after automated tests; only the
requester's explicit approval following manual scenario testing permits ready
for review. The design PR can be ready independently.

## Evidence and coverage

| Source | What it establishes | Attribution / limitations |
| --- | --- | --- |
| `native_http` | Native HTTP custom tools and enrichment: transport attempt, returned response headers/status, or transport error. Each redirect/retry is separate. | Trusted task/conversation/run context where available. Placed inside the governor: cache hits are not new network observations. A proxy or synthetic transport response is not proof of origin receipt. |
| `webfetch_trace` | WebFetch invocation, successful request write, first response byte, or write error using Go HTTP trace callbacks. | Task/conversation/run context. No status/body parsing. With explicit proxy and direct fallback enabled, only `tool_attempt` is recorded: Norma can restart the original URL without a request callback, so exact-URL network attribution would be ambiguous. |
| `execution_proxy` | HTTP request received by the local proxy (`proxy_request`), response headers/status, or transport error. curl, Python urllib/requests and other proxy-aware Bash subprocesses are observed even with full capture off. | Opaque per-execution credential issued from trusted task/worker context; stripped before forwarding. HTTP CONNECT itself is not a resource visit. Exact Bash tool-call ID is unknown. |
| `recording_proxy` | HTTP response headers/status observed by the recording proxy, including streamed responses, or a request error. | No inferred task/call identity. No attribution from user-supplied headers. Opaque CONNECT/passthrough is uncovered. Full-body capture remains opt-in; metadata collection is independent. |
| `extension_ingest`, `har_import` | An authenticated operator supplied an external exchange. | `reported_exchange`, never a local network observation. Observation time is server ingestion time, not the external event's claimed time. Imports cannot supply task/source attribution. |

A request can generate several events. `request_id` groups native transport or
WebFetch trace events when available, not cross-producer requests. Unknown
attribution remains empty. Run IDs reuse the interceptor's per-prompt trace;
tool call IDs are retained only when the existing interceptor correlation is
exact. No guess based on host, URL or nearest timestamp is made.

Shell clients that bypass/override the proxy, NO_PROXY exclusions, MCP traffic outside the proxy, raw TCP/UDP, opaque tunnels, remote SSRF/RCE second
hops, requests rejected before the observed transport, and historical traffic
before installation are outside this PR's coverage. Existing full captures are
not backfilled or relabeled. A URL mentioned in a returned page is not recorded
as an access. **No match means no matching retained observation, not no access.**
The response always advertises `coverage: partial`.

## Execution collection and TLS

The existing local HTTP proxy must be configured (`-proxy`; the usual loopback
listener). Each worker/planner/execution-chat run receives an opaque proxy URL
and the public proxy CA through its Bash environment. HTTP(S)_PROXY (both cases),
ALL_PROXY, CURL_CA_BUNDLE, SSL_CERT_FILE and REQUESTS_CA_BUNDLE cover normal curl,
Python urllib and requests usage. No command-text matching is used. The global
egress proxy remains the local proxy's upstream. Browser MCP, native HTTP and
WebFetch keep their existing routing choices and their existing observations.

A capability resolves only to server-registered execution identity and is revoked
when the run ends. Task headers and invented credentials cannot create attribution.
It is not tamper-proof isolation: processes that can read another execution's
environment can copy its capability; detached children after revocation lose
attribution. Individual Bash call IDs are not guessed. `request_id` joins the
proxy's request/response/error observations; a proxy receipt is not proof that
the destination received the request.

HTTPS inspection requires trusting the public local CA and adds proxy overhead.
Managed proxies verify upstream TLS by default. Self-signed/untrusted targets
fail verification unless the operator deliberately sets
`ARTEX_PROXY_INSECURE_TLS=1` (applies to this proxy, including full capture).
A client's `curl -k` cannot disable verification on the proxy's upstream leg.
Certificate-pinned clients, explicit proxies, `--noproxy`, custom transports and
NO_PROXY exclusions can bypass or fail inspection; no hard network enforcement
is claimed. Raw TCP/UDP and remote SSRF/RCE pivots still need the later gateway,
Go/bubblewrap controls and judge policy. Disabling the proxy listener removes
this execution coverage; existing native-client audit remains available.

With capture off, requests and responses stream through without saving bodies,
headers, blobs or stream messages. A request begun with capture off stays
uncaptured if recording is enabled while it runs. Turning recording off drains
current persistence operations; an off/on cycle cannot resume storing an old
stream's messages. Existing captures are retained under their original policy. Explicit manual
Replay still saves its exchange and applies requested rewrite rules; imports
remain explicit operator writes. These are not automatic worker capture.

## Storage and failure behavior

Audit is independent of the full traffic capture toggle, in
`traffic/_index/access.sqlite`, with mode 0600, within the existing protected
workspace prefix. It records no request/response bodies, headers, userinfo,
query keys/values, URL fragments or raw error messages. Search of a pasted URL
uses the same redaction; query-specific visits cannot be distinguished. Paths
can still contain secrets; this is operator-only data, not a safe public export.
The UI renders URLs as text, never HTML, Markdown, or automatically opened links.
The API uses the existing authenticated handler and returns `Cache-Control:
no-store`. It is a global operator view, explicitly across companies, because
attributing unknown proxy traffic by domain ownership would be misleading.

Retention is 30 days and at most 500,000 events; pruning runs at startup and on
each committed batch. SQLite reuses freed pages; this is a record-count bound, not a fixed
byte budget. No background copy of removed rows is kept. An idle database can
retain expired rows until its next startup/write. Audit data follows its own
retention: deleting full captures, a task, or archiving a task does not delete
these global records. They are not embedded in task archives or restored from
them. Task IDs may therefore reference deleted/archived tasks. Back up this file
separately if the investigation needs longer retention; do not assume a task
archive includes it. Stop ARTEX before copying or removing the database and its
SQLite sidecars. Filesystem backups may retain older data independently.

A bounded queue (1,024 events) drains through one background writer, with up to
64 events in each durable SQLite transaction and a two-second batch deadline.
Network callbacks do not wait on disk I/O. Queue overflow and failed batches
are counted; queries wait for earlier queued events before reading, and normal
shutdown drains the queue. Failures do not change authorization decisions. `write_failures_since_start` and
`process_started_at` expose loss for the current process. They are not a durable
historical health ledger, and crashes/restarts may leave unreported gaps, including uncommitted queued
events. The
API fails explicitly if the store cannot be queried. Startup fails if the audit
store cannot open, rather than silently claiming it is active. The later strict
gateway must define its own fail-closed contract; this PR does not implement it.

## Manual acceptance on an isolated instance

1. Keep full capture **off**. Run a real worker task using Bash curl and Python
   urllib/requests over HTTP and HTTPS, plus WebFetch/native HTTP, against
   an owned test server with `/start` redirecting to `/k5613?token=fixture`.
   Search `k5613` within the selected time window. Expect `execution_proxy`, WebFetch trace events
   and native HTTP observations with trusted task/run identity, without `token`
   or response bodies. Full capture should remain empty.
2. Request an unavailable test port. Expect attempts/errors, never a fabricated
   successful response. A denied pre-transport call may have no audit event;
   consult existing intercept records for its decision.
3. Enable full capture and use an external client through its proxy. Expect
   `recording_proxy` with unknown task identity. Import a HAR/extension exchange;
   expect `reported_exchange`, not a claim ARTEX visited it.
4. Put another URL and prompt-like instructions in a returned page. They must
   not create access events by mere mention. This tests audit provenance, not
   model immunity to prompt injection; retain existing untrusted-content rules.
5. Verify local-time/day boundaries, exact host vs similarly named hosts,
   task/source filters, literal `%`/`_` searches and cursor pages. Pasting a full
   credential/query-bearing URL must find its redacted path without exposing
   those credentials. Unknown URLs and proxy-bypassing shell calls must not
   be presented as proof of absence.
6. Restart the isolated instance and repeat the query. Delete its full captures
   and archive a test task; retained audit records should still be searchable,
   while the UI continues to advertise partial coverage and separate retention.
7. Compare target-server receipts with proxy observations, including redirects,
   errors, large bodies, SSE, capability revocation and capture off/on races.
8. Inspect this feature with the actual business workflow and report gaps before
   authorizing readiness. Do not deploy to or replace the existing running
   service merely to exercise these checks.

Rollback: run the previous application build. The audit database is separate
from existing PostgreSQL/traffic schemas; it is ignored by earlier builds. Keep
or remove it deliberately according to the investigation's retention needs.
