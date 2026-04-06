#!/usr/bin/env bash
# backup-db.sh — PostgreSQL backup script for Sellico
# Usage: ./scripts/backup-db.sh [backup_dir]
#
# Environment variables:
#   PGHOST     (default: localhost)
#   PGPORT     (default: 5432)
#   PGUSER     (default: sellico)
#   PGPASSWORD (required or use .pgpass)
#   PGDATABASE (default: sellico)
#
# Keeps last 7 daily backups by default (BACKUP_RETAIN_DAYS).

set -euo pipefail

BACKUP_DIR="${1:-/opt/sellico/backups}"
RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-7}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
DB_NAME="${PGDATABASE:-sellico}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="${BACKUP_DIR}/${DB_NAME}_${TIMESTAMP}.sql.gz"

echo "[$(date -Iseconds)] Starting backup of ${DB_NAME} -> ${BACKUP_FILE}"

pg_dump \
  --host="${PGHOST:-localhost}" \
  --port="${PGPORT:-5432}" \
  --username="${PGUSER:-sellico}" \
  --dbname="$DB_NAME" \
  --format=custom \
  --compress=6 \
  --no-owner \
  --no-privileges \
  --file="$BACKUP_FILE"

SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
echo "[$(date -Iseconds)] Backup complete: ${BACKUP_FILE} (${SIZE})"

# Cleanup old backups
DELETED=0
find "$BACKUP_DIR" -name "${DB_NAME}_*.sql.gz" -mtime +"$RETAIN_DAYS" -type f | while read -r old; do
  rm -f "$old"
  DELETED=$((DELETED + 1))
  echo "[$(date -Iseconds)] Removed old backup: $(basename "$old")"
done

echo "[$(date -Iseconds)] Backup retention: keeping last ${RETAIN_DAYS} days"
