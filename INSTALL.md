# Install ARTEX

ARTEX is self-hosted. The supported setup is Docker Compose, which starts ARTEX and PostgreSQL
together. You need an LLM API key for exploration, but the console starts without one and you can
add it later in the UI.

| Path | Needs | Use it when |
| --- | --- | --- |
| One-click script | Docker with Compose v2, or Go 1.26 and Node 22 | You want the script to choose Docker or a local build |
| Docker Compose | Docker Engine 24+ or Docker Desktop, Compose v2, buildx | You want to see and control every step |
| Prebuilt binary or source build | Go 1.26, Node 22, an existing PostgreSQL | You cannot run Docker or want a single binary |

## Requirements

- Docker Engine with the Compose v2 plugin and the buildx plugin. Docker Desktop includes both.
  The checkout build stops at `failed to parse platform` when buildx is missing.
- PostgreSQL 16 is started by the Compose files. A local binary or a standalone container needs an
  existing database.
- An Anthropic or OpenAI compatible API key for exploration. It can be set in `.env` or later in
  **System, LLM**.
- Ports 8787 (console) and 8788 (recording proxy) free on the host.

## Option 1: one-click script, recommended

```bash
git clone https://github.com/BaaRai/ARTEX.git
cd ARTEX
./install.sh
```

Choose **1) all-Docker**. The script creates `.env`, starts the stack and prints the URL. It can
also install Docker on supported Linux distributions.

Open <http://localhost:8787>. On the first visit, `/setup` asks for the one-time setup token printed
at startup:

```bash
docker compose logs artex | grep "Setup token"
```

## Option 2: Docker Compose manual

```bash
git clone https://github.com/BaaRai/ARTEX.git
cd ARTEX
cp .env.example .env            # set POSTGRES_PASSWORD, optionally an LLM key
docker compose up -d --build
docker compose logs artex | grep "Setup token"
```

Open <http://localhost:8787>, paste the setup token, then choose the console password.

Check the stack:

```bash
docker compose ps
docker compose port artex 8787
curl -sS http://127.0.0.1:8787/api/health
```

`/api/health` answers `{"ok":true}` without a session. `docker compose port` should show
`127.0.0.1:8787`.

### Docker Desktop on Windows and macOS

Docker Desktop serves host bind mounts in a way that defeats the agent isolation self-test, so the
`artex` container can restart in a loop with `self-test failed`. Layer the named-volume file:

```bash
docker compose -f docker-compose.yml -f docker-compose.desktop.yml up -d --build
```

Use the same `-f` pair for later commands (`logs`, `up -d artex`, `down`). Docker Engine inside WSL2
with the repository under `/mnt/c` has the same problem: use the overlay or move the checkout to the
WSL2 native filesystem. See the README section
[Docker Desktop on Windows and macOS](README.md#docker-desktop-on-windows-and-macos) for the
migration of an existing `keys` and `data` directory.

### Access from another machine

The console binds `127.0.0.1` by default. To reach it from Windows when ARTEX runs in WSL, from a
remote server, or from another host, either keep the loopback binding and use a tunnel:

```bash
ssh -L 8787:127.0.0.1:8787 user@server
```

then open <http://localhost:8787> on your machine, or expose it deliberately:

```bash
# .env
ARTEX_BIND_ADDR=0.0.0.0
```

```bash
docker compose up -d artex
```

Only expose it behind a firewall or a reverse proxy. The setup token protects the first-run claim,
not the console afterwards.

## Other installation paths

- Prebuilt binaries and release archives: see [Installation](README.md#installation) options 3 and 5.
- Build the binary from source: see option 4 in the same section.
- Prebuilt GHCR images: the image is published from the next tagged release. Until then,
  `docker-compose.ghcr.yml` and `docker pull ghcr.io/baarai/artex` return `denied`; use the checkout
  build above.

## Troubleshooting

### `http://localhost:8787` does not answer

1. `docker compose ps` must show `postgres` and `artex` as running, not restarting.
2. `docker compose port artex 8787` must show a mapping. If it prints nothing, you started the wrong
   Compose file or an override removed the ports.
3. `curl -sS http://127.0.0.1:8787/api/health` on the Docker host. If it returns `{"ok":true}`, ARTEX
   is up and the problem is networking or the browser.
4. Read the startup log: `docker compose logs --tail=120 artex`. Do not share the setup token.
5. If the browser is not on the Docker host, see [Access from another machine](#access-from-another-machine).

### The `artex` container restarts in a loop

Look for `self-test failed` or `crash` in the logs. On Docker Desktop, or with Docker Engine inside
WSL2 on `/mnt/c`, use the [Desktop overlay](#docker-desktop-on-windows-and-macos). On native Linux,
check that `./data`, `./keys` and `./skills` are on a filesystem that enforces POSIX ownership, not a
Windows or 9p mount.

### `docker buildx version` fails

Install the buildx plugin:

- Debian/Ubuntu: `sudo apt-get install docker-buildx-plugin`
- Fedora: `sudo dnf install docker-buildx-plugin`
- macOS with Colima: `brew install docker-buildx`

### `fork/exec /usr/bin/bash: no such file or directory`

The agent Bash tool starts `bash` before running your command, so your command never ran. This is a
host or image problem, not a command error. Inside the container or on the host:

```bash
ls -l /usr/bin/bash /bin/bash /bin/sh
file /usr/bin/bash
ldd /usr/bin/bash
```

Install bash if it is missing (`apt-get install -y bash` on Debian/Ubuntu, `apk add bash` on Alpine).
If only `/bin/bash` exists, create the expected path with `ln -s /bin/bash /usr/bin/bash` as root, or
use the official Docker image, which includes bash. Restart ARTEX after the fix.

### The top right says `dev`

`dev` means the build carried no version stamp, not a different branch. A checkout built through
`install.sh` or `update.sh` on current main stamps `ARTEX_VERSION` from `git describe` and
`ARTEX_REVISION` from `git rev-parse HEAD`. To identify an older local build, run `git describe
--tags --always --dirty` or `git rev-parse HEAD` in the checkout it was built from. If the checkout is
gone, the image ID and build date are the only remaining identifiers.

### Port 8787 or 8788 is already allocated

Stop the process using the port (`ss -ltnp | grep 8787`) or change the host side of the mapping in
`docker-compose.yml`. The container side stays 8787.

### I lost the setup token

The token is printed at startup. Read it again from the current container log:

```bash
docker compose logs artex | grep -i "setup token"
```

If the first-run setup is already complete, use the console password instead. Restarting the stack
does not reset an existing password.

### The GHCR image cannot be pulled

The release image is not published yet. Use the checkout build in [Option 2](#option-2-docker-compose-manual)
until the next tagged release.

## Update and back up

Update a checkout build with `./update.sh`, or rebuild with `docker compose up -d --build` after a
`git pull`. Back up PostgreSQL, `./data` and `./keys`: see [Backups](README.md#backups). A database
dump without `keys/secret.key` cannot restore the encrypted secrets.

## Getting help

Open an installation issue with your OS, Docker setup, installation method, `docker compose ps`,
the exact error and the last 120 log lines. Remove the setup token, passwords, API keys and target
data first. The issue form asks for these fields.
