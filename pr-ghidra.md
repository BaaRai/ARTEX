## Problem

ARTEX has no built-in way to statically analyze a compiled artifact. The `rea` skill points at a host-side MCP server, which the operator has to install and run separately; without it the agent can only read strings with the shell.

## Change

A new optional sidecar integration, off by default:

- `ghidra/` — a small client for `ghidra-headless-rest` (Ghidra `analyzeHeadless` behind a local REST API): health/version/capabilities, multipart upload, job polling, functions, decompile, strings and the deterministic attack-surface ranking.
- Seven worker tools: `ghidra_status`, `ghidra_analyze`, `ghidra_job`, `ghidra_functions`, `ghidra_decompile`, `ghidra_strings`, `ghidra_attack_surface`.
- `skills/ghidra/SKILL.md` — when to use it, the lifecycle, and the rule that the attack-surface score is a review prior, not a verdict.
- `docker-compose.ghidra.yml` — optional overlay with the pinned image; no host port, because the sidecar has no authentication of its own.
- `docs/ghidra.md`, a README section, and `ARTEX_GHIDRA_URL` / `ARTEX_GHIDRA_API_KEY` in `.env.example`.

Behaviour without the sidecar is unchanged: the tools answer that the service is not configured, and nothing is sent anywhere.

Safety properties worth calling out:

- The uploaded file is opened with the same confinement as the file tools (`openScanOutputInBase` with the run's working directory), so a model-supplied `../` cannot reach another run's tree.
- Only the base name is sent to the sidecar; responses are capped at 8 MiB in the client and clipped at 96 KB for the model with an explicit truncation note.
- The sidecar analyzes the file, never executes it.

## Testing

- `go test ./ghidra/ ./agent/ -run 'TestClient|TestGhidra'` — request shapes (multipart upload, paths, query parameters, bearer token), deduplicated-upload normalisation, HTTP error text, response cap, id/path rejection, context cancellation, run-directory confinement, page-size clamp, truncation note. All pass under `golang:1.26.11-bookworm`.
- `go build ./...` and `go vet ./...` are clean.
- The compose overlay pins `biniamfd/ghidra-headless-rest:1.2.1` by digest (`sha256:971591a3…f1442d`); `docker manifest inspect` confirms `linux/amd64` (documented in `docs/ghidra.md`).
