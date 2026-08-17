#!/usr/bin/env bash
# Runtime code must not contain mock/demo/fake/synthetic product data paths:
# the product shows real marketplace data or an honest empty/error state.
#
# The check looks at CODE, not at prose. Matching raw text made it fail on
# comments that merely describe test doubles — «tests supply a fake so the
# write path can be exercised» is documentation, not a fake data path. CI had
# been red on those five comments since 10 August, which is worse than having
# no check at all: a real violation would have gone unnoticed in the noise.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pattern='mock|demo|fake|synthetic'
paths=(cmd internal extension)

if ! command -v rg >/dev/null 2>&1; then
  echo "ripgrep is required for real-data-only checks" >&2
  exit 2
fi

# rg finds candidate lines; awk then re-tests each one with its line comment
# removed, so a match that lived only in a comment drops out.
matches="$(
  rg -n --no-heading "$pattern" "${paths[@]}" \
    -g '!**/*_test.go' \
    -g '!**/testdata/**' \
    -g '!**/*.md' \
    -g '!extension/chromium/icons/**' 2>/dev/null |
  awk -v pat="$pattern" '
    {
      # Строка вида path:lineno:content — отделяем содержимое.
      p = index($0, ":")
      rest = substr($0, p + 1)
      q = index(rest, ":")
      content = substr(rest, q + 1)

      # Отрезаем строчный комментарий. "://" пропускаем, иначе обрежется URL
      # и настоящее совпадение внутри адреса потеряется.
      pos = 1
      while ((c = index(substr(content, pos), "//")) > 0) {
        abs = pos + c - 1
        if (abs > 1 && substr(content, abs - 1, 1) == ":") {
          pos = abs + 2          # часть схемы, ищем дальше
          continue
        }
        content = substr(content, 1, abs - 1)
        break
      }

      if (content ~ pat) print $0
    }
  ' || true
)"

if [[ -n "$matches" ]]; then
  cat >&2 <<'MSG'
Real-data-only check failed.

Runtime code must not contain mock/demo/fake/synthetic product data paths.
Use real Sellico/marketplace/database/user data, or return an honest empty/error/sync-needed state.

Matches (comments already excluded — these are in code):
MSG
  echo "$matches" >&2
  exit 1
fi

echo "real-data-only check passed"
