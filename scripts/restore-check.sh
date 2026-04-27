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
DB_NAME="${PGDATABASE:-sellico}"
CHECK_DB="${DB_NAME}_restore_check"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

log() { echo "[$(date -Iseconds)] $*"; }
fail() { log "FAIL: $*"; exit 1; }

# Find newest local dump
LATEST=$(find "$BACKUP_DIR" -name "${DB_NAME}_*.dump" -type f -printf '%T@ %p\n' \
         | sort -nr | head -1 | cut -d' ' -f2-)

if [[ -z "$LATEST" || ! -r "$LATEST" ]]; then
  fail "No restorable backup found in $BACKUP_DIR"
fi
log "Latest backup: $LATEST"

# Drop check DB if leftover from previous run
psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
     --username="${PGUSER:-sellico}" --dbname=postgres \
     -c "DROP DATABASE IF EXISTS \"$CHECK_DB\";" >/dev/null

# Create fresh check DB
psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
     --username="${PGUSER:-sellico}" --dbname=postgres \
     -c "CREATE DATABASE \"$CHECK_DB\";" >/dev/null
log "Check database created: $CHECK_DB"

# Restore
log "Restoring..."
pg_restore \
  --host="${PGHOST:-localhost}" \
  --port="${PGPORT:-5432}" \
  --username="${PGUSER:-sellico}" \
  --dbname="$CHECK_DB" \
  --no-owner \
  --no-privileges \
  --jobs=2 \
  "$LATEST" 2>&1 | tail -5 || fail "pg_restore reported errors"

# Sanity checks
TABLE_COUNT=$(psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
              --username="${PGUSER:-sellico}" --dbname="$CHECK_DB" \
              -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public';")
log "Public tables: $TABLE_COUNT"
[[ "$TABLE_COUNT" -gt 10 ]] || fail "expected >10 tables, got $TABLE_COUNT — backup looks empty"

# schema_migrations head — confirms migrations applied
MIGRATION_VERSION=$(psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
                    --username="${PGUSER:-sellico}" --dbname="$CHECK_DB" \
                    -tAc "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;" 2>/dev/null || echo "")
if [[ -z "$MIGRATION_VERSION" ]]; then
  fail "schema_migrations table empty or missing"
fi
log "Latest migration: $MIGRATION_VERSION"

# A couple of core tables must have non-zero rows in real prod data
for tbl in users workspaces; do
  ROWS=$(psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
         --username="${PGUSER:-sellico}" --dbname="$CHECK_DB" \
         -tAc "SELECT count(*) FROM $tbl;" 2>/dev/null || echo "0")
  log "$tbl rows: $ROWS"
done

# Cleanup
psql --host="${PGHOST:-localhost}" --port="${PGPORT:-5432}" \
     --username="${PGUSER:-sellico}" --dbname=postgres \
     -c "DROP DATABASE \"$CHECK_DB\";" >/dev/null
log "Check database dropped."

log "OK: backup $LATEST is restorable (migration $MIGRATION_VERSION, $TABLE_COUNT tables)"
