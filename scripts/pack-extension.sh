#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXT_DIR="${ROOT_DIR}/extension/chromium"
DIST_DIR="${ROOT_DIR}/extension/dist"
KEY_PATH="${ROOT_DIR}/extension/sellico-extension-key.pem"
CRX_PATH="${DIST_DIR}/sellico-ads-intelligence.crx"

YANDEX_BIN="/Applications/Yandex.app/Contents/MacOS/Yandex"
CHROME_BIN="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

if [[ ! -d "${EXT_DIR}" ]]; then
  echo "Extension dir not found: ${EXT_DIR}" >&2
  exit 1
fi

mkdir -p "${DIST_DIR}"

BIN=""
if [[ -x "${YANDEX_BIN}" ]]; then
  BIN="${YANDEX_BIN}"
elif [[ -x "${CHROME_BIN}" ]]; then
  BIN="${CHROME_BIN}"
else
  echo "Neither Yandex nor Chrome binary found in /Applications." >&2
  echo "Expected one of:" >&2
  echo "  ${YANDEX_BIN}" >&2
  echo "  ${CHROME_BIN}" >&2
  exit 1
fi

echo "Packaging extension from: ${EXT_DIR}"
echo "Using browser binary: ${BIN}"
echo "Key file: ${KEY_PATH}"

ARGS=( "--pack-extension=${EXT_DIR}" )
if [[ -f "${KEY_PATH}" ]]; then
  ARGS+=( "--pack-extension-key=${KEY_PATH}" )
else
  echo "Key not found; browser will generate a new key alongside the extension."
fi

"${BIN}" "${ARGS[@]}" >/dev/null 2>&1 || true

if [[ ! -f "${KEY_PATH}" ]]; then
  GENERATED_KEY="${EXT_DIR}.pem"
  if [[ -f "${GENERATED_KEY}" ]]; then
    mv "${GENERATED_KEY}" "${KEY_PATH}"
  fi
fi

GENERATED_CRX="${EXT_DIR}.crx"
if [[ ! -f "${GENERATED_CRX}" ]]; then
  echo "Packaging failed: ${GENERATED_CRX} not created." >&2
  exit 1
fi

mv "${GENERATED_CRX}" "${CRX_PATH}"
echo "OK: ${CRX_PATH}"

