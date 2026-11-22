---
name: coverage-guided-fuzzing
description: "Invoke when a native binary, parser, or local service must be fuzzed for memory-safety or logic crashes and no CVE exists: build an instrumented harness, seed the corpus, run afl++ under time and core limits, triage unique crashes, and hand confirmed ones to exploit-dev or report_finding. Covers the arm64 host caveat and the quiet-profile rules for anything that touches the network."
---

# Coverage-guided fuzzing (afl++)

Use this when the bug is likely a parser or memory-safety flaw in a local artifact: a firmware daemon, a native CLI, a file-format parser, a probe/agent binary. Fuzzing is local work, so the 2 req/s quiet profile does **not** throttle it — the limits below are about CPU and wall clock, not traffic.

## 0. Gate: does fuzzing pay here?

- Fuzz only an **in-scope** binary. Static RE first (`rea` skill): strings, imports, and a decompiled entry tell you whether a parser exists and where input enters.
- If source is available, prefer `codeaudit` — reasoning usually beats fuzzing at this rate.
- If the target needs a live network-facing service, stop and check the noise profile; a local harness is almost always better.

## 1. Verify the toolchain

```
afl-fuzz -h | head -1        # must print afl-fuzz++4.04c
afl-clang-fast -V
```

Missing → `apt-get install -y afl++` (already in the tool-bootstrap matrix).

## 2. Understand the input surface

- Identify the entry: file argument, stdin, a socket read, an ioctl, an env var.
- Run the binary under `strings`/`rea` and note magic bytes, length prefixes, checksums. These become your seed structure, and checksums need a fixup hook or the fuzzer will stall at the check.
- Note forkserver viability: static binaries and setuid wrappers often need `AFL_DRIVER`/deferred forkserver or afl++'s qemu mode.

## 3. Build the harness

Preferred order:
1. Source available: instrument with `afl-clang-fast` (or `afl-clang-lto` when LTO is possible). Compile with `-O1 -g`, keep asserts on.
2. CLI consumes a file: put the input path in `@@`; no harness needed.
3. stdin consumer: `afl-fuzz ... -- ./target` with a small `@@`-to-stdin wrapper only if the program cannot take a file.
4. Network daemon: `afl-fuzz -n` (dumb) or a libdesock/preload layer; arm64 preload libs are the fragile part, so prefer a file harness built from the protocol handler.
5. No source and no recompilation: `-Q` (qemu mode) — see the arm64 caveat.

Harness rules:
- Close what the program does not need; mask signals with `AFL_DRIVER` only if a SIGCHLD is expected.
- Keep runtime under ~50 ms per exec; add `AFL_HANG_TMOUT` rather than a long `-t`.
- Constrain the heap with `AFL_PRELOAD`/`LD_PRELOAD`+libdislocator only when allocator behavior matters.

## 4. Seed corpus

- Small, valid, and diverse beats large. One seed per known format branch.
- Start from the program's own example files, test fixtures, or files the parser wrote, not random bytes.
- Cap the corpus at a few dozen entries; run `afl-cmin` and `afl-tmin` after the first coverage plateau.
- With checksums, add a post-processing step or start from valid-checksum seeds.

## 5. Run

```
AFL_NO_UI=1 AFL_SKIP_CPUFREQ=1 afl-fuzz -i in -o out -V 600 -- ./harness @@
```

- Use `-V` for a bounded run rather than an open-ended session; start with 5–10 min, extend only if coverage is still climbing.
- `-m none` for programs that need big allocations, else the default limit.
- Resume is `-i -` against the same `out/`; never reuse an `out/` from a different binary.
- Watch `execs/s` and `corpus count`. Flat coverage and flat corpus after ~10 min means the harness or seeds are wrong — fix those, do not just run longer.

## 6. arm64 host caveat

The host is arm64 but most firmware and desktop targets are x86_64.

- Native arm64 targets: normal instrumentation, full speed.
- x86_64 targets: `-Q` qemu mode with `QEMU_LD_PREFIX=/usr/x86_64-linux-gnu`; expect roughly an order of magnitude fewer execs/s, so shorten runs and rely on good seeds.
- Never fuzz a cross-arch target with `-Q` when a native arm64 build of the same source exists.
- A coverage-only run is not enough here: many arm64 overflows do not crash at all, because `stp x29,x30,[sp,#-N]!` saves the return address below the buffer. Triage must check for overwritten function pointers, length fields, and nested frames, not just signal receipt — see the `exploit-dev` skill.

## 7. Triage (mandatory before any finding)

1. `ls out/default/crashes/` — ignore `README.txt`.
2. Reproduce outside afl: `./harness < crash` and confirm the exit signal.
3. De-duplicate by stack: `gdb -batch -ex run -ex bt --args ./harness crash` (use `gdb-multiarch` + `set architecture` for foreign ELF). One bug class per stack frame set.
4. Classify: stack overflow, heap overflow, UAF, null deref, integer-driven OOB, assertion-only.
5. Minimize: `afl-tmin -i crash -o min -- ./harness @@`.
6. Prove controllability: does the crashing offset sit in bytes you control? Does the saved return address / vtable pointer change with the mutation? That decides severity — hand it to `exploit-dev` when it does, otherwise report as a crash/DoS.
7. Record the harness command, seed hash, crash file hash, and the exact `bt` as evidence.

## 8. Reporting

- Register only reproducible, de-duplicated crashes. A crash that does not reproduce on a fresh run is not a finding.
- `report_finding` with the minimized input, the `bt`, and the build hash. Library-only bugs name the library version in the summary.
- Attach the crash file under the task workspace, never `/tmp`.

## 9. Cleanup

- Kill leftover `afl-fuzz` (`pkill -f afl-fuzz`), confirm with `pgrep`.
- Keep `out/default/crashes/` and `queue/` under the workspace if the finding stands; delete multi-GB `out/` trees otherwise.
