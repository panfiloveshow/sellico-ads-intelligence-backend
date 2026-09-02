#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

mkdir -p "$TEST_DIR/bin" "$TEST_DIR/backups"

cat > "$TEST_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *" pg_dump "* ]]; then
  if [[ "${FAKE_PG_DUMP_FAIL:-0}" == "1" ]]; then
    printf 'partial'
    exit 42
  fi
  printf 'valid-custom-archive'
  exit 0
fi
if [[ " $* " == *" pg_restore --list "* ]]; then
  input="$(cat)"
  [[ "$input" == "valid-custom-archive" ]]
  exit
fi
exit 64
EOF
chmod +x "$TEST_DIR/bin/docker"

cat > "$TEST_DIR/bin/gpg" <<'EOF'
#!/usr/bin/env bash
exit 9
EOF
chmod +x "$TEST_DIR/bin/gpg"

run_backup() {
  PATH="$TEST_DIR/bin:$PATH" \
  BACKUP_USE_DOCKER=1 \
  BACKUP_TEXTFILE_DIR=off \
  POSTGRES_DB=sellico_test \
  POSTGRES_USER=sellico_test \
  "$ROOT_DIR/scripts/backup-db.sh" "$TEST_DIR/backups"
}

run_backup > "$TEST_DIR/success.log"
[[ "$(find "$TEST_DIR/backups" -type f -name '*.dump' | wc -l | tr -d ' ')" == "1" ]]
[[ "$(find "$TEST_DIR/backups" -type f -name '*partial*' | wc -l | tr -d ' ')" == "0" ]]
if stat -c '%a' "$TEST_DIR/backups"/*.dump > /dev/null 2>&1; then
  dump_mode="$(stat -c '%a' "$TEST_DIR/backups"/*.dump)"
else
  dump_mode="$(stat -f '%Lp' "$TEST_DIR/backups"/*.dump)"
fi
[[ "$dump_mode" == "600" ]]

rm -f "$TEST_DIR/backups"/*.dump
if FAKE_PG_DUMP_FAIL=1 run_backup > "$TEST_DIR/failure.log" 2>&1; then
  echo "expected failed pg_dump to fail the backup command" >&2
  exit 1
fi
[[ "$(find "$TEST_DIR/backups" -type f | wc -l | tr -d ' ')" == "0" ]]

# Offsite is best-effort, but a failed encryption must remain truthful in the
# exported metrics and must not remove the valid local archive.
mkdir -p "$TEST_DIR/metrics"
printf 'test-passphrase' > "$TEST_DIR/pass"
PATH="$TEST_DIR/bin:$PATH" \
BACKUP_USE_DOCKER=1 \
BACKUP_TEXTFILE_DIR="$TEST_DIR/metrics" \
POSTGRES_DB=sellico_test \
POSTGRES_USER=sellico_test \
S3_BUCKET=test-bucket \
BACKUP_GPG_PASSPHRASE_FILE="$TEST_DIR/pass" \
"$ROOT_DIR/scripts/backup-db.sh" "$TEST_DIR/backups" > "$TEST_DIR/offsite-failure.log"
grep -q '^sellico_backup_offsite_configured 1$' "$TEST_DIR/metrics/sellico_backup.prom"
grep -q '^sellico_backup_offsite_success 0$' "$TEST_DIR/metrics/sellico_backup.prom"
if stat -c '%a' "$TEST_DIR/metrics/sellico_backup.prom" > /dev/null 2>&1; then
  metric_mode="$(stat -c '%a' "$TEST_DIR/metrics/sellico_backup.prom")"
else
  metric_mode="$(stat -f '%Lp' "$TEST_DIR/metrics/sellico_backup.prom")"
fi
[[ "$metric_mode" == "644" ]]
[[ "$(find "$TEST_DIR/backups" -type f -name '*.dump' | wc -l | tr -d ' ')" == "1" ]]
[[ "$(find "$TEST_DIR/backups" -type f -name '*.gpg' | wc -l | tr -d ' ')" == "0" ]]

echo "backup-db tests passed"
