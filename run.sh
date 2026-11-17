#!/usr/bin/env bash
# ARTEX: the one script to install, build, run, update and maintain ARTEX.
#
#   ./run.sh                        interactive menu
#   ./run.sh docker                 install, build & run with Docker
#   ./run.sh dev                    install, build & run as dev (backend + frontend hot reload)
#   ./run.sh update                 git pull, then rebuild Docker or dev
#   ./run.sh reset-password [...]   reset the admin password (./run.sh reset-password -h)
#   ./run.sh build [...]            build the single binary / release archives (./run.sh build -h)
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }
# json_str prints $1 as a JSON string literal: backslash, double quote and the
# control characters read -r can return are escaped.
json_str(){
  local s="$1"
  s="${s//\\/\\\\}"; s="${s//\"/\\\"}"
  s="${s//$'\t'/\\t}"; s="${s//$'\r'/\\r}"
  printf '"%s"' "$s"
}
# sed_repl escapes $1 for the replacement side of s|...|...|: \, | and &.
sed_repl(){ printf '%s' "$1" | sed 's/[\\|&]/\\&/g'; }

DEFAULT_PORT=8787
DEV_PROXY_ADDR="127.0.0.1:8788"

# load_env exports .env the way docker compose reads it, so dev mode gets the same
# settings (LLM, keys, ports) as the container: KEY=VALUE lines, comments and blanks
# skipped, one pair of surrounding quotes dropped, variables already set in the shell
# kept. Parsed rather than sourced: sourcing would execute the values.
load_env(){
  [ -f .env ] || return 0
  local line key val
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    # Comments, blanks and malformed lines do not match and are skipped.
    [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
    key="${BASH_REMATCH[1]}"; val="${BASH_REMATCH[2]}"
    [ -n "${!key+x}" ] && continue
    if [[ "$val" =~ ^\"(.*)\"$ || "$val" =~ ^\'(.*)\'$ ]]; then val="${BASH_REMATCH[1]}"; fi
    export "$key=$val"
  done < .env
}

# resolve_port defaults and validates ARTEX_PORT (call after load_env).
resolve_port(){
  ARTEX_PORT="${ARTEX_PORT:-$DEFAULT_PORT}"
  case "$ARTEX_PORT" in (*[!0-9]*|'') die "ARTEX_PORT must be a number: $ARTEX_PORT" ;; esac
}

os_id(){ ( . /etc/os-release 2>/dev/null && echo "${ID:-}" ) || true; }
is_wsl(){ grep -qi microsoft /proc/version 2>/dev/null; }

# ── docker environment check / auto-install ───────────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "docker and docker compose detected"; return
  fi
  warn "docker / docker compose not detected"
  case "$(uname -s)" in
    Linux)
      if is_wsl; then
        die "WSL: install Docker Desktop on Windows and enable Settings → Resources → WSL integration for this distro, then reopen the terminal"
      fi
      [ "$(ask 'Install Docker automatically? (y/n)' y)" = y ] || die "please install docker yourself and retry"
      case "$(os_id)" in
        # get.docker.com refuses Kali; its own repository ships the engine and compose v2.
        kali)
          sudo apt-get update
          sudo apt-get install -y docker.io docker-compose docker-buildx
          sudo systemctl enable --now docker ;;
        *)
          curl -fsSL https://get.docker.com | sh ;;
      esac
      sudo usermod -aG docker "$USER" || true
      ok "Docker installed (log out and back in, or run 'newgrp docker', for the group change)" ;;
    Darwin) die "on macOS please install Docker Desktop: https://www.docker.com/products/docker-desktop/" ;;
    MINGW*|MSYS*|CYGWIN*) die "on Windows please install Docker Desktop: https://www.docker.com/products/docker-desktop/ (WSL2 recommended, see README)" ;;
    *)      die "please install docker yourself and retry" ;;
  esac
}

# ── docker: install, build image, run ─────────────────────────
run_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres password (press Enter to generate randomly)' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY (may be empty, configure later in the UI)' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(sed_repl "$pw")|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=$(sed_repl "$key")|" .env
    rm -f .env.bak
    ok ".env generated (POSTGRES_PASSWORD set)"
  else
    info "reusing existing .env"
  fi
  load_env; resolve_port
  info "building the image from this checkout and starting..."
  docker compose up -d --build
  ok "started → http://localhost:$ARTEX_PORT"
  info "setup token: docker compose logs artex | grep 'Setup token'"
  info "view logs:   docker compose logs -f artex"
}

