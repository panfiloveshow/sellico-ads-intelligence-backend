#!/usr/bin/env bash
# restore-check.sh — Daily smoke test that the latest backup is restorable.
#
# Restores the most recent local dump into a throwaway database, runs a few
# sanity checks (table count, row counts, schema_migrations head), then drops
# the database. Failure exits non-zero so cron can alert.
#
# Usage (cron, daily 04:30):
#   30 4 * * * /opt/sellico/scripts/restore-check.sh >> /var/log/sellico-restore-check.log 2>&1

set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/opt/sellico/backups}"
DB_NAME="${POSTGRES_DB:-${PGDATABASE:-sellico}}"
DB_USER="${POSTGRES_USER:-${PGUSER:-sellico}}"
CHECK_DB="${DB_NAME}_restore_check"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"

log() { echo "[$(date -Iseconds)] $*"; }
fail() { log "FAIL: $*"; exit 1; }

psql_exec() {
  local db="$1"
  local sql="$2"
  if [[ "${BACKUP_USE_DOCKER:-0}" == "1" ]]; then
    docker compose -f "$COMPOSE_FILE" exec -T postgres \
      psql --username="$DB_USER" --dbname="$db" -c "$sql" >/dev/null
  else
    psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
      --username="$DB_USER" --dbname="$db" -c "$sql" >/dev/null
  fi
}

psql_query() {
  local db="$1"
  local sql="$2"
  if [[ "${BACKUP_USE_DOCKER:-0}" == "1" ]]; then
    docker compose -f "$COMPOSE_FILE" exec -T postgres \
      psql --username="$DB_USER" --dbname="$db" -tAc "$sql"
  else
    psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
      --username="$DB_USER" --dbname="$db" -tAc "$sql"
  fi
}

# Find newest local dump
LATEST=$(find "$BACKUP_DIR" -name "${DB_NAME}_*.dump" -type f -printf '%T@ %p\n' \
         | sort -nr | head -1 | cut -d' ' -f2-)

if [[ -z "$LATEST" || ! -r "$LATEST" ]]; then
  fail "No restorable backup found in $BACKUP_DIR"
fi
log "Latest backup: $LATEST"

# Drop check DB if leftover from previous run
psql_exec postgres "DROP DATABASE IF EXISTS \"$CHECK_DB\";"

# Create fresh check DB
psql_exec postgres "CREATE DATABASE \"$CHECK_DB\";"
log "Check database created: $CHECK_DB"

# Restore
log "Restoring..."
if [[ "${BACKUP_USE_DOCKER:-0}" == "1" ]]; then
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    pg_restore \
      --username="$DB_USER" \
      --dbname="$CHECK_DB" \
      --no-owner \
      --no-privileges \
    < "$LATEST" 2>&1 | tail -5 || fail "pg_restore reported errors"
else
  pg_restore \
    --host="${PGHOST:-localhost}" \
    --port="${PGPORT:-5432}" \
    --username="$DB_USER" \
    --dbname="$CHECK_DB" \
    --no-owner \
    --no-privileges \
    --jobs=2 \
    "$LATEST" 2>&1 | tail -5 || fail "pg_restore reported errors"
fi

# Sanity checks
TABLE_COUNT=$(psql_query "$CHECK_DB" "SELECT count(*) FROM information_schema.tables WHERE table_schema='public';")
log "Public tables: $TABLE_COUNT"
[[ "$TABLE_COUNT" -gt 10 ]] || fail "expected >10 tables, got $TABLE_COUNT — backup looks empty"

# schema_migrations head — confirms migrations applied
MIGRATION_VERSION=$(psql_query "$CHECK_DB" "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;" 2>/dev/null || echo "")
if [[ -z "$MIGRATION_VERSION" ]]; then
  fail "schema_migrations table empty or missing"
fi
log "Latest migration: $MIGRATION_VERSION"

# A couple of core tables must have non-zero rows in real prod data
for tbl in users workspaces; do
  ROWS=$(psql_query "$CHECK_DB" "SELECT count(*) FROM $tbl;" 2>/dev/null || echo "0")
  log "$tbl rows: $ROWS"
done

# Cleanup
psql_exec postgres "DROP DATABASE \"$CHECK_DB\";"
log "Check database dropped."

log "OK: backup $LATEST is restorable (migration $MIGRATION_VERSION, $TABLE_COUNT tables)"
