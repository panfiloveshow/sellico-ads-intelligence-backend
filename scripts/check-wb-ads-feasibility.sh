#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="${WB_ADS_FRONTEND_DIR:-/Users/panfiloveshow/Documents/front2/frontend-from-server}"

fail() {
  echo "WB ads feasibility check failed: $*" >&2
  exit 1
}

require_file() {
  [[ -f "$1" ]] || fail "missing required file: $1"
}

require_dir() {
  [[ -d "$1" ]] || fail "missing required directory: $1"
}

require_pattern() {
  local pattern="$1"
  local file="$2"
  local label="$3"
  rg -q "$pattern" "$file" || fail "missing $label in $file"
}

reject_pattern() {
  local pattern="$1"
  local path="$2"
  local label="$3"
  local matches
  matches="$(rg -n "$pattern" "$path" || true)"
  if [[ -n "$matches" ]]; then
    echo "$matches" >&2
    fail "$label"
  fi
}

command -v rg >/dev/null 2>&1 || fail "ripgrep is required"

cd "$ROOT"

require_file "$ROOT/wb_ads_tool_feasibility.md"
require_file "$ROOT/scripts/check-real-data-only.sh"
require_file "$ROOT/docs/WB_ADS_TOOL_COMPLETION_AUDIT.md"
require_file "$ROOT/docs/WB_ADS_FEASIBILITY_TRACEABILITY.md"
require_file "$ROOT/docs/WB_ADS_AUTHENTICATED_QA_CHECKLIST.md"
require_dir "$FRONTEND_DIR"

sh "$ROOT/scripts/check-real-data-only.sh" >/dev/null

require_pattern "GetEvidenceSupportReport" "$ROOT/internal/service/extension_evidence_debug.go" "support report service"
require_pattern "EvidenceSupportReport" "$ROOT/internal/transport/handler/extension.go" "support report handler"
require_pattern "/evidence-debug/report" "$ROOT/internal/transport/router.go" "support report route"
require_pattern "/api/v1/extension/evidence-debug/report" "$ROOT/openapi/openapi.yaml" "support report OpenAPI path"
require_pattern "ExtensionEvidenceSupportReportEnvelope" "$ROOT/openapi/openapi.yaml" "support report OpenAPI schema"

support_page="$FRONTEND_DIR/src/modules/ads-intelligence/pages/SupportEvidencePage.tsx"
support_test="$FRONTEND_DIR/src/modules/ads-intelligence/pages/SupportEvidencePage.test.tsx"
routes_file="$FRONTEND_DIR/src/app/routes/AppRoutes.tsx"
layout_file="$FRONTEND_DIR/src/modules/ads-intelligence/AdsIntelligenceLayout.tsx"
layout_test="$FRONTEND_DIR/src/modules/ads-intelligence/AdsIntelligenceLayout.test.tsx"
api_file="$FRONTEND_DIR/src/modules/ads-intelligence/api/adsIntelligenceApi.ts"
command_center_page="$FRONTEND_DIR/src/modules/ads-intelligence/pages/CommandCenter.tsx"
command_center_test="$FRONTEND_DIR/src/modules/ads-intelligence/pages/CommandCenter.test.tsx"

require_file "$support_page"
require_file "$support_test"
require_file "$routes_file"
require_file "$layout_file"
require_file "$layout_test"
require_file "$api_file"
require_file "$command_center_page"
require_file "$command_center_test"

require_pattern "Диагностика данных" "$support_page" "support diagnostics page title"
require_pattern "getExtensionEvidenceSupportReport" "$support_page" "support page backend call"
require_pattern "Локальные примеры.*не используются|подмененные метрики.*не используются" "$support_page" "truthful no-local-example state"
require_pattern "Пульт решений" "$command_center_page" "visible command-center automation panel"
require_pattern "Данные частичные: агрессивный автопилот ограничен" "$command_center_page" "partial-data automation guard"
require_pattern "Система уже нашла действия по реальным данным" "$command_center_test" "command-center automation panel regression"
require_pattern "path=\"support\"" "$routes_file" "frontend support route"
require_pattern "AdsSupportEvidencePage" "$routes_file" "frontend support lazy route"
require_pattern "does not show internal diagnostics" "$layout_test" "customer nav hides support diagnostics"
require_pattern "preserves workspace query" "$layout_test" "workspace-preserving Ads Intelligence navigation regression"
require_pattern "/extension/evidence-debug/report" "$api_file" "frontend support API endpoint"

reject_pattern "demo|mock|fake|synthetic" "$support_page" "support page must not contain demo/mock/fake/synthetic runtime wording"
reject_pattern "demo|mock|fake|synthetic" "$command_center_page" "command center must not contain demo/mock/fake/synthetic runtime wording"
reject_pattern "chrome\\.cookies|document\\.cookie" "$ROOT/extension/chromium" "extension runtime must not read browser cookies"
reject_pattern "internal.*wildberries|wb.*internal|x-supplier-id" "$ROOT/extension/chromium" "extension runtime must not target unofficial WB internal integration primitives"

echo "wb-ads-feasibility check passed"