# ── dev: config, install deps, build, run ─────────────────────
write_config(){
  echo "Database setup:"
  echo "  1) Connect to an existing PostgreSQL"
  echo "  2) Start a PostgreSQL with Docker (requires docker)"
  local DB_HOST DB_PORT DB_USER DB_PASS DB_NAME DB_SSL
  case "$(ask 'Choose' 1)" in
    2)
      ensure_docker
      DB_PASS="$(ask 'Postgres password (press Enter for random)' "$(rand)")"
      docker run -d --name artex-pg -p 127.0.0.1:5432:5432 \
        -e POSTGRES_USER=artex -e POSTGRES_PASSWORD="$DB_PASS" -e POSTGRES_DB=artex \
        -v artex-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=artex DB_NAME=artex DB_SSL=disable ;;
    *)
      DB_HOST="$(ask 'Database host' 127.0.0.1)"
      DB_PORT="$(ask 'Port' 5432)"
      DB_USER="$(ask 'User' artex)"
      DB_PASS="$(ask 'Password' '')"
      DB_NAME="$(ask 'Database name' artex)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  case "$DB_PORT" in (*[!0-9]*|'') die "port must be a number: $DB_PORT" ;; esac
  # config.json holds the DB password: owner-only.
  ( umask 077; cat > config.json <<JSON
{
  "database": {
    "host": $(json_str "$DB_HOST"),
    "port": ${DB_PORT},
    "user": $(json_str "$DB_USER"),
    "password": $(json_str "$DB_PASS"),
    "dbname": $(json_str "$DB_NAME"),
    "sslmode": $(json_str "$DB_SSL")
  }
}
JSON
  )
  ok "config.json generated"
}

run_dev(){
  load_env; resolve_port
  command -v go >/dev/null 2>&1 || die "Go not detected; install Go (>=1.26): https://go.dev/dl/"
  command -v npm >/dev/null 2>&1 || die "npm not detected; install Node.js (>=22): https://nodejs.org/"
  ok "$(go version) / node $(node --version)"

  if [ -f config.json ]; then info "reusing existing config.json"; else write_config; fi

  info "installing dependencies..."
  go mod download
  # npm stamps node_modules/.package-lock.json; reinstall only when the lockfile is newer.
  if [ web/package-lock.json -nt web/node_modules/.package-lock.json ]; then
    ( cd web && npm ci )
  else
    info "web/node_modules up to date"
  fi

  info "building backend..."
  go build -o artex ./cmd/artex
  ok "build complete → ./artex"

  # On exit, kill the whole process group (backend + frontend).
  trap 'kill 0 2>/dev/null || true' EXIT INT TERM
  ./artex -addr ":$ARTEX_PORT" -proxy "$DEV_PROXY_ADDR" &
  ( cd web && AUTOPENTEST_API="http://localhost:$ARTEX_PORT" npm run dev ) &
  info "backend :$ARTEX_PORT / proxy $DEV_PROXY_ADDR / frontend http://localhost:3000  (Ctrl-C to exit)"
  wait
}

# ── update: pull the latest code, then rebuild ────────────────
# No manual DB migration: artex idempotently re-runs schema.sql on every start, so
# restarting is migrating. Data (pgdata volume, ./data, ./skills) is not touched.
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "not a git working copy, skipping git pull"; return; }
  [ "$(ask 'Pull the latest code (git pull --ff-only)? (y/n)' y)" = y ] || return 0
  if ! git pull --ff-only; then
    # Continuing would rebuild the OLD tree and still report success.
    cat >&2 <<'RECOVER'
[!] git pull could not fast-forward. This happens when the checkout has local
    changes, or when it was cloned before the history rewrite of 2026-10-23.
    Refusing to rebuild stale code. Recover, then rerun this script:
      keep your changes:  git stash && git fetch origin && git reset --hard origin/main && git stash pop
      discard them:       git fetch origin && git reset --hard origin/main
RECOVER
    die "aborting: nothing was rebuilt"
  fi
}

