# Sandbox runtime ecosystem evaluation

Research record and revised direction, 2026-11-27: ARTEX-owned Go/bubblewrap for Linux.
Candidate containment remains unqualified; this is not a runtime test report.
Companion: [managed execution design](managed-execution-scope-and-audit.md).

## Evidence and scope

This review checked official documentation, repository files, release metadata and
the published SRT npm archive. It did not execute containment tests on any candidate
or on a Windows host. Source availability, package contents and a passing build do
not establish a security guarantee.

The npm `latest` version checked was `@anthropic-ai/sandbox-runtime@0.0.83`.
Its archive matched the registry SHA-512 integrity value and contained x64/ARM64
`srt-win.exe` helpers and distributed configuration/proxy code for the features
discussed below. The package declares Node >=22.12.0. Archive inspection is not
runtime qualification.

Official release metadata checked: SRT v0.0.83, MXC v1.1.0, OpenShell v0.1.10,
nono v0.82.0, microsandbox v0.8.3, OpenSandbox release-1.2.1, and Kubernetes
agent-sandbox v1.0.12. Other feature observations may describe main/development
documentation; the implementation must repeat checks against its selected artifact.

## Four different portability claims

1. The ARTEX controller compiles/runs on the host.
2. An SDK/client can contact a sandbox service from that host.
3. A local backend creates an execution boundary on that host.
4. The sandbox can execute the required guest OS tools.

A Go SDK does not imply a Go runtime or native Windows execution. A Linux VM on
Windows runs Linux workloads; it does not establish a boundary around arbitrary
Windows executables. Language choice is not an isolation primitive.

## SRT: cross-platform, with different guarantees

