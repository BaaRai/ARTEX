# Ghidra static binary analysis

ARTEX can analyze native binaries with a self-hosted Ghidra sidecar: the agent uploads an artifact from its run directory, waits for `analyzeHeadless` to finish, then decompiles functions, reads strings and imports, and ranks the attack surface. The sidecar analyzes the file; it never executes it.

This is an optional service. Without it ARTEX runs exactly as before: the `ghidra_*` tools stay in the catalog but answer "Ghidra is not configured", and the `ghidra` skill tells the agent to continue with the tools it has.

## Enabling it

The bundled overlay starts the pinned `biniamfd/ghidra-headless-rest` image and points ARTEX at it:

```bash
docker compose -f docker-compose.yml -f docker-compose.ghidra.yml up -d
```

The sidecar is reachable only on the compose network (`http://ghidra:9090`). It publishes no host port on purpose: the image has no authentication of its own, so a published port would expose an unauthenticated binary-analysis endpoint to every process on the host.

To use an external sidecar instead, set these in `.env` and run the base compose file:

```bash
ARTEX_GHIDRA_URL=http://ghidra.internal:9090
# Only needed when the endpoint sits behind an authenticating proxy.
ARTEX_GHIDRA_API_KEY=
```

The overlay defaults `ARTEX_GHIDRA_URL` to the bundled service; a value already in `.env` wins.

## Configuration

| Variable | Meaning |
|---|---|
| `ARTEX_GHIDRA_URL` | Base URL of the sidecar. Empty disables the integration. |
| `ARTEX_GHIDRA_API_KEY` | Sent as `Authorization: Bearer …` when non-empty. |

Sidecar-side knobs (`MAX_CONCURRENT_ANALYSES`, `MAX_UPLOAD_SIZE`, `ANALYSIS_TIMEOUT`, `RETENTION_SECONDS`, `SECURITY_SCORER_ENABLED`, …) are set in `docker-compose.ghidra.yml`. The overlay analyzes one binary at a time and accepts uploads up to 512 MiB.

The image is published for `linux/amd64` only. On an arm64 host Docker Desktop runs it under emulation: it works, but analysis is several times slower. ARTEX itself is unaffected either way.

## Tools

All seven are built-in worker tools, so no MCP server is involved.

| Tool | Kind | What it does |
|---|---|---|
| `ghidra_status` | read | health, version and capabilities; call once before the first use |
| `ghidra_analyze` | write | upload a file from the run directory and start analysis |
| `ghidra_job` | read | poll a job: `queued`, `running`, `done`, `failed`, `cancelled`, `interrupted` |
| `ghidra_functions` | read | list functions, filtered by name, display name or address |
| `ghidra_decompile` | read | C-like pseudocode of one function by address |
| `ghidra_strings` | read | printable strings with a minimum length |
| `ghidra_attack_surface` | read | functions ranked by the sidecar's deterministic score, filterable by band, category and minimum score |

`ghidra_analyze` opens the upload with the same confinement as the file tools: the path is resolved inside the worker's run directory, so a model-supplied `../` cannot reach another run's tree. The uploaded name is the base name only.

Every read tool clips its JSON at 96 KB and appends a note saying so, instead of silently returning a truncated document.

The attack-surface score is a review prior, not a verdict: it orders what to read first. A finding still needs the decompiled code and, where it matters, a proof.

## Workflow the skill enforces

1. `ghidra_status` once; if it reports "not configured", say so and stop.
2. Save the artifact in the run directory, call `ghidra_analyze`, keep the `job_id`.
3. Poll `ghidra_job` until a terminal state. A repeated upload of the same bytes is deduplicated by SHA-256: the sidecar answers `cache_hit` with `reused_job_id`, and the client reports that id as `job_id`.
4. Start with `ghidra_attack_surface` (`band: critical`, then `high`) and `ghidra_strings`, decompile the candidates, then widen.
5. Record what is learned as facts, citing the job id and address.

The full playbook is `skills/ghidra/SKILL.md`.

## Resource use

A Ghidra analysis is CPU- and memory-heavy and takes minutes on a large binary; the container is the place to bound it. The overlay runs one analysis at a time (`MAX_CONCURRENT_ANALYSES: 1`), and analysis artifacts live in the `ghidra-data` volume, bounded by the sidecar's `RETENTION_SECONDS`. Deleting the volume removes every stored project.

## Security notes

- The sidecar has no authentication and is meant for a single trusted user. Keep it on the compose network or behind a proxy that authenticates; never publish port 9090 to an untrusted network.
- Analysis is static. Nothing in this integration runs the uploaded binary.
- The binary is untrusted data: strings and pseudocode are evidence for the agent, not instructions.
- Artifacts stay in the sidecar's data volume. Treat that volume as engagement data and delete it when the engagement ends.

## Troubleshooting

| Symptom | Cause |
|---|---|
| every tool answers "Ghidra is not configured" | `ARTEX_GHIDRA_URL` is empty in the ARTEX container |
| `ghidra_status` fails with a connection error | the sidecar is not up yet, or the URL is wrong; `docker compose -f docker-compose.yml -f docker-compose.ghidra.yml ps` |
| job stays `running` for a long time | normal for a large binary; poll, do not re-upload |
| job is `interrupted` | the sidecar restarted mid-analysis; upload again |
| `HTTP 413` on upload | the file exceeds the sidecar's `MAX_UPLOAD_SIZE` |
| decompile returns an error for an address | the address is not a function entry; find it with `ghidra_functions` first |

## Tests

The client and the tools are covered without a running sidecar: `go test ./ghidra/ ./agent/ -run 'TestClient|TestGhidra'`. The tests use `httptest` servers and assert the request shapes (multipart upload, paths, query parameters, bearer token), the confinement refusal, the response size cap and the truncation note.