update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "docker / docker compose not detected; deploy with ./run.sh docker first"
  [ -f .env ] || die ".env not found; deploy with ./run.sh docker first"
  load_env; resolve_port
  # Only artex: postgres stays pinned (a major bump risks compatibility); depends_on
  # starts it if needed and leaves a running one untouched.
  info "building the image from this checkout (artex only)..."
  docker compose build artex
  info "recreating artex (schema migrates automatically on restart)..."
  docker compose up -d artex
  ok "update complete → http://localhost:$ARTEX_PORT"
  info "view logs: docker compose logs -f artex"
  info "clean up old images (optional): docker image prune -f"
}

run_update(){
  echo "Update which deployment?"
  echo "  1) Docker"
  echo "  2) Dev (pull, then reinstall, rebuild and run)"
  case "$(ask 'Choose' 1)" in
    1) sync_repo; update_docker ;;
    2) sync_repo; run_dev ;;
    *) die "invalid choice" ;;
  esac
}

# ── reset-password: admin password reset ──────────────────────
reset_usage(){
  cat <<'EOF'
Reset the ARTEX admin password (username is fixed as ARTEX).

The password is stored as a bcrypt hash under settings.auth.password_hash; pgcrypto
generates it in-database, compatible with the backend (x/crypto/bcrypt).

Modes:
  local (default)  psql from this host. Connection: args > --dsn/$ARTEX_PG_DSN > config.json
  docker           psql inside the postgres container (POSTGRES_* from .env)

Usage:
  ./run.sh reset-password                          prompt for the new password
  ./run.sh reset-password -p 'New-Passphrase!'     at least 12 characters
  ./run.sh reset-password --dsn postgres://u:p@h:5432/artex
  ./run.sh reset-password -H 127.0.0.1 -P 5433 -U artex -W pass -d artex
  ./run.sh reset-password -m docker
  ./run.sh reset-password -m docker -c pg-container-name --exec docker

Options:
  -m, --mode local|docker   -c, --container NAME   --exec compose|docker
  --dsn DSN   -H HOST   -P PORT   -U USER   -W DBPASS   -d DBNAME   --sslmode MODE
  --config FILE   -p, --new-password PASS   -y, --yes   -h, --help

Security: the new password reaches psql via an env var + \getenv (never argv) and is
escaped with :'var'; the DB password goes through PGPASSWORD, also not argv.
EOF
}

# read_config_json prints database.* of a config.json as KEY<TAB>value lines.
read_config_json(){
  local path="$1"
  [[ -f "$path" ]] || return 1
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$path" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1])).get("database", {})
except Exception:
    sys.exit(1)
if d.get("dsn"):
    print("DSN\t" + d["dsn"]); sys.exit(0)
for k in ("host","port","user","password","dbname","sslmode"):
    if d.get(k) is not None:
        print(k.upper() + "\t" + str(d[k]))
PY
  else
    # Minimal fallback: config.json is a flat per-key file.
    local k v
    for k in host port user password dbname sslmode; do
      v=$(grep -oE "\"$k\"[[:space:]]*:[[:space:]]*(\"[^\"]*\"|[0-9]+)" "$path" 2>/dev/null \
            | head -1 | sed -E "s/.*:[[:space:]]*//; s/^\"//; s/\"$//") || true
      [[ -n "$v" ]] && echo -e "${k^^}\t$v"
    done
  fi
}

