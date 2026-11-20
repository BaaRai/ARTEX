#!/usr/bin/env bash
# Applies the [hardening] intercept rules and skill visibility (idempotent),
# then restarts the artex service so the interceptor reloads its rule cache.
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; [ -f .env ] && . ./.env; set +a
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER:-artex}" -d "${POSTGRES_DB:-artex}" < hardening/intercept-rules.sql
docker compose restart artex
docker compose exec -T postgres psql -U "${POSTGRES_USER:-artex}" -d "${POSTGRES_DB:-artex}" -c \
  "SELECT id, name, action, timeout_seconds, timeout_action FROM intercept_rules WHERE name LIKE '[hardening]%' ORDER BY priority DESC"
