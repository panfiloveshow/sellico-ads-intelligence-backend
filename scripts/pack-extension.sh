#!/usr/bin/env bash
# pack-extension.sh — Build a signed CRX for the Sellico browser extension.
#
# Works on macOS (uses Yandex/Chrome) and Linux (CI; uses headless Chromium).
# In CI the private key is read from EXTENSION_PRIVATE_KEY env (base64-encoded
# PEM); locally it's read from extension/sellico-extension-key.pem.
#
# Output: extension/dist/sellico-ads-intelligence.crx
# Side outputs: extension/dist/sellico-ads-intelligence-v{version}.zip
# (the zip is what Chrome Web Store accepts for upload — CRX is for self-host).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXT_DIR="${ROOT_DIR}/extension/chromium"
DIST_DIR="${ROOT_DIR}/extension/dist"
KEY_PATH="${EXTENSION_KEY_PATH:-${ROOT_DIR}/extension/sellico-extension-key.pem}"
CRX_PATH="${DIST_DIR}/sellico-ads-intelligence.crx"

if [[ ! -d "${EXT_DIR}" ]]; then
  echo "Extension dir not found: ${EXT_DIR}" >&2
  exit 1
fi

mkdir -p "${DIST_DIR}"

# CI: materialise the private key from a base64-encoded secret.
if [[ -n "${EXTENSION_PRIVATE_KEY:-}" && ! -f "${KEY_PATH}" ]]; then
  printf '%s' "$EXTENSION_PRIVATE_KEY" | base64 -d > "${KEY_PATH}"
  chmod 0600 "${KEY_PATH}"
  trap 'rm -f "${KEY_PATH}"' EXIT
fi

if [[ ! -f "${KEY_PATH}" ]]; then
  echo "WARN: no private key at ${KEY_PATH} - browser will generate a fresh one" >&2
  echo "      and the extension ID will change. For Chrome Web Store releases" >&2
  echo "      the same key MUST be reused across versions." >&2
fi

# Browser binary discovery - order matters (CI Linux first, then macOS).
BIN=""
for candidate in \
    "/usr/bin/chromium-browser" \
    "/usr/bin/chromium" \
    "/usr/bin/google-chrome" \
    "/Applications/Yandex.app/Contents/MacOS/Yandex" \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "/Applications/Chromium.app/Contents/MacOS/Chromium"; do
  if [[ -x "$candidate" ]]; then
    BIN="$candidate"
    break
  fi
done
if [[ -z "$BIN" ]]; then
  echo "No Chrome/Chromium/Yandex binary found." >&2
  echo "Install one or set BIN=/path/to/chrome and re-run." >&2
  exit 1
fi
echo "Using browser: ${BIN}"

# Ensure icons exist before packaging (manifest references them).
if [[ ! -f "${EXT_DIR}/icons/icon128.png" ]]; then
  echo "Icons missing - running icons/generate-icons.sh" >&2
  bash "${EXT_DIR}/icons/generate-icons.sh" || {
    echo "Icon generation failed (need imagemagick: apt install imagemagick / brew install imagemagick)" >&2
    exit 1
  }
fi

ARGS=( "--pack-extension=${EXT_DIR}" "--no-message-box" )
[[ -f "${KEY_PATH}" ]] && ARGS+=( "--pack-extension-key=${KEY_PATH}" )

# Headless flags for CI; harmless on macOS.
ARGS+=( "--headless" "--disable-gpu" "--no-sandbox" )

"$BIN" "${ARGS[@]}" >/dev/null 2>&1 || true

# Capture generated artefacts.
GENERATED_CRX="${EXT_DIR}.crx"
GENERATED_KEY="${EXT_DIR}.pem"

if [[ -f "$GENERATED_KEY" && ! -f "${KEY_PATH}" ]]; then
  mv "$GENERATED_KEY" "${KEY_PATH}"
fi

if [[ ! -f "$GENERATED_CRX" ]]; then
  echo "Packaging failed: ${GENERATED_CRX} not created." >&2
  exit 1
fi

mv "$GENERATED_CRX" "${CRX_PATH}"

# Also emit a versioned zip - Chrome Web Store accepts only zip uploads
# (CRX is for self-hosted distribution + signature verification).
VERSION=$(grep -m1 '"version"' "${EXT_DIR}/manifest.json" | sed -E 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
ZIP_PATH="${DIST_DIR}/sellico-ads-intelligence-v${VERSION}.zip"
( cd "${EXT_DIR}" && zip -qr "${ZIP_PATH}" . -x '*.pem' -x 'icons/README.md' -x 'icons/generate-icons.sh' )

echo "OK: ${CRX_PATH}"
echo "OK: ${ZIP_PATH}  <- upload this to Chrome Web Store"
