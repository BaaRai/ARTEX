---
name: symbolic-execution
description: Invoke when a specific input must be derived to reach a sink, bypass a guard, or satisfy a chain of checks in a native binary — no crash available, brute force too large, or a patch needs reachability proof. Covers angr project/state setup on this aarch64 host, stdin and argv symbolisation, hooking, state-explosion control, model extraction and dynamic confirmation.
---
# Symbolic execution (angr)

Use this when the question is **"which input gets me from here to there"** and the search space is too large to fuzz or reason about by hand: a magic-value chain, a checksum with a branch, a flag parser, a license routine, a reachability question on a patched vs unpatched build.

Use something else when: the bug is already crashing (→ `exploit-dev`), the target is huge and mostly I/O-bound (→ `coverage-guided-fuzzing`), or the checks are pure string compares that `strings`/Ghidra reads in a minute.

Installed: angr 9.3.4, claripy 9.3.4, z3-solver 4.13.0.0. **Do not upgrade angr** — 10.x pins z3 5.x which has no aarch64 wheel and needs gcc 13 (host has gcc 12). The environment is aarch64; x86_64 targets run under `qemu-x86_64` and angr loads them natively (it is architecture-independent).

## 1. Frame the question precisely

Write it down before touching code: *reach address A (or return value V) from entry, with sink S executed, while avoiding crash path B.* Vague goals are what make angr "slow" — it is the search space, not the tool.

Pick the cheapest goal that still proves the point:

| Goal | Method |
|---|---|
| Does input reach a `system`/`memcpy` call with controlled pointer? | `find=<call addr>`, inspect the arg registers on the found state |
| Bypass a compare chain | `avoid=<fail block addr>`, `find=<success block>` |
| Does the patched build's new guard block the old payload? | run both builds with the same symbolic prefix, compare found/not-found |
| What is a valid licence/key? | find the `puts("OK")` successor, `eval` the buffer |

## 2. Project and state

```python
import angr, claripy, logging
logging.getLogger("angr").setLevel("ERROR")
p = angr.Project("./target", auto_load_libs=False)   # True only if libc semantics matter
base = p.loader.main_object.mapped_base
```

Never `auto_load_libs=True` on a binary with a full glibc — simulate, don't load; hook what you must.

State with a **fixed-length symbolic stdin** (verified working on 9.3.4 — `posix.files` is gone, and `SimFileStream` has no `read_from`):

```python
n = 32
payload = claripy.BVS("stdin", 8*n)
sf = angr.SimFile("stdin", size=n, content=payload)
st = p.factory.full_init_state(
    stdin=sf,
    add_options={angr.options.ZERO_FILL_UNCONSTRAINED_MEMORY,
                 angr.options.ZERO_FILL_UNCONSTRAINED_REGISTERS,
                 angr.options.LAZY_SOLVES})
```

- A fixed length beats `angr.SimFileStream`: unbounded streams invite length blowups and half-solved models. Size it to what the target actually reads (`read(0, buf, 8)` → 8).
- `SimFileStream` is still fine for a quick reachability question where the length does not matter; keep the `content` handle if you need per-byte evaluation.
- argv instead of stdin: `p.factory.entry_state(args=[b"./target", claripy.BVS("arg1", 8*16)])`.
- A specific function, not `main`: `p.factory.call_state(func_addr, claripy.BVS("p", 8*8))` — fastest for isolated parsers.

## 3. Hook the environment

Anything that touches the kernel or is not modelled must be hooked, or the path dies:

```python
class Ok(angr.SimProcedure):
    def run(self, *a): return 1
p.hook(0x401234, Ok(), length=5)
p.hook_symbol("__isoc99_scanf", angr.SIM_PROCEDURES["libc"]["scanf"])
```

Rules of thumb: hook `time`, `rand` (return a concrete constant), `getenv` (return a fixed pointer or 0), file opens (return a `SimFile` with the content the target expects), and any anti-debug call. Keep hooks minimal — every hook is a lie the solver believes.

## 4. Drive the search

```python
simgr = p.factory.simgr(st)
simgr.explore(find=0x401300, avoid=[0x401280], num_find=1)
```

- `find`/`avoid` take addresses, lists, or callables over the state. Prefer **block addresses** from Ghidra/r2 over callables — callables re-evaluate every step and are easy to get wrong.
- cap it: `simgr.explore(..., num_find=1)`; if nothing lands in ~60 s, re-frame (usually the goal address is wrong or a hook is missing).
- explosion control, in order: `veritesting=True` in `explore`, `LAZY_SOLVES`, then narrowing the input size, then slicing the function (`call_state`) instead of running from `main`.
- `simgr.stashes`, `simgr.move(...)` for manual staging when a harness needs two phases.

## 5. Extract the model

```python
f = simgr.found[0]
inp = f.posix.dumps(0)                  # stdin bytes, concrete
print(inp, len(inp))
# exact bytes from the handle you created, when the dump is truncated by NULs:
print(bytes(f.solver.eval(payload, cast_to=bytes)))
```

Constraints that are *not* part of the answer (ASLR, heap addresses) go out with `f.solver.eval` on the specific variable. If `eval` returns two different values on two calls, the model is under-constrained — add the missing `avoid`, or pin the unconstrained register/pointer with `ZERO_FILL_*` (already set above).

## 6. Confirm dynamically, always

A model that only satisfies the simulator is a hypothesis:

- Native arm64: run it. `printf '%s' "$(python3 -c 'print(...)')" | ./target`.
- x86_64: `QEMU_LD_PREFIX=/usr/x86_64-linux-gnu qemu-x86_64 ./target < model`.
- Compare against the crash/return the goal predicted; on mismatch add the guard to `avoid` (the simulator usually took a hook path the loader does not).
- For a patch-reachability claim, run the *same* model on the unpatched and patched builds and show found vs blocked.

## 7. Reporting

Bind: the goal (address + symbol), the find/avoid pair, the script (work dir, sha256), the model bytes, and the dynamic confirmation on both builds. PoE: 1 model found only in the simulator, 2 model confirmed on the real binary, 3 sink reached with controlled argument, 4 full exploit input / guard bypass with impact.

## 8. Known failure modes on this host

- `AttributeError: SimSystemPosix has no attribute 'files'` — old blog examples. Use `st.posix.stdin`.
- `bitstring` / `pycparser` / `z3` upgraded by a careless `pip install` — pin: `bitstring==4.1.4`, `pycparser==3.0`, `z3-solver==4.13.0.0`.
- `unsupported architecture` on firmware blobs: `p = angr.Project("./blob", main_opts={"backend": "blob", "base_addr": 0x400000, "arch": "arm")}`.
- Solver returns instantly with garbage and `simgr.found` is false: the success block is unreachable because `main` never got the argv it wants — check `p.entry`, `__libc_start_main` handling, and pass `args=[...]`.
- Timeouts with no progress: usually unmapped memory reads. Add `ZERO_FILL_UNCONSTRAINED_MEMORY` (set) and hook the allocator before blaming angr.

## 9. Cleanup

Kill stray qemu processes, keep `solve.py` and the model file in the work dir, and note the angr version in the finding so the model stays reproducible.