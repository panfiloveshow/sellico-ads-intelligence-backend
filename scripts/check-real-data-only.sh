#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pattern='mock|demo|fake|synthetic'
paths=(cmd internal extension)

if ! command -v rg >/dev/null 2>&1; then
  echo "ripgrep is required for real-data-only checks" >&2
  exit 2
fi

matches="$(
  rg -n "$pattern" "${paths[@]}" \
    -g '!**/*_test.go' \
    -g '!**/testdata/**' \
    -g '!**/*.md' \
    -g '!extension/chromium/icons/**' || true
)"

if [[ -n "$matches" ]]; then
  cat >&2 <<'MSG'
Real-data-only check failed.

Runtime code must not contain mock/demo/fake/synthetic product data paths.
Use real Sellico/marketplace/database/user data, or return an honest empty/error/sync-needed state.

Matches:
MSG
  echo "$matches" >&2
  exit 1
fi

echo "real-data-only check passed"
