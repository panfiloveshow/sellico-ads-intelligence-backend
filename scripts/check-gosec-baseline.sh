#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

baseline=".gosec-baseline.tsv"
report="$(mktemp)"
current="$(mktemp)"
new_findings="$(mktemp)"
trap 'rm -f "$report" "$current" "$new_findings"' EXIT

if [[ ! -f "$baseline" ]]; then
  echo "missing gosec baseline: $baseline" >&2
  exit 2
fi

# -no-fail is used only so the report can be compared with the checked-in
# baseline. This script exits non-zero for every finding not in that baseline.
go run github.com/securego/gosec/v2/cmd/gosec@v2.25.0 \
  -no-fail -fmt=json -out="$report" -exclude-generated -exclude-dir=.claude ./...

jq -r --arg root "$ROOT/" '
  .Issues[]
  | [
      .rule_id,
      (.file | ltrimstr($root)),
      .details,
      (.code
        | gsub("(?m)^[[:space:]]*[0-9]+: "; "")
        | gsub("[[:space:]]+"; " ")
        | ltrimstr(" ")
        | rtrimstr(" "))
    ]
  | @tsv
' "$report" | LC_ALL=C sort -u > "$current"

comm -13 "$baseline" "$current" > "$new_findings"
if [[ -s "$new_findings" ]]; then
  echo "gosec found security findings outside the approved baseline:" >&2
  cat "$new_findings" >&2
  exit 1
fi

baseline_count="$(wc -l < "$baseline" | tr -d ' ')"
current_count="$(wc -l < "$current" | tr -d ' ')"
echo "gosec baseline check passed (current=$current_count, baseline=$baseline_count)"