reset_password(){
  local PASS_KEY="auth.password_hash" BCRYPT_COST=10
  local MODE="" DSN="" HOST="" PORT="" DBUSER="" DBPASS="" DBNAME="" SSLMODE="" CONFIG=""
  local CONTAINER="" EXEC_KIND="" NEWPASS="" NEWPASS2="" ASSUME_YES=0 ans target key val

  while [[ $# -gt 0 ]]; do
    case "$1" in
      -m|--mode)         MODE="${2:-}"; shift 2 ;;
      --dsn)             DSN="${2:-}"; shift 2 ;;
      -H|--host)         HOST="${2:-}"; shift 2 ;;
      -P|--port)         PORT="${2:-}"; shift 2 ;;
      -U|--user)         DBUSER="${2:-}"; shift 2 ;;
      -W|--db-password)  DBPASS="${2:-}"; shift 2 ;;
      -d|--dbname)       DBNAME="${2:-}"; shift 2 ;;
      --sslmode)         SSLMODE="${2:-}"; shift 2 ;;
      --config)          CONFIG="${2:-}"; shift 2 ;;
      -c|--container)    CONTAINER="${2:-}"; shift 2 ;;
      --exec)            EXEC_KIND="${2:-}"; shift 2 ;;
      -p|--new-password) NEWPASS="${2:-}"; shift 2 ;;
      -y|--yes)          ASSUME_YES=1; shift ;;
      -h|--help)         reset_usage; return 0 ;;
      *) die "unknown argument: $1 (see ./run.sh reset-password -h)" ;;
    esac
  done

  if [[ -z "$MODE" ]]; then
    if [[ -n "$DSN$HOST$DBUSER$DBNAME" || -n "${ARTEX_PG_DSN:-}" || -f "${CONFIG:-config.json}" ]]; then
      MODE="local"
    elif command -v docker >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
      MODE="docker"
    else
      MODE="local"
    fi
  fi
  info "deployment mode: $MODE"

  if [[ -z "$NEWPASS" ]]; then
    read -r -s -p "Enter new password (username is fixed as ARTEX): " NEWPASS; echo >&2
    [[ -n "$NEWPASS" ]] || die "password cannot be empty"
    read -r -s -p "Enter again to confirm: " NEWPASS2; echo >&2
    [[ "$NEWPASS" == "$NEWPASS2" ]] || die "the two entries do not match"
  fi
  [[ -n "$NEWPASS" ]] || die "password cannot be empty"
  # Same minimum as the server (minPasswordRunes in server/auth.go), counted in
  # characters like the server: UTF-8 bytes minus continuation bytes, whatever the locale.
  local NEWPASS_CHARS
  NEWPASS_CHARS=$(printf '%s' "$NEWPASS" | LC_ALL=C tr -d '\200-\277' | wc -c)
  (( NEWPASS_CHARS >= 12 )) || die "password must be at least 12 characters"

  export ARTEX_RESET_NEWPASS="$NEWPASS"
  local SQL
  SQL=$(cat <<SQL
\\set ON_ERROR_STOP on
\\getenv newpw ARTEX_RESET_NEWPASS
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO settings(key, value)
VALUES ('$PASS_KEY', crypt(:'newpw', gen_salt('bf', $BCRYPT_COST)))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
SQL
)

  if [[ "$MODE" == "local" ]]; then
    if [[ -z "$DSN" && -z "$HOST$DBUSER$DBNAME" ]]; then
      [[ -n "${ARTEX_PG_DSN:-}" ]] && DSN="$ARTEX_PG_DSN"
    fi
    if [[ -z "$DSN" && -z "$HOST$DBUSER$DBNAME" ]]; then
      local cfg="${CONFIG:-config.json}"
      if [[ -f "$cfg" ]]; then
        info "reading database config from $cfg"
        while IFS=$'\t' read -r key val; do
          case "$key" in
            DSN)      [[ -z "$DSN" ]] && DSN="$val" ;;
            HOST)     [[ -z "$HOST" ]] && HOST="$val" ;;
            PORT)     [[ -z "$PORT" ]] && PORT="$val" ;;
            USER)     [[ -z "$DBUSER" ]] && DBUSER="$val" ;;
            PASSWORD) [[ -z "$DBPASS" ]] && DBPASS="$val" ;;
            DBNAME)   [[ -z "$DBNAME" ]] && DBNAME="$val" ;;
            SSLMODE)  [[ -z "$SSLMODE" ]] && SSLMODE="$val" ;;
          esac
        done < <(read_config_json "$cfg")
      fi
    fi

    command -v psql >/dev/null 2>&1 || die "psql not found (install postgresql-client, or use -m docker)"

    local -a PSQL_ARGS=()
    if [[ -n "$DSN" ]]; then
      PSQL_ARGS=("$DSN")
      target="$DSN"
    else
      [[ -n "$DBUSER" ]] || die "missing database user (-U) or a valid config.json/DSN"
      [[ -n "$DBNAME" ]] || die "missing database name (-d) or a valid config.json/DSN"
      HOST="${HOST:-127.0.0.1}"; PORT="${PORT:-5432}"; SSLMODE="${SSLMODE:-disable}"
      PSQL_ARGS=(-h "$HOST" -p "$PORT" -U "$DBUSER" -d "$DBNAME")
      export PGSSLMODE="$SSLMODE"
      [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
      target="$DBUSER@$HOST:$PORT/$DBNAME"
    fi

    info "target database: $target"
    if [[ "$ASSUME_YES" -ne 1 ]]; then
      read -r -p "Reset the ARTEX password in this database? [y/N] " ans
      [[ "$ans" == "y" || "$ans" == "Y" ]] || die "cancelled"
    fi
    if ! printf '%s\n' "$SQL" | psql "${PSQL_ARGS[@]}" -v ON_ERROR_STOP=1 -q >/dev/null; then
      die "write failed. If it reports a pgcrypto permission/missing error, use a role allowed to create extensions, or run CREATE EXTENSION pgcrypto manually first."
    fi
  else
    command -v docker >/dev/null 2>&1 || die "docker not found"
    CONTAINER="${CONTAINER:-postgres}"
    if [[ -z "$EXEC_KIND" ]]; then
      if docker compose version >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
        EXEC_KIND="compose"
      else
        EXEC_KIND="docker"
      fi
    fi

    # psql credentials: command line, then POSTGRES_* from .env, then compose defaults.
    load_env
    local DUSER="${DBUSER:-${POSTGRES_USER:-artex}}" DNAME="${DBNAME:-${POSTGRES_DB:-artex}}"
    [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
    [[ -z "${PGPASSWORD:-}" && -n "${POSTGRES_PASSWORD:-}" ]] && export PGPASSWORD="$POSTGRES_PASSWORD"

    info "target: psql -U $DUSER -d $DNAME inside container $CONTAINER (exec=$EXEC_KIND)"
    if [[ "$ASSUME_YES" -ne 1 ]]; then
      read -r -p "Reset the ARTEX password in this container database? [y/N] " ans
      [[ "$ans" == "y" || "$ans" == "Y" ]] || die "cancelled"
    fi

    # -e NAME without a value inherits it, so the password never appears in the docker argv.
    local -a EXEC_CMD
    if [[ "$EXEC_KIND" == "compose" ]]; then
      EXEC_CMD=(docker compose exec -T -e ARTEX_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
                psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
    else
      EXEC_CMD=(docker exec -i -e ARTEX_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
                psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
    fi
    if ! printf '%s\n' "$SQL" | "${EXEC_CMD[@]}" >/dev/null; then
      die "write failed. Check the container name (-c), the database account (POSTGRES_* in .env), and that the role has pgcrypto permission."
    fi
  fi

  unset ARTEX_RESET_NEWPASS
  ok "ARTEX admin password has been reset. Log in with username ARTEX + the new password (no restart needed)."
}

# ── build: single binary / release archives ───────────────────
build_usage(){
  cat <<'EOF'
Usage:
  ./run.sh build                         build for the current OS and architecture
  ./run.sh build --target linux/amd64    build one specific target
  ./run.sh build --release               build and zip all supported targets

Options:
  --release              linux/darwin amd64+arm64 and windows/amd64, zipped, with SHA256SUMS
  --target OS/ARCH       a single target, e.g. windows/amd64
  --upx                  force UPX compression (may break some Linux environments)
  --no-compress          no UPX; Go linker trimming and zip compression only
  -h, --help             show this help

Environment:
  ARTEX_TARGET_OS / ARTEX_TARGET_ARCH   single target (default: host)
  ARTEX_TARGETS=linux/amd64,...         multi-target list
  ARTEX_BUILD_VERSION=vX.Y.Z            version embedded in the binary and archive name
  ARTEX_OUTPUT=/path/to/artex           explicit binary path in single-target mode
  ARTEX_OUTPUT_DIR=dist                 directory for default binary paths
  ARTEX_PACKAGE=1                       zip each target
  ARTEX_PACKAGE_DIR=dist                directory for release archives
  ARTEX_COMPRESS=off|auto|required      UPX mode
  ARTEX_UPX_ARGS="--best --lzma"        arguments passed to UPX
  ARTEX_SKIP_FRONTEND=1                 reuse server/webui/dist (CI artifact / Docker builds)
  ARTEX_SKIP_NPM_CI=1                   skip npm ci while rebuilding the frontend
  ARTEX_GOSUMDB=sum.golang.org          Go checksum database
EOF
}

compress_binary(){
  local binary="$1" goos="$2" before after upx_args
  case "$ARTEX_COMPRESS" in
    0|off|false|none) info "skipping UPX: $binary"; return 0 ;;
    auto|required|true|1) ;;
    *) die "ARTEX_COMPRESS must be off, auto, or required" ;;
  esac
  if ! command -v upx >/dev/null 2>&1; then
    [ "$ARTEX_COMPRESS" = "required" ] && die "ARTEX_COMPRESS=required but upx was not detected"
    warn "upx not detected; keeping the linker-compressed result: $binary"
    return 0
  fi
  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$ARTEX_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    [ "$ARTEX_COMPRESS" = "required" ] && die "UPX compression failed: $binary"
    warn "UPX does not support this target format; keeping the uncompressed binary: $binary"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "UPX compression done: $binary (${before} -> ${after} bytes)"
}

