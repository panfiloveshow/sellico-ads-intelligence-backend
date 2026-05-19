#!/usr/bin/env bash
# backup-db.sh — PostgreSQL backup with optional offsite (S3) replication.
#
# Local-only mode (default):
#   ./scripts/backup-db.sh
#
# With offsite replication (Yandex Object Storage / any S3-compatible):
#   S3_ENDPOINT=https://storage.yandexcloud.net \
#   S3_BUCKET=sellico-backups \
#   S3_PREFIX=postgres/ \
#   AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... \
#   BACKUP_GPG_PASSPHRASE_FILE=/etc/sellico/backup-gpg.pass \
#   ./scripts/backup-db.sh
#
# Environment variables (PostgreSQL connection):
#   PGHOST     (default: localhost)
#   PGPORT     (default: 5432)
#   PGUSER     (default: sellico)
#   PGPASSWORD (required or use .pgpass)
#   PGDATABASE (default: sellico)
#
# Retention:
#   BACKUP_RETAIN_DAYS=7    Local cleanup window
#   S3_RETAIN_DAYS=30       Set as bucket lifecycle rule (not enforced here)
# Metrics:
#   BACKUP_TEXTFILE_DIR=/var/lib/node_exporter/textfile
#     Writes Prometheus textfile metrics for BackupAbsent alerting.

set -euo pipefail

BACKUP_DIR="${1:-/opt/sellico/backups}"
RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-7}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
DB_NAME="${POSTGRES_DB:-${PGDATABASE:-sellico}}"
DB_USER="${POSTGRES_USER:-${PGUSER:-sellico}}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="${BACKUP_DIR}/${DB_NAME}_${TIMESTAMP}.dump"
OFFSITE_CONFIGURED=0
OFFSITE_SUCCESS=0

log() { echo "[$(date -Iseconds)] $*"; }

write_backup_metrics() {
  local textfile_dir="${BACKUP_TEXTFILE_DIR:-/var/lib/node_exporter/textfile}"
  if [[ -z "$textfile_dir" ]] || [[ "$textfile_dir" == "off" ]]; then
    log "Backup metrics disabled (BACKUP_TEXTFILE_DIR=${textfile_dir:-<empty>})"
    return 0
  fi

  mkdir -p "$textfile_dir"

  local metric_file="${textfile_dir}/sellico_backup.prom"
  local tmp_file="${metric_file}.$$"
  local now size_bytes

  now="$(date +%s)"
  size_bytes="$(stat -c%s "$BACKUP_FILE" 2>/dev/null || stat -f%z "$BACKUP_FILE")"

  cat > "$tmp_file" <<EOF
# HELP sellico_backup_last_success_timestamp_seconds Unix timestamp of the last successful PostgreSQL backup.
# TYPE sellico_backup_last_success_timestamp_seconds gauge
sellico_backup_last_success_timestamp_seconds ${now}
# HELP sellico_backup_last_size_bytes Size of the last successful PostgreSQL backup dump.
# TYPE sellico_backup_last_size_bytes gauge
sellico_backup_last_size_bytes ${size_bytes}
# HELP sellico_backup_offsite_success Whether the latest backup was uploaded offsite successfully.
# TYPE sellico_backup_offsite_success gauge
sellico_backup_offsite_success ${OFFSITE_SUCCESS}
# HELP sellico_backup_offsite_configured Whether offsite backup upload was configured for the latest run.
# TYPE sellico_backup_offsite_configured gauge
sellico_backup_offsite_configured ${OFFSITE_CONFIGURED}
EOF
  mv "$tmp_file" "$metric_file"
  log "Backup metrics written: ${metric_file}"
}

log "Starting backup of ${DB_NAME} -> ${BACKUP_FILE}"

if [[ "${BACKUP_USE_DOCKER:-0}" == "1" ]]; then
  log "Using docker compose postgres service for pg_dump"
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    pg_dump \
      --username="$DB_USER" \
      --dbname="$DB_NAME" \
      --format=custom \
      --compress=6 \
      --no-owner \
      --no-privileges \
    > "$BACKUP_FILE"
else
  pg_dump \
    --host="${PGHOST:-localhost}" \
    --port="${PGPORT:-5432}" \
    --username="$DB_USER" \
    --dbname="$DB_NAME" \
    --format=custom \
    --compress=6 \
    --no-owner \
    --no-privileges \
    --file="$BACKUP_FILE"
fi

SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
log "Backup complete: ${BACKUP_FILE} (${SIZE})"

# --- Optional: encrypt + upload to S3 (offsite DR) ----------------------------
upload_offsite() {
  if [[ -z "${S3_BUCKET:-}" ]]; then
    log "S3_BUCKET not set — skipping offsite upload (local backup only)"
    return 0
  fi
  OFFSITE_CONFIGURED=1

  if [[ -z "${BACKUP_GPG_PASSPHRASE_FILE:-}" ]] || [[ ! -r "$BACKUP_GPG_PASSPHRASE_FILE" ]]; then
    log "ERROR: BACKUP_GPG_PASSPHRASE_FILE missing or unreadable — refusing to upload unencrypted backup"
    return 1
  fi

  if ! command -v gpg &>/dev/null; then
    log "ERROR: gpg not installed (apt install gnupg)"
    return 1
  fi
  if ! command -v aws &>/dev/null; then
    log "ERROR: aws cli not installed (pip install awscli or apt install awscli)"
    return 1
  fi

  local enc_file="${BACKUP_FILE}.gpg"
  log "Encrypting backup with GPG (symmetric, AES256)..."
  gpg --batch --yes \
      --cipher-algo AES256 \
      --passphrase-file "$BACKUP_GPG_PASSPHRASE_FILE" \
      --symmetric \
      --output "$enc_file" \
      "$BACKUP_FILE"

  local prefix="${S3_PREFIX:-postgres/}"
  local s3_uri="s3://${S3_BUCKET}/${prefix}${DB_NAME}_${TIMESTAMP}.dump.gpg"
  local endpoint_arg=""
  if [[ -n "${S3_ENDPOINT:-}" ]]; then
    endpoint_arg="--endpoint-url=${S3_ENDPOINT}"
  fi

  log "Uploading to ${s3_uri}..."
  aws ${endpoint_arg} s3 cp \
      --only-show-errors \
      --storage-class STANDARD_IA \
      "$enc_file" "$s3_uri"

  rm -f "$enc_file"
  OFFSITE_SUCCESS=1
  log "Offsite upload complete."
  log "NOTE: configure bucket lifecycle to delete objects older than ${S3_RETAIN_DAYS:-30} days."
}

if ! upload_offsite; then
  log "WARN: offsite upload failed — local backup retained but DR is degraded"
  # Don't fail the script if local backup succeeded; offsite is best-effort.
fi

# --- Local cleanup ------------------------------------------------------------
DELETED=0
while IFS= read -r -d '' old; do
  rm -f "$old"
  DELETED=$((DELETED + 1))
  log "Removed old local backup: $(basename "$old")"
done < <(find "$BACKUP_DIR" -name "${DB_NAME}_*.dump" -mtime +"$RETAIN_DAYS" -type f -print0)

log "Backup retention: keeping last ${RETAIN_DAYS} days locally (deleted ${DELETED})"
write_backup_metrics
