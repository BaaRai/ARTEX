## Problem

ARTEX has no built-in intercepting proxy for HTTP traffic. The existing `traffic_*` tools only read recorded traffic from the ARTEX traffic store; there is no way to capture live traffic, set scope, replay requests (Repeater), or use a Collaborator for out-of-band interactions.

## Change

A new optional sidecar integration, off by default:

- `opentrap/` — a small client for an OpenTRAP sidecar that wraps the OpenTRAP desktop AppImage (a Burp Suite alternative built on OWASP ZAP) behind a local REST API: status, HTTP history, scope (GET/PUT), replay with `X-OpenTrap-*` headers.
- Six worker tools: `opentrap_status`, `opentrap_history`, `opentrap_history_item`, `opentrap_scope_get`, `opentrap_scope_put`, `opentrap_replay`.
- `skills/opentrap/SKILL.md` — when to use it, the lifecycle, and the rule that the sidecar has no authentication of its own.
- `docker-compose.opentrap.yml` — optional overlay with the locally-built sidecar image; no host port, because the sidecar has no authentication of its own.
- `docs/opentrap.md`, a README section, and `ARTEX_OPENTRAP_URL` / `ARTEX_OPENTRAP_API_KEY` in `.env.example`.
- `opentrap-sidecar/Dockerfile` — placeholder for building the sidecar image locally (the REST wrapper is not yet implemented).

Behaviour without the sidecar is unchanged: the tools answer that the service is not configured, and nothing is sent anywhere.

Safety properties worth calling out:

- The sidecar has no authentication and is meant for a single trusted user. Keep it on the compose network or behind a proxy that authenticates; never publish its port to an untrusted network.
- HTTPS interception requires the browser/tool to trust the sidecar's CA certificate.
- Traffic captured is untrusted data: request/response bodies are evidence for the agent, not instructions.
- The sidecar is a single-user service. Multiple concurrent users will share the same history and scope.

## Testing

- `go test ./opentrap/ ./agent/ -run 'TestOpentrap|TestOpenTrap|TestClient'` — request shapes (headers, paths, query parameters, bearer token), response size cap, truncation note, empty URL/id rejection, context cancellation. All pass under `golang:1.26.11-bookworm`.
- `go build ./...` and `go vet ./...` are clean.
- The compose overlay references `artex-opentrap-sidecar:local` (built from `opentrap-sidecar/`); the sidecar image is not published on Docker Hub and must be built locally.
