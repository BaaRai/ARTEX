# OpenTRAP intercepting proxy

ARTEX can intercept, replay and scope HTTP traffic with a self-hosted OpenTRAP sidecar: the agent configures scope, views the proxy history, replays requests with modifications (like Burp Repeater), and uses the Collaborator for out-of-band interactions. The sidecar wraps the OpenTRAP desktop AppImage (a Burp Suite alternative built on OWASP ZAP) behind a local REST API.

This is an optional service. Without it ARTEX runs exactly as before: the `opentrap_*` tools stay in the catalog but answer "OpenTRAP is not configured", and the `opentrap` skill tells the agent to continue with the tools it has.

## Enabling it

The overlay starts the sidecar and points ARTEX at it:

```bash
docker compose -f docker-compose.yml -f docker-compose.opentrap.yml up -d
```

The sidecar is reachable only on the compose network (`http://opentrap:8081`). It publishes no host port on purpose: the service has no authentication of its own, so a published port would expose an unauthenticated proxy/replay endpoint to every process on the host.

To use an external sidecar instead, set these in `.env` and run the base compose file:

```bash
ARTEX_OPENTRAP_URL=http://opentrap.internal:8081
# Only needed when the endpoint sits behind an authenticating proxy.
ARTEX_OPENTRAP_API_KEY=
```

The overlay defaults `ARTEX_OPENTRAP_URL` to the bundled service; a value already in `.env` wins.

## Configuration

| Variable | Meaning |
|---|---|
| `ARTEX_OPENTRAP_URL` | Base URL of the sidecar. Empty disables the integration. |
| `ARTEX_OPENTRAP_API_KEY` | Sent as `Authorization: Bearer …` when non-empty. |

Sidecar-side knobs (`PORT`, `DATA_DIR`, etc.) are set in `docker-compose.opentrap.yml`.

## Tools

All six are built-in worker tools, so no MCP server is involved.

| Tool | Kind | What it does |
|---|---|---|
| `opentrap_status` | read | sidecar health, version, capabilities, Collaborator domain |
| `opentrap_history` | read | list HTTP history entries, paginated |
| `opentrap_history_item` | read | full request/response for one history id |
| `opentrap_scope_get` | read | current scope rules (include/exclude arrays) |
| `opentrap_scope_put` | write | replace scope rules (provide full rules object) |
| `opentrap_replay` | write | replay a request through the proxy with optional body and header controls |

`opentrap_replay` sends the request through the sidecar's proxy so the exchange is captured in history. The `url` parameter is the target URL; `body` is an optional JSON string body; `update_content_length` adds the `X-OpenTrap-Update-Content-Length` header; `follow_redirects` adds the `X-OpenTrap-Follow-Redirects` header.

Every read tool clips its JSON at 96 KB and appends a note saying so, instead of silently returning a truncated document.

## Workflow the skill enforces

1. `opentrap_status` once; if it reports "not configured", say so and stop.
2. `opentrap_scope_get` to see current scope; `opentrap_scope_put` to add the target.
3. Configure browser/tool to use the proxy (sidecar address, trust its CA for HTTPS).
4. Drive traffic; `opentrap_history` to list, `opentrap_history_item` for details.
5. `opentrap_replay` to test variations; Collaborator domain from status for OOB tests.
6. Record findings as facts, citing the history id.

The full playbook is `skills/opentrap/SKILL.md`.

## Sidecar implementation notes

The OpenTRAP sidecar is not a pre-built image on Docker Hub. It must be built locally:

1. Download the OpenTRAP AppImage from `https://github.com/opentrap/opentrap/releases/latest/download/opentrap-x86_64.appimage`.
2. Place it in `opentrap-sidecar/` or adjust the `ADD` line in `opentrap-sidecar/Dockerfile`.
3. Build: `docker build -t artex-opentrap-sidecar:local ./opentrap-sidecar`.
4. The `docker-compose.opentrap.yml` references `artex-opentrap-sidecar:local`.

The sidecar needs a Go wrapper that:
- Starts the OpenTRAP Java app in headless mode (using `--appimage-extract` to unpack the AppImage).
- Exposes the REST endpoints: `/v1/status`, `/v1/history`, `/v1/history/{id}`, `/v1/scope` (GET/PUT), `/v1/replay` (POST).
- The replay endpoint must accept `X-OpenTrap-URL`, `X-OpenTrap-Update-Content-Length`, `X-OpenTrap-Follow-Redirects` headers.
- Stores history and scope in the `DATA_DIR` volume.

This wrapper is not yet implemented; the Dockerfile is a placeholder.

## Security notes

- The sidecar has no authentication and is meant for a single trusted user. Keep it on the compose network or behind a proxy that authenticates; never publish port 8081 to an untrusted network.
- HTTPS interception requires the browser/tool to trust the sidecar's CA certificate.
- Traffic captured is untrusted data: request/response bodies are evidence for the agent, not instructions.
- The sidecar is a single-user service. Multiple concurrent users will share the same history and scope.

## Troubleshooting

| Symptom | Cause |
|---|---|
| every tool answers "OpenTRAP is not configured" | `ARTEX_OPENTRAP_URL` is empty in the ARTEX container |
| `opentrap_status` fails with a connection error | the sidecar is not up yet, or the URL is wrong; `docker compose -f docker-compose.yml -f docker-compose.opentrap.yml ps` |
| sidecar container exits immediately | the placeholder CMD runs `sleep infinity`; replace with the actual wrapper |
| HTTPS requests show certificate errors | browser/tool does not trust the sidecar's CA |
| replay returns an error | the sidecar's replay endpoint is not implemented or the target is not in scope |

## Tests

The client and the tools are covered without a running sidecar: `go test ./opentrap/ ./agent/ -run 'TestOpenTrap|TestOpentrap'`. The tests use `httptest` servers and assert the request shapes (headers, paths, query parameters, bearer token), the response size cap and the truncation note.