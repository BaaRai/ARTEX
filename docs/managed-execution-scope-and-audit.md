# Managed execution scope and access audit

Status: **proposed; not implemented by this document**.

Revised 2026-11-27: select an ARTEX-owned Go/bubblewrap Linux backend.
Implementation and runtime qualification remain follow-up work.

Tracking: [access audit #553](https://github.com/BaaRai/ARTEX/issues/553) and
[Go/bubblewrap backend #554](https://github.com/BaaRai/ARTEX/issues/554).
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
- Deceptive target content cannot grant permissions or be promoted directly into
  verified findings; execution and investigation have enforceable budgets.
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

A focused integration review on 2026-11-27 checked packaging, startup isolation
and budgets at [`e5c7bfc`](https://github.com/BaaRai/ARTEX/tree/e5c7bfcbdd893db26c08f0b004e0169b4ab6ca85).
This supplements the earlier full behavior baseline. Current main already has
task/intent token and turn budgets in `agent/budget.go` and `server/engine_budget.go`.
Extend these and the existing task clock; do not create competing accounting.
New gateway/OS limits and evidence events must integrate with their lifecycle.

## Trust boundaries

```text
operator-approved policy revision
               |
tool arguments -> deterministic rules + semantic judge + approval
               |
ARTEX execution manager (trusted session/provenance)
               |
unprivileged tool + children inside the Go/bwrap Linux backend
               |
authenticated ARTEX egress gateway
  policy -> rate control -> final dial -> metadata / optional payload evidence
               |
          authorized target A
               |
       remote action A -> B
       [outside the local execution boundary]
```

The native Go HTTP path must use the same policy/gateway contract even though it
does not run inside a child-process sandbox. Control-plane traffic is separate.
The gateway and audit writer are trusted services; children cannot modify their
configuration, credentials, event store or policy revision.

### What is inside a worker's sandbox

The sandbox boundary is the worker's tool execution environment, not its entire
logical agent loop. Model calls, scheduling, judge/approval and audit stay in the
trusted control plane. Managed shell/custom tools and local MCP process trees run
inside their assigned execution boundary. Native HTTP uses the policy gateway;
file tools must use an isolated executor or an explicitly constrained file service.
Inventory every process, file and network entry point so wrapping Bash does not
leave an alternative host-level path. Unsupported paths fail a strict profile.

Prefer separate workspaces and execution sessions for each worker run. A persistent
runner versus invocation-local processes is a lifecycle/performance decision for
the implementation; this ADR does not claim one permanent sandbox per worker.
Wrapping a process does not isolate an individual goroutine inside the ARTEX server.
Cross-run credentials, writable files and IPC must not defeat task separation.

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

### Deception resilience and counter-exploitation (anti-honeypot)

The intended benefits are reduced local damage from hostile target content,
traceable evidence when an agent is misled, and bounded wasted effort. A sandbox
does not automatically recognize honeypots, hide source IP/request fingerprints,
or stop a permitted target from recording requests.

| Scenario | Proposed control | Remaining limit |
| --- | --- | --- |
| Hostile content induces a secret read or unauthorized callback | Explicit filesystem policy, protected host secrets and deterministic egress | Tool-readable data can still leave through an allowed endpoint |
| Downloaded content attacks a browser/parser/tool | Untrusted artifact handling, applicable code/tool approval, isolated process tree | No guarantee against kernel/runtime compromise; no implicit safe-to-execute verdict |
| A service fabricates a finding or misleading success response | Evidence provenance, corroboration, contradictions and judge/operator review | A model assessment is not proof of deception or a vulnerability |
| Crawl/output/download traps consume resources | Task/run budgets enforced by gateway, supervisor and scheduler | Limits and overshoot must be measured; bwrap alone is not a quota manager |
| An unusual but legitimate service looks suspicious | Record evidence and uncertainty; allow an auditable operator disposition | Suspicion alone must not produce a confirmed honeypot label |

Treat target instructions as untrusted inputs. The sandbox can block a resulting
disallowed action, but does not prevent the model seeing or believing malicious
content. Preserve existing prompt-injection controls and tool/action review.

Use existing evidence before requesting new verification. Corroboration must have
known provenance and remain within authorized scope, approvals and budgets. A
target-supplied credential or suspected honeytoken is not automatically used or
validated against another service. A denial, error or unusual response is not by
itself proof of a honeypot. Do not add stealth/evasion or out-of-scope probes as a
response to suspicion.

Hard policy/budget limits stop affected execution and further scheduling.
Evidence-based suspicion can pause a run for operator review under a documented
policy; ordinary failures remain ordinary failures. Preserve available evidence,
usage and the stop reason before cleanup where possible, without delaying the
enforcement action. Resumption requires authorized disposition and, when needed,
an approved policy/budget revision. A model cannot raise its own limits.

These controls are planned capabilities. They do not extend local containment to
remote SSRF/RCE second hops or guarantee that deceptive content is detected.

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
| budgets and stop policy | Task/run resource limits, aggregation, enforcement owner and authorized resumption |

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

### Resource budgets and enforcement ownership

| Budget | Enforcement owner |
| --- | --- |
| Requests, network bytes, connection/redirect limits | Managed egress gateway, including persistent streams |
| Process count, CPU/memory, wall-clock, output/download/workspace size | Runner supervisor with supported OS accounting/limits and I/O controls |
| Model tokens/cost, tool steps and overall investigation time | Trusted ARTEX scheduler/control plane |

Account for children and aggregate simultaneous workers at task level; per-process
limits alone may not bound a whole task. Configure documented thresholds, bounded
overshoot and actions. A strict profile must not advertise a required quota it
cannot enforce. Cancellation/stop closes execution sessions, terminates children
and prevents new scheduling rather than simply returning a warning to the model.
Persist budget configuration, observed usage, enforcement authority, stop reason
and authorized resumption as audit events. bubblewrap is not assumed to supply these
limits; capability tests determine which supervisor/OS mechanisms are required.

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

### Selected Linux backend: ARTEX-owned Go/bubblewrap adapter

Implement a narrowly scoped Go supervisor invoking a supported external `bwrap`
binary. Do not fork bubblewrap, reimplement kernel isolation, or add a Node/SRT or
Fence dependency for this backend. Existing Node-dependent tools remain unaffected.
The detailed [backend plan](bubblewrap-backend-plan.md) defines responsibilities,
qualification gates and the incremental PRs.

The choice follows ARTEX's need for one policy authority, task-bound provenance,
explicit failure behavior and complete cancellation. It is not based only on
language alignment or the presence/absence of Node on one host. A higher-level
runtime can save compatibility work, but still needs adaptation to those contracts.
ARTEX accepts responsibility for this adapter's security regressions and supported
Linux matrix; a thin adapter is not automatically a secure sandbox.

The Go supervisor constructs structured argv and an explicit environment, validates
trusted executable/mount paths, prepares private workspaces and resource domains,
and owns the full process/bridge lifecycle. Preserve existing privilege dropping;
do not expose controller credentials, unintended file descriptors, host home/root
trees or broad IPC sockets. Read-only access to a secret still discloses it.
Test path/symlink races and cross-task access, not only path normalization.

The managed Linux profile requires separate network and PID namespaces. A protected
task-private bridge exposes only the authenticated ARTEX gateway. All permitted
target traffic also goes through that gateway; it owns destination policy, final
dial, pacing, metadata and optional TLS capture. Environment proxy variables alone
do not enforce containment. Missing namespaces or required resource mechanisms
reject the strict profile; never silently remove isolation to make a tool work.

Resource enforcement must cover descendants before untrusted code runs. Validate
cgroup v2 delegation or another demonstrated host mechanism plus output/storage
limits; launching and moving a process into limits afterwards leaves a race.
Cancellation/revocation stops the process tree and existing streams, including
CONNECT tunnels. Killing only the direct child or closing only a proxy listener
does not establish whole-task cleanup. Record bounded cleanup and quota overshoot.

### Dependency acceptance and maintenance

The [review of PR #555](https://github.com/BaaRai/ARTEX/pull/555#issuecomment-7193053269)
raised the proposed Node-based research-preview dependency. The selected Linux
plan no longer introduces that dependency. Design acceptance still does not qualify
the new backend: implementation PRs need supported bwrap/kernel/distro/container
combinations, reproducible package/artifact provenance, capability tests,
license/SBOM review, measured overhead, diagnostic behavior and named maintenance
and rollback ownership before merge.

Bubblewrap's [v0.13.1 security model](https://github.com/containers/bubblewrap/blob/v0.13.1/README.md#sandbox-security)
assigns policy and argument-selection responsibility to its caller. This is a
documentation reference, not a chosen minimum runtime version. Track upstream and
distro security fixes, qualify updates and reject incompatible versions/capabilities.
No task-time installation or automatic fallback to unsandboxed execution is allowed.
Independent audit/search work does not require an installed execution backend.

### Upstream distribution and migration contract

This targets all upstream ARTEX distributions. At the focused baseline, the
[Docker runtime](https://github.com/BaaRai/ARTEX/blob/e5c7bfcbdd893db26c08f0b004e0169b4ab6ca85/Dockerfile)
installs Node 22.23.4, while [Go ZIPs](https://github.com/BaaRai/ARTEX/blob/e5c7bfcbdd893db26c08f0b004e0169b4ab6ca85/build.sh)
embed the static UI without Node. Neither fact changes the selected backend's
dependency contract. Do not remove dependencies required by existing tools.

- Preserve Linux amd64/arm64, macOS amd64/arm64 and Windows amd64 release builds
  and both Linux container architectures. A build target is not a sandbox guarantee.
- Begin with an explicitly selected optional Go/bwrap backend. Preserve existing
  startup safety checks; existing degraded operation is not strict containment.
- Provide installation/preflight diagnostics in PR 4 and complete Linux packaging
  and host qualification in PR 6. Pin supported packages/artifacts and protect
  binaries/helpers/config from task writes and task-controlled PATH.
- Version any bridge/helper protocol and capabilities, including cancellation,
  exit and error semantics. Test missing/old bwrap, unavailable user/network
  namespaces, absent resource delegation, helper crashes, upgrades and rollback.
- Retain container `no-new-privileges`, execution identities and credential
  boundaries. Do not require blanket privileged containers or relax existing host
  protections to obtain passing results.
- Include shipped components in SBOM/provenance and assign update ownership.
  PR 10 consolidates releases; basic installation is not deferred until then.
  Rollback stops incompatible managed tasks and retains audit history/migrations.

No deployment-specific address, path or credential becomes an upstream default.
Clean-platform tests, not one deployed host, determine support claims.

### Alternatives retained for future profiles

The [runtime ecosystem evaluation](sandbox-runtime-evaluation.md) records SRT,
Fence/other Go wrappers, MXC, gVisor, OCI/VM runtimes and execution platforms as
alternatives. They are not dependencies of the selected Linux backend. A future
adapter needs the same Runner/gateway/qualification contract; stronger hostile-code
profiles can evaluate gVisor or VMs separately. Keep the implementation narrow;
reconsider a full runtime if arbitrary-tool compatibility dominates maintenance.
Go-based does not imply native Windows support or zero runtime dependencies.

### Portability contract: Linux, Windows and macOS

Preserve ARTEX's application portability independently of runner availability.
Keep public policy, audit and runner APIs free of unconditional Linux imports,
syscalls and shell assumptions. Use platform adapters/build constraints or a
versioned helper protocol. Optional runtime installation must not become a
requirement for unrelated management/audit operations, subject to existing server
security startup checks; this is not permission to bypass an isolation self-test.

| Environment | Planned execution path | Support claim required |
| --- | --- | --- |
| Linux native | ARTEX-owned Go/bwrap backend; stronger profiles evaluated separately | Pinned backend, effective containment, task separation and tool tests |
| Windows native | Separately select a native adapter, with MXC among evaluated options | Actual OS build, ACL/token/process-tree/network and tool tests; no Linux-equivalence claim from API compatibility |
| Windows host with Linux VM/WSL2 | Explicit Linux executor profile | Linux guest tools only; host mounts, interoperability, networking and quotas independently tested |
| Windows controller with remote Linux executor | Explicit authenticated runner connection | Policy/session binding, workspace transfer, audit delivery, revocation and outage tests |
| macOS | Keep the portable contract; test a separate backend before support claims | No assumption that Linux/Windows tests establish macOS enforcement |

SRT v0.0.83 packages Windows x64/ARM64 helpers but labels Windows alpha. Its
documented native Windows network residuals and shared-machine limitations mean
that successful initialization alone cannot qualify it for strict adversarial
execution. Reject a profile when required guarantees are not met; a warning or
working HTTP proxy is insufficient. SRT's experimental MXC integration needs its
own capability tests. A Linux guest on Windows does not run native Windows tools.

At the focused upstream baseline, `server/isolation_other.go` reports unsupported
non-Unix isolation; startup in `cmd/artex/main.go` exposes degraded operation where
applicable. A Windows ZIP does not establish strict Windows isolation. New adapters
must integrate with that startup/isolation state, not create a conflicting status.

Before dispatch, report backend/version, host and guest OS/architecture, effective
capabilities, required privileges and limitations. If none qualifies, reject the
strict execution with an actionable reason; never automatically run it on the
host. Explicit remote dispatch is a different deployment profile, not a hidden
fallback. Report local versus remote evidence accordingly.

CI must preserve all five release builds and run portable contract tests on the
supported operating systems. macOS containment needs real-host acceptance before
it is advertised. Native Windows acceptance additionally needs a real Windows
runner: PowerShell/cmd and native executables, path quoting/Unicode, ACL and reparse
point handling, child cleanup/cancellation, environment/credential isolation,
proxy/TLS behavior, task separation, revocation, resources and backend absence.
Cross-compilation and mocked tests alone cannot establish runtime containment.

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
| Coverage | visibility level, backend/version, host/guest platform and capability manifest, related gap references |
| Assessment | referenced observations/artifacts, rule/model/operator source and version, rationale, contradictions and disposition |
| Resource control | budget revision, measured usage, limit/stop reason, enforcement owner and authorized resume reference |

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

Deception assessments are separate events referencing observations, not edits that
turn a target claim into a verified fact. Record uncertainty and later corrections;
the underlying observed request remains unchanged. Artifact references and hashes
identify evidence without requiring unsafe preview or execution. Suspected secrets
and honeytokens follow the same redaction/access policy as other sensitive evidence.
Retain stop/pause/resume history through archive and restart; do not fabricate
fine-grained OS violation details when the sensor reports only an execution error.

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

For suspected deception, show target claims, actual observations, corroboration,
contradictions, judge/operator assessments and resource-limit events separately.
Link a proposed action to its inducing content when correlation is available; mark
unknown/ambiguous correlation rather than inferring causation. Operators can record
a false-positive disposition or authorize a revised budget/policy without erasing
the original evidence. Investigation UI actions must not silently execute artifacts,
test discovered credentials or initiate further probes.

## Compatibility and cost

| Surface | Initial treatment / acceptance boundary |
| --- | --- |
| Linux shell and children | Managed only after containment and bypass-resistance tests |
| Local MCP | Task/run-owned sessions first; concurrent tool-call provenance tested |
| Native Go HTTP | Policy and audit instrumentation required independently of the process sandbox |
| HTTP and redirected HTTP | Per-request policy and attempt chain |
| HTTPS with capture | Explicit trust/inspection profile; preserve existing capture behavior |
| Pinned TLS or mTLS | Opaque or unsupported profile; no invented URL/path visibility |
| SOCKS/raw TCP | Destination-level policy; no universal HTTP semantics |
| UDP, ICMP, raw SYN, QUIC | Separate capability/runner work; strict unsupported profiles fail |
| Remote MCP, VM, container, Android | Separate enrollment and evidence; local wrapping is insufficient |
| Parent proxy | Final-hop address enforcement must be demonstrated |
| Native Windows | Dedicated backend milestone and real-host tests; alpha/degraded capabilities never satisfy strict requirements implicitly |
| Windows with Linux VM/WSL2, remote executors and nested containers | Explicit profiles with independent tests; distinguish host OS from guest/tool OS |
| macOS | Preserve portability and publish separate tests before claiming parity |

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

Deception-resilience controls also cost review time, accounting overhead and
evidence storage. Measure false positives on benign controls, lost useful work,
stop latency and budget overshoot. Stronger isolation for high-risk artifact
execution is a separate runner profile; this proposal makes no VM-level isolation
claim for a shared-kernel sandbox.

## Delivery sequence and exit gates

| PR | Deliverable | Dependencies | Exit criterion |
| --- | --- | --- | --- |
| 1 | ADR, portable Runner/policy/audit contracts and support boundaries | — | Design review only; no new runtime dependency or default |
| 2 | Durable lightweight audit, provenance, coverage and assessment/budget events | 1 | Capture-off, restart, duplicate, archive and spool-full tests; assessments cannot rewrite observations |
| 3 | Authoritative gateway policy and network budgets | 1, 2 | Final-dial IP/host/port, DNS, redirects, deny precedence, revocation and aggregate budgets |
| 4 | Go/bwrap supervisor: explicit offline profile, filesystem/process isolation, lifecycle and installation preflight | 1, 2 and shared Runner contract | Required namespaces, argv/env/fd handling, child cleanup, cross-task isolation, missing-bwrap diagnostics; no network-capable strict claim |
| 5 | Task-private bridge to the ARTEX gateway and network lifecycle | 3, 4 | Removed proxy env/direct sockets cannot bypass policy; trusted attribution; existing tunnels close on cancel/revoke; gateway/bridge failure stops affected work |
| 6 | Linux resource enforcement, tool/host qualification and reproducible packaging | 4, 5 | Pre-exec resource-domain entry, descendant/task-wide limits, supported amd64/arm64 host/container matrix and diagnostics; enables only tested opt-in strict profiles |
| 7 | URL/time search, investigation timeline, retention/archive/export | 2 plus producer integration | Correct attribution, redaction, coverage and distinction between mentions, suspicion and observations |
| 8 | Indirect-action review, evidence corroboration and pause/review/resume | 1, 2; 3–6 for end-to-end stop | Secondary scope checked; remote evidence labeled; fake success stays unverified; resumption cannot override a hard limit |
| 9 | Separate Windows backend and explicit Linux guest/remote profiles | 3, shared Runner contract, backend acceptance | Real native Windows tests where claimed; host/guest tools distinguished; preserve macOS builds and require separate native macOS qualification |
| 10 | Release/SBOM consolidation, compatibility fixtures and rollout benchmarks | 2–8 for Linux; 9 for additional platform claims | Measured overhead, storage, false positives, stop latency and overshoot; upgrade/migration/rollback; no unsupported cross-platform claim |

The former single Linux runtime change is split into PRs 4–6. PR 4 qualifies only
its explicit offline development profile; it does not establish network-capable
strict execution. Only profiles passing the combined gateway, lifecycle, resource
and platform gates may be enabled opt-in after PR 6. PR 7 can proceed when producers
exist, and a native Windows milestone does not block a qualified Linux release.

Ten planned PRs are not ten simultaneous submissions. Keep within the repository's
limit of seven open PRs per contributor and deliver focused changes incrementally.
Code PRs describe actual validation, including real runtime tests rather than mocks
or cross-compilation alone. A promised security property failing is a test failure;
unsupported environments are diagnosed separately, never converted into a pass.

Use synthetic targets and authoritative test DNS. Cover allowed/denied domain and
port, CIDR/IPv6, changed DNS, disallowed redirects, hostname/IP mismatch, invalid
provenance, simultaneous tasks, child processes, removed proxy environment, native
HTTP, parent proxies, TLS pinning/mTLS, opaque protocols, unavailable namespaces,
gateway/bridge failures, full spool, supervisor restart, cancellation and revocation
of already established tunnels. Test filesystem races, descriptor leaks, pre-exec
resource limits, descendant cleanup and aggregate quota exhaustion. A mock A-to-B
server-side request demonstrates the remote trust boundary without operational
exploit payloads. Preserve capture/rewrite/archive/noise-governor regressions.

Deception-resilience fixtures use synthetic services, inert artifacts and fake
credentials: content inducing a denied file read or destination; a fabricated
success claim; bounded crawl/output/download/resource traps; cross-workspace
attempts by two workers; and benign unusual responses. Assert actual enforcement,
aggregate budgets, stop latency/overshoot and auditable review/resumption, without
automatic confirmed-honeypot classification. PR 10 consolidates evidence for
protections implemented earlier; it does not defer those protections to release.

## Rollout and open decisions

First release is opt-in for a controlled Linux task set. Observe mode helps
compatibility evaluation but cannot certify future enforce-mode support. Required
capabilities are checked before task start. Any default change is a separate
reviewed decision.

Migrations remain additive. Rollback stops affected managed tasks and restores
compatible components; it does not silently resume them outside the sandbox or
delete audit history.

Resolve during implementation reviews:

- Which protected bridge/helper topology and supported bwrap versions satisfy
  task authentication, forced egress, cleanup and deployment requirements?
- Which cgroup/delegation and storage mechanisms enforce required limits before
  untrusted execution across the declared host/container support matrix?
- Which execution surfaces can satisfy per-run and per-tool-call attribution
  without unacceptable MCP startup overhead?
- Which scope syntax/migration and URL ambiguity semantics should be exposed?
- What revocation bound and durable-spool behavior can the implementation meet?
- Which TLS/non-HTTP profiles are initially supported, and which require a
  separate executor?
- What retention and performance targets follow from benchmark measurements?
- Which Windows backend and OS builds meet strict-mode requirements, and which
  operations must use explicitly selected Linux-guest/remote profiles?
- Which aggregate budgets and suspicion-to-pause policy preserve useful work
  while containing deceptive workloads?
