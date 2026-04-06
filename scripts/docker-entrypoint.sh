#!/bin/sh
set -eu

export_path="${EXPORT_STORAGE_PATH:-/app/exports}"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$export_path"
  chown -R app:app "$export_path"
  exec su-exec app /app/server "$@"
fi

mkdir -p "$export_path"
exec /app/server "$@"