package_binary(){
  local binary="$1" goos="$2" goarch="$3"
  local package_name="artex-${ARTEX_BUILD_VERSION}-${goos}-${goarch}"
  local package_root="${ARTEX_PACKAGE_DIR}/${package_name}"
  local archive="${ARTEX_PACKAGE_DIR}/${package_name}.zip"
  command -v zip >/dev/null 2>&1 || die "packaging requires zip"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  cp -R skills "$package_root/"
  cp config.example.json "$package_root/"
  if [ -f README.md ]; then cp README.md "$package_root/"; fi
  (cd "$ARTEX_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "release archive: $archive"
}

build_target(){
  local target="$1" goos goarch binary_name output
  case "$target" in */*) ;; *) die "invalid target: $target (must be OS/ARCH)" ;; esac
  goos="${target%%/*}"; goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "unsupported OS: $goos (supported: linux, darwin, windows)" ;;
  esac
  binary_name="artex"
  [ "$goos" = "windows" ] && binary_name="artex.exe"
  if [ -n "${ARTEX_OUTPUT:-}" ] && [ "$ARTEX_RELEASE" != "1" ]; then
    output="$ARTEX_OUTPUT"
  else
    output="${ARTEX_OUTPUT_DIR}/artex-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  info "compiling ${goos}/${goarch}, version ${ARTEX_BUILD_VERSION}"
  GOSUMDB="$ARTEX_GOSUMDB" CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -tags embedui -trimpath \
      -ldflags "-s -w -buildid= -X main.version=${ARTEX_BUILD_VERSION}" \
      -o "$output" ./cmd/artex

  compress_binary "$output" "$goos"
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$ARTEX_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "build complete: $output"
}

write_checksums(){
  [ "$ARTEX_PACKAGE" = "1" ] || return 0
  local checksum_file="$ARTEX_PACKAGE_DIR/SHA256SUMS" sum
  if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then sum="shasum -a 256"
  else warn "neither sha256sum nor shasum detected; skipping SHA256SUMS"; return 0
  fi
  # shellcheck disable=SC2086
  (cd "$ARTEX_PACKAGE_DIR" && for archive in *.zip; do $sum "$archive"; done > "$(basename "$checksum_file")")
  ok "checksum file: $checksum_file"
}

run_build(){
  local RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"
  ARTEX_RELEASE="${ARTEX_RELEASE:-0}"
  ARTEX_COMPRESS="${ARTEX_COMPRESS:-off}"
  ARTEX_PACKAGE="${ARTEX_PACKAGE:-0}"
  local target_arg target
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --release) ARTEX_RELEASE=1; ARTEX_PACKAGE=1; shift ;;
      --target)
        [ "$#" -ge 2 ] || die "--target requires an OS/ARCH argument"
        target_arg="$2"
        case "$target_arg" in
          */*) ARTEX_TARGET_OS="${target_arg%%/*}"; ARTEX_TARGET_ARCH="${target_arg##*/}"; ARTEX_TARGETS="$target_arg" ;;
          *) die "target must be OS/ARCH, e.g. linux/amd64" ;;
        esac
        shift 2 ;;
      --no-compress) ARTEX_COMPRESS=0; shift ;;
      --upx) ARTEX_COMPRESS=required; shift ;;
      -h|--help) build_usage; return 0 ;;
      *) die "unknown argument: $1 (see ./run.sh build -h)" ;;
    esac
  done

  command -v go >/dev/null 2>&1 || die "Go not detected (the project requires Go 1.26 or newer)"
  ARTEX_GOSUMDB="${ARTEX_GOSUMDB:-sum.golang.org}"
  if [ -z "${ARTEX_BUILD_VERSION:-}" ]; then
    if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
      ARTEX_BUILD_VERSION="$(git describe --tags --always --dirty)"
    else
      ARTEX_BUILD_VERSION="dev"
    fi
  fi
  # Release tags usually carry a v prefix; keep the binary version consistent.
  ARTEX_BUILD_VERSION="${ARTEX_BUILD_VERSION#v}"
  ARTEX_OUTPUT_DIR="${ARTEX_OUTPUT_DIR:-dist}"
  ARTEX_PACKAGE_DIR="${ARTEX_PACKAGE_DIR:-$ARTEX_OUTPUT_DIR}"
  ARTEX_UPX_ARGS="${ARTEX_UPX_ARGS:---best --lzma}"

  if [ "$ARTEX_RELEASE" = "1" ]; then
    ARTEX_TARGETS="${ARTEX_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
  else
    ARTEX_TARGET_OS="${ARTEX_TARGET_OS:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOOS)}"
    ARTEX_TARGET_ARCH="${ARTEX_TARGET_ARCH:-$(GOSUMDB="$ARTEX_GOSUMDB" go env GOARCH)}"
    ARTEX_TARGETS="${ARTEX_TARGETS:-${ARTEX_TARGET_OS}/${ARTEX_TARGET_ARCH}}"
  fi

  if [ "${ARTEX_SKIP_FRONTEND:-0}" = "1" ]; then
    [ -d server/webui/dist ] || die "ARTEX_SKIP_FRONTEND=1 but server/webui/dist does not exist"
  else
    command -v npm >/dev/null 2>&1 || die "npm not detected (building the frontend requires Node.js/npm)"
    command -v rsync >/dev/null 2>&1 || die "rsync not detected"
    info "building frontend static assets"
    if [ "${ARTEX_SKIP_NPM_CI:-0}" != "1" ]; then (cd web && npm ci); fi
    (cd web && npm run build:static)
    info "syncing frontend assets to server/webui/dist"
    mkdir -p server/webui/dist
    rsync -a --delete web/out/ server/webui/dist/
  fi

  mkdir -p "$ARTEX_OUTPUT_DIR"
  if [ "$ARTEX_PACKAGE" = "1" ]; then mkdir -p "$ARTEX_PACKAGE_DIR"; fi
  local -a targets
  IFS=',' read -r -a targets <<< "$ARTEX_TARGETS"
  [ "${#targets[@]}" -gt 0 ] || die "ARTEX_TARGETS must not be empty"
  for target in "${targets[@]}"; do
    target="${target//[[:space:]]/}"
    [ -n "$target" ] || continue
    build_target "$target"
  done
  if [ "$ARTEX_PACKAGE" = "1" ]; then
    write_checksums
    info "release packages generated in: $ARTEX_PACKAGE_DIR"
  fi
}

# ── entry point ───────────────────────────────────────────────
menu(){
  echo "=============================="
  echo "  ARTEX"
  echo "  1) Install & run with Docker"
  echo "  2) Install & run as dev (hot reload)"
  echo "  3) Update (git pull + rebuild)"
  echo "  4) Reset admin password"
  echo "  5) Build single binary"
  echo "=============================="
  case "$(ask 'Choose' 1)" in
    1) run_docker ;;
    2) run_dev ;;
    3) run_update ;;
    4) reset_password ;;
    5) run_build ;;
    *) die "invalid choice" ;;
  esac
}

cmd="${1:-}"
[ "$#" -gt 0 ] && shift
case "$cmd" in
  '')             menu ;;
  docker)         run_docker ;;
  dev)            run_dev ;;
  update)         run_update ;;
  reset-password) reset_password "$@" ;;
  build)          run_build "$@" ;;
  -h|--help|help) sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) die "unknown command: $cmd (see ./run.sh --help)" ;;
esac
