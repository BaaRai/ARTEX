# ARTEX-owned Go/bubblewrap Linux backend

Status: selected implementation direction, 2026-11-27; implementation and runtime
qualification remain future work. Companion: [managed execution design](managed-execution-scope-and-audit.md),
[runtime issue #554](https://github.com/BaaRai/ARTEX/issues/554), and
[audit issue #553](https://github.com/BaaRai/ARTEX/issues/553).

## Decision and rationale

Implement a narrowly scoped Linux backend in ARTEX's Go code that invokes a
supported external `bwrap` executable. Keep upstream bubblewrap responsible for
its isolation primitives. ARTEX owns its launch policy, lifecycle, gateway binding,
capability reporting and integration tests. This proposal neither forks bubblewrap
nor reimplements namespace/mount primitives in Go, and requires no CGO binding.

The selected backend adds no Node/SRT runtime requirement and does not depend on
Fence. Existing Node-dependent ARTEX tools and frontend builds remain separate
requirements. SRT, Fence and other runtimes remain references or possible future
adapters; this decision does not claim they are universally inferior or that a
smaller wrapper is automatically secure.

The reason to own this adapter is the fit to ARTEX's single policy gateway,
task-bound provenance, explicit unsupported behavior and whole-task cleanup.
Adopting a higher-level runtime would still leave those responsibilities with
ARTEX while introducing another configuration/lifecycle contract. The cost of the
chosen approach is explicit ownership of platform compatibility and security
regressions. Keep that scope bounded rather than recreating a general desktop or
cross-platform sandbox framework.

Bubblewrap's [security model at v0.13.1](https://github.com/containers/bubblewrap/blob/v0.13.1/README.md#sandbox-security)
assigns security-policy and argument-selection responsibility to the caller. That
release is a documentation reference, not an approved minimum version or a claim
that every distro package has been qualified. Implementation PRs must establish a
supported version/capability matrix and reproducible package or artifact pins.

## Component and trust boundaries

```text
trusted ARTEX scheduler / judge / approvals / existing budget ledger
                         |
                  portable Runner contract
                         |
       Go Linux supervisor + protected task session
                         |
        bwrap: tool process tree, restricted filesystem,
               separate PID and network namespaces
                         |
     task-private proxy bridge with no general host-network access
                         |
           authoritative ARTEX egress gateway
       authentication -> policy -> budgets -> final dial -> audit
```

The bridge is a narrow transport, not another policy engine or an unrestricted
SOCKS escape route. All permitted target traffic also passes through the gateway;
there is no direct-allow path that omits attribution or final-dial checks. Native
Go HTTP clients use the same gateway/policy contract. File tools and managed local
MCP need enrolled execution ownership; remote/shared executors require separate
capability and attribution handling. The logical worker's model loop stays outside
the tool sandbox. Remote SSRF/RCE second hops remain outside local containment.

## Responsibilities and acceptance

| Component | ARTEX must implement or integrate | Acceptance boundary |
| --- | --- | --- |
| Launch specification | Executable/argv, cwd, explicit environment, mounts, session and limits; immutable server-owned policy | No shell concatenation by the adapter; shell execution only when explicitly requested by the tool contract; no loading policy from an untrusted workspace |
| Runtime discovery | Trusted executable/helper paths, version and required-feature probes | Reject missing/incompatible capabilities before untrusted execution; do not execute a task-writable binary from PATH |
| Filesystem | A controlled read-only tool environment; separate writable workspace and bounded temporary/output storage | No host root/home/config or Docker/D-Bus socket exposure by default; read-only secrets are still secrets; test symlink/path races and sibling-task access |
| Environment and descriptors | Minimal explicit environment and intended stdio/bridge descriptors | No inherited controller credentials, proxy escape routes, control sockets or unintended open file descriptors |
| Network | Mandatory separate network namespace for the managed Linux profile; protected per-session gateway transport | Direct host/external connections fail even with proxy variables removed; both allowed and rejected attempts have honest observation/coverage semantics |
| Lifecycle | Prepare, start, cancel/revoke, wait and cleanup; supervisor restart/failure handling | Kill descendants, close existing tunnels and bridge connections, prevent new work, bound cleanup time and record incomplete cleanup |
| Resource domain | Integrate cgroup v2 or another demonstrated host mechanism plus I/O/storage limits | Required limits cover descendants before the first untrusted instruction; a create-then-move race is not acceptance; missing delegation rejects that strict profile |
| Gateway | Trusted task/run binding, final IP checks, typed host/port rules, redirects, revocation and byte/request budgets | An allowed hostname cannot bypass address constraints; reused connections and CONNECT/WebSocket/SSE streams cannot outlive revoked authorization |
| Audit | Existing capture integration plus independent metadata, stages and source health | Opaque HTTPS remains host/port metadata unless a qualified visibility mechanism provides the path; no invented full-URL or OS-denial detail |
| Packaging and maintenance | Supported distro/container matrix, dependency provenance, fixes and rollback ownership | Clean install, absence, incompatible versions, security updates and rollback are tested; task-time downloads and blanket privileged containers are excluded |

Use existing executor privilege dropping and startup isolation checks. The bwrap
supervisor must not turn a root controller into a root tool executor. Namespace,
capability, mount, device and session flags are derived from an explicit tested
profile, not copied from an illustrative command. Do not silently omit a required
namespace to improve compatibility. Host LSM/user-namespace restrictions and
container nesting need explicit diagnosis, without relaxing existing protections.

CPU/memory/process limits do not solve disk/output budgets, and per-worker limits
do not bound a whole task. Extend ARTEX's existing token/turn/time accounting with
gateway and supervisor dimensions. Attribute helper overhead and define task-wide
aggregation, measured overshoot and pause/resume semantics. Failure of a mandatory
limit, policy channel or audit durability gate stops strict admission/execution.

`exec.CommandContext` killing one direct child is not sufficient evidence of tree
cleanup. Likewise, closing a listening proxy socket is not evidence that existing
tunnels have stopped. Tests must hold a child and an established stream open,
cancel/revoke the task, and assert that both stop within the published bound.

## Initial support and deliberate limits

- First qualify Linux amd64/arm64 with declared distro, kernel, bwrap, execution
  identity and cgroup/delegation combinations. Test supported Docker configurations
  with the existing `no-new-privileges` and credential boundaries intact.
- Start with controlled tool environments, shell/custom tools and task-owned local
  MCP sessions, plus HTTP(S) and supported proxied TCP. An offline profile is useful
  during development but is not permission to silently disable network for a task
  that requested it or misreport the resulting coverage.
- UDP/ICMP/raw SYN/QUIC, arbitrary host mounts, shared or remote MCP, nested
  containers and browser-specific requirements need separate capability profiles
  and tests. Unsupported strict requests fail with an actionable reason. Never
  grant host networking or broad sockets just to make an unsupported tool work.
- Preserve Linux/macOS/Windows controller builds. bwrap is a Linux backend, not a
  native Windows/macOS implementation. Native Windows is a separate milestone;
  macOS requires its own qualified adapter before claims of local containment.
  Explicit Linux guest/remote profiles identify both host and guest and their
  credential, mount, evidence and revocation boundaries.
- A shared-kernel sandbox is not VM-level isolation. Stronger hostile-artifact
  execution can use a separately evaluated VM/gVisor profile. Deception resilience
  means containment, evidence and budgets, not honeypot detection or anonymity.

## Incremental PR plan

The sequence contains ten planned focused PRs, not ten simultaneous submissions.
PR 1 is the existing design PR #555. Runtime work is split into three changes so
that process/filesystem, gateway transport, and resource/qualification behavior
can be reviewed separately. All later numbers are planning slots, not GitHub PR IDs.

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

PR 4 ships diagnostic/install support with its implementation. PR 6 completes the
Linux support matrix rather than postponing packaging until PR 10. Shared package
changes preserve all five existing release build targets throughout. Search can
proceed once producers exist; a native Windows milestone does not block the
qualified Linux release. The repository's limit of seven open PRs per contributor
still applies; deliver and review incrementally.

## Required regression evidence and ownership

Use synthetic local services, test DNS and inert content. Include namespace
unavailability, failed helper startup, removed proxy environment, cross-task
workspace/credential attempts, redirect/DNS/IP mismatches, gateway outage,
long-lived tunnel revocation, descendant cleanup, aggregate quota exhaustion,
spool-full behavior and supervisor restart. For a supported profile, a security
property that fails is a failed test; it must not become a skip. Unsupported hosts
are identified by separate preflight tests, not a successful-enforcement label.

The ARTEX backend maintainers own argument/profile review, helper protocol
compatibility, supported-platform CI and bwrap security-update intake. Each backend
PR must identify maintenance/rollback ownership before merge; use a supported
version range and artifact/package provenance rather than accepting any binary
that prints a version. Test approved updates before promoting them. An upgrade or
rollback stops incompatible managed tasks and preserves audit history.

This document records the implementation direction and its costs. No runtime
implementation, performance result, Windows security qualification or production
rollout is claimed by the design PR.
