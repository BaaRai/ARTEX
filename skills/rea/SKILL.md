---
name: rea
description: "Reverse-engineer binaries and application packages (ELF, PE, Mach-O, APK, IPA, HAP, WASM, ZIP/JAR containers, JavaScript bundles) through the rea MCP server running in this container. Invoke when a task needs static analysis of a compiled or packaged target: embedded secrets, hardcoded endpoints, native library review, string and constant extraction, managed code structure, or deobfuscation."
mcps: rea
---

# rea: binary and application reverse engineering

The rea MCP server runs inside this container as a stdio server (`/usr/local/bin/rea mcp`, package `rea-agents@6.9.0`) and is already wired to Ghidra 12.2 with JDK 25. Its tools are named `mcp__rea__<tool>`. If a `mcp__rea__*` call fails with `provider_unavailable`, check `GHIDRA_INSTALL_DIR=/opt/ghidra rea doctor` before anything else. After this skill loads, their names appear as deferred tools: fetch each tool's schema with SearchExtraTools (for example `select:mcp__rea__open_binary`, or keywords like `rea strings`), then invoke it with ExecuteExtraTool. Use the tool that directly answers the question; do not enumerate the whole catalog.

## 1. File paths

rea runs in the same container as the shell, so it reads ordinary container paths. No host-side translation is needed.

Rules:
- Save or download every target file under the task workspace (for example `/app/data/tasks/<task_id>/rea/`) and pass that absolute container path.
- Verify the file exists with `ls` and note its sha256 before opening it; if a path is reported missing, it is a typo, not a mount problem.
- Keep the working copy out of `/tmp` so it survives for analysis and evidence binding.

## 2. Session lifecycle

1. `open_binary` with the absolute container `path`. For native binaries (ELF, PE, Mach-O) set `provider_id: "ghidra"`; if the error lists candidate provider ids, retry with one of them. Packages and bundles (APK, IPA, HAP, zip, JAR, wasm, JavaScript) need no provider.
2. Run analysis tools (`search_strings`, `list_strings`, `search_procedures`, `inspect_native_*`, `project_managed_application_graph`, family-specific graph tools, ...). Results are Evidence objects: they carry an evidence id, a normalized result, and a limitations section. Record anything task-relevant as facts in the knowledge graph and cite the evidence id in findings.
3. `close_binary` when done. One open target per session; close before opening another. Never leave a binary open at the end of a run.

## 3. What to use it for (authorized targets only)

- Client-side artifacts fetched from an in-scope target: mobile apps (APK, IPA, HAP), desktop binaries, Electron app archives, obfuscated JavaScript bundles, wasm modules.
- Embedded secrets and configuration: API keys, tokens, endpoints, debug flags, test accounts (`search_strings` with patterns like `key`, `token`, `secret`, `password`, `http`, base64 blobs).
- Fingerprinting: sha256 and component inventories identify the exact build and its third-party libraries.
- Deep dives: strings and constants, imports and exports, procedures, managed type graphs, native API usage.

## 4. Practical notes

- Prefer scoped queries (one string pattern, one procedure) over whole-binary deep passes; some analyses take minutes. If the server is configured with a short request timeout, budget accordingly.
- Large results are truncated to the session output limit with spill to disk; narrow the query instead of paging through huge output.
- If a call fails with `capability_unavailable`, read the message: it usually lists the exact candidate provider ids or the remediation to apply.
- rea reads files only; it never executes the target.
- rea cannot see container-only paths; do not try to open files outside the shared data mount.