[SRT v0.0.83](https://github.com/anthropics/sandbox-runtime/blob/v0.0.83/README.md)
uses Linux bubblewrap/network namespaces, macOS Seatbelt, and an alpha Windows
backend using a dedicated account, restricted tokens and WFP. Its Windows helpers
are present in the released npm package.

The Windows documentation discloses network residuals involving identity changes
and service-mediated operations, plus shared-machine and tool compatibility limits.
Some residuals are warnings rather than initialization failures. Consequently,
that backend must not qualify for ARTEX strict adversarial execution merely because
it starts successfully. The experimental MXC integration is a separate capability
profile with its own host and tool requirements.

SRT's [configuration source](https://github.com/anthropics/sandbox-runtime/blob/0bbebed0aa9a8e628dc4df8bc30293550a1988eb/src/sandbox/sandbox-config.ts)
offers domain/port policy, strict allowlists, resolved-address restrictions and
proxy integration. ARTEX still owns task policy translation, final-hop enforcement,
audit provenance, budgets and incident search. HTTPS path policy requires plaintext
visibility or TLS inspection; upstream proxy resolution needs its own enforcement.
Avoid duplicate TLS interception.

Node costs must be evaluated across upstream distributions, not one installed host.
At ARTEX main `e5c7bfcbdd893db26c08f0b004e0169b4ab6ca85`, the Docker runtime installs
Node 22.23.4, but release ZIPs do not bundle Node and serve a statically embedded UI.
See the [distribution contract](managed-execution-scope-and-audit.md#upstream-distribution-and-migration-contract)
and [backend plan](bubblewrap-backend-plan.md) for packaging and acceptance requirements.
Neither a frontend build nor one deployment establishes a universal prerequisite.

## Ecosystem map

| Layer / candidate | Execution boundary and platform | Go relationship | ARTEX fit and limit |
| --- | --- | --- | --- |
| [SRT](https://github.com/anthropics/sandbox-runtime) | Native process restrictions; Linux/macOS, Windows alpha | Invoke a Node helper/CLI from Go | Evaluated alternative, not selected for the initial Linux backend; Windows needs separate qualification |
| [Microsoft MXC](https://github.com/microsoft/mxc/tree/v1.1.0) | OS-specific process/session/VM backends across Windows/Linux/macOS | Rust/.NET/Node SDKs; JSON executor interface usable from Go | Important native Windows candidate; exact OS build and backend capability matter |
| [nono](https://github.com/nolabs-ai/nono) | Linux Landlock, macOS Seatbelt; Windows through WSL2 | Rust implementation; verify official binding coverage rather than third-party SDK lists | Similar policy/credential/audit concerns; WSL2 is not native Windows |
| [bubblewrap](https://github.com/containers/bubblewrap), [runc](https://github.com/opencontainers/runc) | Linux namespace/container primitives | runc is Go; bwrap is an external primitive for a Go supervisor | Useful foundation, not a complete per-task policy/audit product |
| [Fence](https://github.com/fencesandbox/fence) | Go runtime over Linux bwrap and macOS Seatbelt | Public Go package plus CLI/helper lifecycle | Reference/alternative; its library, proxy and resource-limit contracts still require ARTEX integration; no native Windows guarantee |
| [exec-sandbox](https://github.com/tkdtaylor/exec-sandbox), [flar](https://github.com/swelljoe/flar) | Go launchers over Linux bwrap and/or gVisor | Go implementations | Relevant examples; independently verify security, release maintenance and credential/approval defaults |
| [gVisor/runsc](https://gvisor.dev/docs/) | Linux application kernel / OCI runtime | Go implementation; separate Go SDK exists | Candidate stronger-isolation profile; syscall compatibility and overhead need tests |
| [Firecracker](https://github.com/firecracker-microvm/firecracker), [Kata](https://github.com/kata-containers/kata-containers) | VM boundary and Linux/container integration | Firecracker is an external VMM; Kata has Go/Rust components | More guest/image/network lifecycle work; no built-in ARTEX URL audit |
| [microsandbox](https://github.com/superradcompany/microsandbox) | Linux guests on Linux KVM, macOS Apple Silicon and Windows WHP | Go SDK plus native runtime/FFI assets | Cross-host microVM candidate; beta, and a Windows host still does not imply Windows guests |
| [Windows Sandbox](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/) | Disposable Windows VM | OS feature, not a Go library | Native Windows workload isolation; edition, lifecycle, concurrency and network mediation constraints |
| [OpenShell](https://github.com/ai-openshell/openshell) | Policy/supervisor platform with several compute drivers | Go SDK to its gateway, not a pure-Go sandbox | Rich controls but greater integration surface; native Windows has a separate unqualified development path |
| [OpenSandbox](https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/architecture/index.md) | Lifecycle platform over Docker/Kubernetes and other providers | Python control plane, Go execd and Go SDK | Useful for pooled/remote execution; guarantees depend on runtime and network configuration |
| [Kubernetes agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) | CRD/controller orchestration; delegates isolation via RuntimeClass | Go controller/client ecosystem | Relevant if ARTEX adopts Kubernetes; not an isolation engine itself |
| [E2B Runtime](https://github.com/e2b-dev/runtime) | Linux/KVM Firecracker execution platform, hosted or self-operated | Go backend around an external VMM | Full remote execution stack; heavier than wrapping local commands |
| [Daytona](https://www.daytona.io/docs/en/sandboxes/) | Service with different workload classes, including Windows VMs | Client SDK support, including Go | Service evaluation distinct from embedding a runtime; old public core repository is unmaintained |
| [Wasmtime](https://docs.wasmtime.dev/security.html) | WebAssembly capability boundary | Embed/invoke a Wasm runtime | Useful for constrained plugins, not a drop-in boundary for arbitrary native tools |

The gVisor [Go SDK quickstart](https://gvisor.dev/docs/sdk/go/quickstart/) explicitly
labels that SDK experimental and not intended for production. Do not transfer
runsc's maturity to its newer API.

MXC v1.1.0 lists Rust/.NET/Node SDKs and JSON executors. Its
[Windows build requirements](https://github.com/microsoft/mxc/blob/v1.1.0/docs/backends/process-container/os-version-support.md)
vary by process/session backend; a generic Windows 10/11 label is insufficient.

OpenShell's [Windows guide](https://github.com/ai-openshell/openshell/blob/main/WINDOWS.md)
points to a separate branch and explicitly says the implementation is not a
production-qualified security boundary. Its main stable release assets are not
that native Windows distribution.

nono's [WSL2 documentation](https://www.nono.sh/os-sandbox) describes conditional
kernel capabilities. Query actual ABI/feature support; do not assume all WSL2
installations have the same kernel or proxy enforcement.

Daytona's [old repository notice](https://github.com/daytonaio/daytona) says core
development moved to a private codebase and that repository receives no further
fixes/releases. Current cloud capabilities do not imply equivalent maintained
self-hosted source.

## Selected implementation direction for ARTEX

Implement the narrowly scoped [ARTEX-owned Go/bwrap Linux backend](bubblewrap-backend-plan.md)
behind portable Runner and authoritative gateway contracts. Use upstream bwrap
as an external primitive; do not fork it or reimplement kernel isolation. No
Node/SRT/Fence dependency is introduced by this backend. Existing Node-dependent
tools retain their requirements. This direction prioritizes the fit to ARTEX's
task attribution, policy and lifecycle, not language alignment alone.

ARTEX owns launch/mount policy, gateway transport, complete cleanup, resource
enforcement, packaging and the supported Linux matrix. Keep that scope explicit;
small code size is not proof of security. The implementation plan splits offline
isolation, protected network transport and resource/platform qualification into
three separately reviewable PRs. No candidate has completed ARTEX runtime acceptance.

SRT and Go wrappers remain references or future adapter options. Evaluate native
Windows options independently and consider gVisor or VM profiles when a stronger
boundary is required. The Linux choice establishes no native Windows/macOS
containment guarantee and preserves all existing ARTEX build targets.

For Windows, expose native execution, explicit Linux guest and remote Linux
profiles separately. Absence of a qualifying backend rejects strict execution;
it must not break unrelated management/audit solely because an optional dependency
is missing, nor weaken existing security startup checks.

Do not add a fleet platform merely to sandbox a local command. Conversely, if
execution pools, snapshots and remote scheduling become requirements, compare
OpenSandbox, agent-sandbox and E2B as platforms rather than rewriting those services.

## Acceptance before selecting a backend

The integration targets upstream mergeability. Backend PRs need reproducible
installation, missing-dependency behavior, protocol compatibility and upgrade/
rollback tests immediately; final release consolidation is a later milestone.
Use existing upstream budget accounting and isolation status rather than parallel
implementations. One host's installed Node or a cross-compiled Windows ZIP cannot
establish the product's runtime requirements or containment guarantees.

- Pin version/artifact and record platform/capabilities; test installation, absence,
  upgrade and rollback without insecure fallback.
- Test filesystem/IPC/credential isolation and per-run separation, including
  children and long-lived MCP sessions.
- Test final-dial destination policy, DNS changes, redirects, IPv6, upstream proxies,
  TLS/mTLS/pinning, opaque protocols and policy revocation.
- Test Windows native tools and paths on real Windows; separately test guest/host
  mounts, interoperability and network behavior for VM/WSL profiles.
- Measure startup, CPU/memory/I/O, concurrent throughput and tail latency on the
  actual ARTEX tool set, including browsers, parsers and scanners.
- Test durable attribution, capture-off metadata, budget/stop behavior and deceptive
  versus benign fixtures. No honeypot detection, anonymity or remote RCE containment
  guarantee follows merely from choosing a sandbox.

No installation, rollout or Windows security qualification was performed in this
research. The design selects a direction; implementation PRs must provide the actual qualification evidence.
