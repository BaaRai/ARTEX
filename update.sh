#!/usr/bin/env bash
# ARTEX update script: (1) Docker update (rebuild the image from this checkout)  (2) local build update (rebuild the binary)
# Counterpart of install.sh: install handles the first-time setup, update upgrades to a new version.
# No manual DB migration needed — artex idempotently re-runs schema.sql on every start (with ADD COLUMN/CREATE
# INDEX IF NOT EXISTS), so "restart equals migrate". Data (pgdata volume, ./data, ./skills) is not affected.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }

# ── Build identity: keep .env and the binary in step with the checkout ─────────
BUILD_VERSION="dev"
BUILD_REVISION=""

# set_env replaces KEY=... in .env (or appends it) without touching other lines.
set_env(){
  local key="$1" value="$2" tmp
  tmp="$(mktemp)"
  awk -v k="$key" -v v="$value" '
    $0 ~ "^" k "=" { print k "=" v; found=1; next }
    { print }
    END { if (!found) print k "=" v }
  ' .env > "$tmp"
  chmod --reference=.env "$tmp" 2>/dev/null || true
  mv "$tmp" .env
}

# stamp_version records the commit the image is about to be built from. Without
# it docker-compose.yml passes ARTEX_VERSION=dev and every local install reports
# the same meaningless version in the UI and the startup banner.
stamp_version(){
  if ! command -v git >/dev/null 2>&1 || ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    warn "not a git working copy: leaving ARTEX_VERSION in .env unchanged"
    return 0
  fi
  BUILD_VERSION="$(git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD)"
  BUILD_REVISION="$(git rev-parse HEAD 2>/dev/null || true)"
  if [ -f .env ]; then
    set_env ARTEX_VERSION "$BUILD_VERSION"
    [ -n "$BUILD_REVISION" ] && set_env ARTEX_REVISION "$BUILD_REVISION"
  fi
  ok "Build identity: ${BUILD_VERSION}${BUILD_REVISION:+ (${BUILD_REVISION:0:12})}"
}

# ── Optional: sync the repo to the latest code (compose/scripts/local build sources all update through it) ───────
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "not a git working copy, skipping git pull"; return 0; }
  if [ "$(ask 'Pull the latest code (git pull --ff-only)? (y/n)' y)" = y ]; then
    if ! git pull --ff-only; then
      # Continuing would rebuild the OLD tree and still print "Build complete":
      # exactly the silent stale upgrade this script exists to prevent.
      cat >&2 <<'RECOVER'
[!] git pull could not fast-forward. This happens when the checkout has local
    changes, or when it was cloned before the history rewrite of 2026-10-23.
    Refusing to rebuild stale code. Recover, then rerun this script:
      keep your changes:  git stash && git fetch origin && git reset --hard origin/main && git stash pop
      discard them:       git fetch origin && git reset --hard origin/main
RECOVER
      die "aborting: nothing was rebuilt"
    fi
  fi
}

# ── (1) Docker update ───────────────────────────────
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "docker / docker compose not detected; install and deploy with ./install.sh first"
  docker buildx version >/dev/null 2>&1 \
    || die "docker buildx not detected; the image build needs it (see the Docker section of the README)"
  [ -f .env ] || die ".env not found; run ./install.sh first to complete the initial deployment"

  # Touch only artex: postgres is pinned to 16-alpine and does not need upgrading along
  # (pulling it wastes bandwidth, and a major version bump risks compatibility). artex
  # declares depends_on postgres, so an up with the service name starts pg if needed and leaves a running one untouched.
  info "Building the image from this checkout (artex only)…"
  docker compose build artex
  info "Recreating artex (schema migrates automatically on restart)…"
  docker compose up -d artex
  ok "Update complete → http://localhost:8787"
  info "View logs: docker compose logs -f artex"
  info "Clean up old images (optional): docker image prune -f"
}

# ── (2) Local build update ──────────────────────────────
update_local(){
  command -v go >/dev/null 2>&1 || die "Go (>=1.26) not detected: https://go.dev/dl/"
  [ -f config.json ] || warn "config.json not found — use ./install.sh instead for a first-time deployment"
  ok "Go: $(go version)"
  LDFLAGS="-X main.version=${BUILD_VERSION}"
  [ -n "${BUILD_REVISION}" ] && LDFLAGS="${LDFLAGS} -X main.revision=${BUILD_REVISION}"

  if command -v npm >/dev/null 2>&1; then
    info "Rebuilding frontend static assets…"
    ( cd web && npm ci --ignore-scripts && npm run build:static )
    rm -rf server/webui/dist && mkdir -p server/webui && cp -r web/out server/webui/dist
    info "Recompiling the embedded single binary (version ${BUILD_VERSION})…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -ldflags "${LDFLAGS}" -o artex ./cmd/artex
  else
    warn "npm not detected: building a backend **without the embedded frontend** (run the frontend separately with npm run dev)"
    CGO_ENABLED=0 go build -ldflags "${LDFLAGS}" -o artex ./cmd/artex
  fi
  ok "Build complete → ./artex"
  warn "Restart the running artex process to take effect (schema migrates automatically on restart)"
}

echo "=============================="
echo "  ARTEX Update"
echo "  1) Docker update (pull new image and rebuild)"
echo "  2) Local update (rebuild with go)"
echo "=============================="
case "$(ask 'Choose' 1)" in
  1) sync_repo; stamp_version; update_docker ;;
  2) sync_repo; stamp_version; update_local ;;
  *) die "invalid choice" ;;
esac
