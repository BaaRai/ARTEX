# Managed execution scope and access audit

Status: **proposed; not implemented by this document**.

Tracking: [access audit #553](https://github.com/BaaRai/ARTEX/issues/553) and
[sandbox-runtime backend #554](https://github.com/BaaRai/ARTEX/issues/554).
This is PR 1 of the proposed sequence. It changes no runtime behavior or defaults.

## Problem and success criteria

An operator investigating an unexpected URL access needs to find the observed
attempt, its task and tool invocation, the effective policy and approval, and any
response or target-side evidence. A matching string in a task transcript is not
evidence of network access. Missing captures are not proof of non-access.

At the same time, the authorized destination boundary should apply to actual
connections from managed tools and their children, including connections whose
destinations were not declared in the tool arguments. This requires execution
containment and deterministic egress enforcement, alongside semantic review.

Success means:

- Managed direct egress cannot exceed an operator-authorized policy.
- Judge approval cannot override a deterministic denial.
- A URL/time-window investigation explains observation, attribution and gaps.
- Full-body recording is optional; required metadata is independent of it.
- Remote second-hop actions have separate review and evidence semantics.
- Unsupported coverage and reduced isolation are visible, never silently promoted
  to an enforcement guarantee.

## Baseline and existing components

The review baseline is ARTEX
[`3cb0638`](https://github.com/BaaRai/ARTEX/tree/3cb06383fd99ffe7686abe1d375de11da7cfad54).
This proposal extends existing controls rather than replacing them:

- `guard` and `intercept`: tool rules, judge, target approval/pins and trace.
- `server/execpriv.go` and `server/isolation*`: privilege dropping and startup
  isolation checks.
- `traffic`: HTTP, SOCKS, TCP/UDP evidence, rewrite history, HAR and archives.
- `egress`: pacing/noise governance, which is not itself a task allowlist.

Related discussions: #421, #423, #347, #422 and #440. Revalidate integration points
against the current main branch before each implementation PR.

## Trust boundaries

```text
operator-approved policy revision
               |
tool arguments -> deterministic rules + semantic judge + approval
               |
ARTEX execution manager (trusted session/provenance)
               |
unprivileged tool + children inside srt
               |
authenticated ARTEX egress gateway
  policy -> rate control -> final dial -> metadata / optional payload evidence
               |
          authorized target A
               |
       remote action A -> B
       [outside the local srt boundary]
```

The native Go HTTP path must use the same policy/gateway contract even though it
does not run inside a child-process sandbox. Control-plane traffic is separate.
The gateway and audit writer are trusted services; children cannot modify their
configuration, credentials, event store or policy revision.

### SSRF, RCE and lateral movement

A permitted request to A may cause A to contact B. Neither a local network
namespace nor a proxy on the ARTEX host can constrain A's own network. A remote MCP
service, device or VM also needs an independently managed executor to claim that
coverage.

Use deterministic validation for recognizable secondary destinations and typed
tool parameters. Keep judge review for payload intent, indirect effects and
multi-step behavior. In strict mode, an unresolvable secondary scope or material
scope expansion requires operator approval or denial. A known out-of-policy
destination requires an authorized policy revision, not just a judge's approval.

An operator authorization should identify target scope, action class, duration and
limits. The model cannot grant its own exception. Where the test environment is
controlled, target-side network policy provides an additional enforceable boundary.

Arbitrary remote code cannot be completely interpreted by argument parsing or a
judge. Neither mechanism can guarantee that every hidden secondary destination is
identified. Local observation, authorized callback/target logs, and inference must
remain distinct evidence classes. A response from A alone proves neither access
to B nor the absence of it.

## Proposed policy contract

Use a versioned server-owned policy with at least:

| Field | Meaning |
| --- | --- |
| policy ID, revision, digest | Stable identity and exact effective version |
| task/run binding | The execution this grant covers |
| mode | Explicit observe or enforce; legacy unmanaged state is not enforcement |
| target rules | Typed hostname/IP/CIDR, port and protocol permissions |
| address constraints | Permitted/denied final destination addresses and approved pins |
| request constraints | Optional HTTP method/path rules with declared parsing semantics |
| infrastructure grants | Narrow operational services, separate from target authorization |
| indirect-action grants | Approved remote actions/secondary scope where expressible |
| provenance | Operator/approval reference, creation and expiration, revocation state |
| required capabilities | What the selected runner/gateway must demonstrably enforce |

Policy rules:

1. Explicit deny wins. In enforce mode missing/empty target permissions deny
   target access; they do not mean unrestricted access.
2. Scope is operator-owned. Judge/model/target content cannot widen it.
3. Domain authorization and destination-address constraints are separate. Exact
   labels, suffix boundaries, IDNA, trailing dots, IPv4/IPv6, ports and protocol
   defaults need documented semantics and table-driven tests. Preserve existing
   rule behavior until an explicit migration; do not silently convert URL
   substring rules into canonical path authorization.
4. Check the final dial address, not just the DNS answer used during approval.
   New DNS answers, connections and redirect hops are reevaluated. A permitted
   domain does not automatically authorize an excluded IP.
5. Parent proxies that resolve the destination must enforce equivalent address
   policy. Strict IP-sensitive policies fail if that capability is unavailable;
   hostname-only routing must not be advertised as pinned-IP enforcement.
6. Reevaluate requests on reused HTTP connections. Revocation stops affected
   executions and connections, including persistent tunnels, within a documented
   bounded interval. Connection pools cannot outlive authorization unnoticed.
7. Path constraints require HTTP visibility. An opaque TLS/SOCKS tunnel can only
   claim the constraints actually enforced. If a required path constraint is not
   observable, refuse that profile or require an explicit changed policy.
8. Scope approval is necessary but not sufficient to approve an operation:
   destructive behavior, data handling and indirect actions retain their own
   rules/judge/operator checks.

DNS resolution and package/recon/callback endpoints require explicit operational
grants. LLM provider and server database credentials remain outside the tool
sandbox. An infrastructure grant must not become general testing authorization
or unrestricted CONNECT access.

## Runner and gateway contracts

A managed runner receives executable/argv, working directory, filtered environment,
filesystem permissions, limits, cancellation, a policy reference and an opaque
server-created session binding. Shell parsing remains explicit for shell tools.
The runner returns execution lifecycle events, exit status and capability/coverage
details. Policy transport must not put secrets in argv or tool output.

Start with Linux shells, custom command tools and task-scoped local MCP processes.
Preserve existing privilege dropping. Restrict filesystem reads explicitly:
wrapping a command does not automatically conceal every server secret. Keep runtime
configuration read-only to the child; allow only task workspaces and necessary
runtime paths. Do not expose broad IPC, host Docker sockets or shared task secrets.

The gateway authenticates execution sessions before accepting requests. It resolves
policy from a server-side binding rather than trusting a task ID header. Prefer
per-run protected endpoints/credentials; demonstrate isolation between concurrent
tasks, including sibling processes. A long-lived MCP process can be attributed to
a run, but attributing individual concurrent requests to tool calls requires an
actual correlation mechanism. Otherwise label attribution ambiguous.

If containment, policy lookup or required audit durability fails, enforce mode
does not start new work. Never retry unsandboxed automatically. Cancellation and
revocation terminate the whole managed process tree and close its sessions.

### sandbox-runtime integration choice

Evaluate [srt source at `0bbebed`](https://github.com/anthropics/sandbox-runtime/tree/0bbebed0aa9a8e628dc4df8bc30293550a1988eb).
It is a research preview; a source review is not proof of release compatibility.
Select and pin a tested release/artifact and digest, including its Node/runtime
requirements.

Preferred topology for the initial spike: srt contains the process tree and routes
it only to an authenticated ARTEX HTTP/SOCKS gateway, using its external-proxy
configuration. ARTEX retains policy, pacing, evidence and existing TLS capture
ownership. External-proxy mode delegates filtering to that proxy; this topology
must be tested, not inferred from a successful `curl` through `HTTP_PROXY`.

Current source also offers domain/port rules, `strictAllowlist`, resolved-address
restrictions, and request-filter/TLS termination hooks. These are useful alternative
integration points, not a reason to duplicate policy authorities or TLS interception.
The source documents that resolved-address checks do not apply when a parent/MITM
proxy resolves the target. Final-hop policy must therefore be explicit.

References:
[configuration](https://github.com/anthropics/sandbox-runtime/blob/0bbebed0aa9a8e628dc4df8bc30293550a1988eb/src/sandbox/sandbox-config.ts),
[request filter](https://github.com/anthropics/sandbox-runtime/blob/0bbebed0aa9a8e628dc4df8bc30293550a1988eb/src/sandbox/request-filter.ts),
[HTTP proxy](https://github.com/anthropics/sandbox-runtime/blob/0bbebed0aa9a8e628dc4df8bc30293550a1988eb/src/sandbox/http-proxy.ts).

## Audit event contract

Proposed metadata is append-oriented and independent of request/response body
retention. Use idempotent event IDs and distinct attempt IDs.

| Group | Proposed fields |
| --- | --- |
| Identity | event ID, attempt/connection ID, parent retry/redirect reference |
| Time | observed-at, received-at, source clock/precision |
| Execution | task, exploration, intent, run, tool-call, runner/session IDs |
| Attribution | trusted source and confidence: exact, run-only, ambiguous, unknown |
| Actors | initiator, executor and approver references, when known |
| Policy | policy revision/digest, rule and approval IDs, verdict and reason |
| Destination | host, actual IP/port when known, protocol, method and URL when visible |
| Observation | stage, local/upstream outcome origin, error, timing and known bytes |
| Evidence | capture/archive references, source kind and integrity metadata |
| Coverage | visibility level, sensor/backend version and related gap references |

Observation stages distinguish declared intent, policy decision, connect attempt,
connection established, request forwarded, response headers observed, stream
completion/failure and imported evidence. A response-header hook is not a completion
event. Unknown fields stay null; no synthetic success status.

A gateway denial and a target response are different outcomes even if both use HTTP
403. Direct socket attempts blocked by OS containment might not produce a
per-destination proxy event; do not claim complete denied-attempt detail if the
runtime offers only an execution error. Record that coverage limit.

Raw and normalized URL forms are separate. Safe display/search redacts userinfo,
sensitive query values and configured headers. Raw evidence is separately
authorized; access and exports are audited. URL normalization for search never
changes request bytes or policy meaning. Preserve original/rewritten request
references when capture rules intentionally mutate traffic.

Retries and redirects use new attempt IDs. Historic capture imports preserve the
source timestamp and add ingestion time/provenance. Transcript matches are
investigation candidates, not retroactively created network events.

### Storage and failure semantics

Start by evaluating PostgreSQL metadata alongside existing task data, referencing
the current traffic store for optional bodies. Benchmark before adding another
storage service. Index time, destination and execution identifiers; substring
search needs a bounded index/query design rather than unbounded scans.

Use a bounded durable spool, idempotent ingestion, explicit backpressure and health
events. If neither storage nor spool can accept required records, strict audited
execution stops accepting new work. An outage has a recorded coverage interval,
not an unexplained hole. Payload truncation/body recording failure must not be
confused with successful full capture.

Retention is configurable and distinct for metadata and bodies. Task archive/delete
does not silently erase the incident trail: retain audit references/tombstones for
the retention period and expose authorized archive lookup. Administrative deletion
and redaction remain explicit audited operations. Append-oriented storage is not
a claim of cryptographic tamper resistance.

## Operator workflow

Search by URL/domain/IP/path fragment and time window/timezone. Support exact host
and URL versus substring, method, outcome/status, task/run and visibility filters.
Use bounded keyset pagination.

Each result explains what matched and what was observed. The timeline links the
execution, decision/approval, retry/redirect chain, capture and remote evidence.
Searching or opening evidence must not make a new target request.

Coverage accompanies results: full HTTP visibility, opaque connection metadata,
remote/unmanaged execution, recorder outage or archived-only evidence. “No matching
observed access” is appropriate when evidence is incomplete; “never accessed” is
not. A shared login does not support named-human attribution.

## Compatibility and cost

| Surface | Initial treatment / acceptance boundary |
| --- | --- |
| Linux shell and children | Managed only after containment and bypass-resistance tests |
| Local MCP | Task/run-owned sessions first; concurrent tool-call provenance tested |
| Native Go HTTP | Policy and audit instrumentation required independently of srt |
| HTTP and redirected HTTP | Per-request policy and attempt chain |
| HTTPS with capture | Explicit trust/inspection profile; preserve existing capture behavior |
| Pinned TLS or mTLS | Opaque or unsupported profile; no invented URL/path visibility |
| SOCKS/raw TCP | Destination-level policy; no universal HTTP semantics |
| UDP, ICMP, raw SYN, QUIC | Separate capability/runner work; strict unsupported profiles fail |
| Remote MCP, VM, container, Android | Separate enrollment and evidence; local wrapping is insufficient |
| Parent proxy | Final-hop address enforcement must be demonstrated |
| Windows/macOS hosts and nested containers | Future/conditional support; publish tests before claiming parity |

TLS interception changes trust and may affect signed or intentionally unusual
requests. Preserve raw targets and expose ambiguous parsing. Do not normalize test
traffic silently. WebSocket/SSE streams need lifecycle events and resource limits,
not endless body buffering. Avoid automatically broadening privileges or enabling
all Unix sockets to make a tool work.

Measure runner startup, CPU/memory, throughput/p95 latency, spool capacity, metadata
and payload storage growth, query p50/p95 and concurrent attribution. Illustrative
only: one million 1 KB metadata records is approximately 1 GB before indexes and
replication; 50 KB bodies per request add approximately 50 GB. Retention defaults
must follow measured workload and operator needs.

## Delivery sequence and exit gates

| PR | Focus | Dependencies | Acceptance |
| --- | --- | --- | --- |
| 1 | This ADR, contracts and support boundaries | None | Reviewable design; no runtime behavior change |
| 2 | Audit metadata/provenance, durable ingestion and health/coverage | 1 | Denied/failed/forwarded events, capture-off, restart, duplicate and spool-full tests |
| 3 | Policy adapter at managed egress/final dial | 1, 2 | Domain/port/IP, DNS, redirect, upstream proxy, revocation and deny precedence tests |
| 4 | Pinned srt Linux runner, shell/custom commands and scoped local MCP | 3 | Direct sockets/children/proxy-env changes cannot bypass; cancellation and task isolation |
| 5 | Search API/UI, timeline, retention, archive lookup and export | 2 plus producers | URL/time investigation with correct attribution, authorization and gap reporting |
| 6 | Indirect-action scope review and evidence correlation | 1, 2 | Known secondary destinations checked; uncertain actions reviewed; remote evidence labeled |
| 7 | Packaging, diagnostics, compatibility and rollout benchmarks | 3–6 | Reproducible support matrix, migration and rollback procedure |

Implement focused PRs in order; PR 5 and PR 6 may proceed after their dependencies.
Keep within the repository's limit of seven open PRs per contributor. Code PRs
must describe actual validation and avoid claiming coverage from unit mocks alone.

Use synthetic targets and authoritative test DNS. Cover allowed/denied domain and
port, CIDR/IPv6, changed DNS, disallowed redirects, hostname/IP mismatch, invalid
provenance, simultaneous tasks, child processes, removed proxy environment, native
HTTP, parent proxies, TLS pinning/mTLS, opaque protocols, unavailable namespaces,
gateway failure, full spool, cancellation and revocation. A mock A-to-B server-side
request must demonstrate the remote trust boundary without operational exploit
payloads. Preserve existing capture/rewrite/archive/noise-governor regressions.

## Rollout and open decisions

First release is opt-in for a controlled Linux task set. Observe mode helps
compatibility evaluation but cannot certify future enforce-mode support. Required
capabilities are checked before task start. Any default change is a separate
reviewed decision.

Migrations remain additive. Rollback stops affected managed tasks and restores
compatible components; it does not silently resume them outside the sandbox or
delete audit history.

Resolve during implementation reviews:

- Is the external ARTEX proxy topology supported by the selected released srt
  artifact, and does it preserve authentication and final-dial enforcement?
- Which execution surfaces can satisfy per-run and per-tool-call attribution
  without unacceptable MCP startup overhead?
- Which scope syntax/migration and URL ambiguity semantics should be exposed?
- What revocation bound and durable-spool behavior can the implementation meet?
- Which TLS/non-HTTP profiles are initially supported, and which require a
  separate executor?
- What retention and performance targets follow from benchmark measurements?
