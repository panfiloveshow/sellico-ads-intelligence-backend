# WB Ads Feasibility Traceability

Date: 2026-05-28

Scope: trace `wb_ads_tool_feasibility.md` to current implementation evidence. This is a verification aid, not a product spec replacement.

## Core Product Direction

| Feasibility intent | Current evidence | Verification status |
| --- | --- | --- |
| API-first WB ads/product decision center, not only an autobidder | `internal/service/ads_read.go`, `internal/service/recommendation_engine.go`, `internal/service/bid_automation.go`, `docs/WB_ADS_TOOL_COMPLETION_AUDIT.md` | Implemented and covered by backend tests |
| Product/nmID is the main business unit, campaign is not the only center | `internal/service/ads_read_builders.go`, `internal/service/product.go`, `internal/service/product_economics.go` | Implemented |
| Official WB API is the primary source for campaigns, bids, stats, products, prices, stocks, reports, tariffs, reviews/questions | `internal/integration/wb`, `internal/service/sync.go`, `internal/service/seller_cabinet_communication.go` | Implemented where real source data exists |
| Missing real data must stay truthful | `scripts/check-real-data-only.sh`, `internal/service/ads_read_builders.go`, `internal/service/extension_evidence.go` | Automated check passed; authenticated QA still required for final UI proof |
| Customer-facing automation must be visible in the command center | `CommandCenter.tsx`, `CommandCenter.test.tsx`, `scripts/check-wb-ads-feasibility.sh` | Frontend panel added for real active recommendations |
| Support-report wiring must stay attached across backend/OpenAPI/frontend | `scripts/check-wb-ads-feasibility.sh`, `internal/transport/router.go`, `openapi/openapi.yaml`, `SupportEvidencePage.tsx` | Automated gate added |

## Feature Traceability

| Feasibility module | Current evidence | Notes |
| --- | --- | --- |
| Campaign list/status/stats/actions | `internal/service/campaign.go`, `internal/service/campaign_actions.go`, `internal/transport/handler/campaign.go` | API-backed actions with audit metadata |
| Bids and minimum/recommended bid guardrails | `internal/integration/wb/bids_api.go`, `internal/service/bidengine.go`, `internal/service/bid_automation.go` | Rate/cooldown/strategy guardrails are backend-side |
| Search clusters, query stats, minus phrases | `internal/service/phrase.go`, `internal/transport/handler/phrase.go`, `internal/service/ads_read_classify.go` | Campaign type restrictions are treated as guardrails |
| Product card, price, stock, sales funnel and business diagnostics | `internal/service/product.go`, `internal/service/stock_evidence.go`, `internal/service/ads_read_builders.go` | Empty/partial/stale states must be preserved |
| Manual/imported unit economics | `internal/service/product_economics.go`, `internal/integration/sellico/unit_economics.go`, migrations `000026_*` | Real user/imported economics, no invented margin |
| WB tariffs and commissions | `internal/integration/wb/tariffs_api.go`, migrations `000027_*` | Used as real tariff evidence where available |
| Reviews/questions/reputation evidence | `internal/integration/wb/user_communication.go`, `internal/service/seller_cabinet_communication.go` | Read-only WB User Communication API evidence |
| Recommendations, task ownership, owner/agency reports | `internal/service/recommendation*.go`, `internal/service/export.go`, `internal/service/notification.go` | Decision queue is internal logic over real inputs |
| Visible recommendation execution | `/Users/panfiloveshow/Documents/front2/frontend-from-server/src/modules/ads-intelligence/pages/CommandCenter.tsx` | `Пульт решений` shows backend recommendations and apply/pause/open/dismiss controls where API actions exist |
| Autopilot levels and safety rules | `internal/domain/strategy.go`, `internal/service/bid_automation.go`, `internal/service/wb_rate_limit_guard.go` | Guardrails block unsafe action when evidence is missing |
| Action history and rollback readiness | `internal/service/campaign_actions.go`, `internal/repository/sqlc/wb_ads_controls_manual.go` | Change metadata is stored backend-side |

## Extension Boundary

| Feasibility constraint | Current evidence | Status |
| --- | --- | --- |
| Extension is auxiliary, not scraper-first source of truth | `docs/WB-API-COVERAGE-AND-EXTENSION.md`, `internal/service/extension_evidence.go`, `extension/chromium/README.md` | Aligned |
| Extension evidence report uses saved real captures only | `internal/service/extension_evidence_debug.go`, `internal/transport/handler/extension.go` | Implemented |
| Sellico frontend has an internal support diagnostics route | `/Users/panfiloveshow/Documents/front2/frontend-from-server/src/modules/ads-intelligence/pages/SupportEvidencePage.tsx` | Implemented at `/ads-intelligence/support`; hidden from normal customer nav |
| Support screen does not invent demo metrics | `SupportEvidencePage.test.tsx`, `scripts/check-real-data-only.sh`, manual QA checklist | Automated frontend test covers the new screen; final authenticated visual QA remains |
| Customer nav must stay focused on automation | `AdsIntelligenceLayout.tsx`, `AdsIntelligenceLayout.test.tsx` | Diagnostics route is direct/internal, not promoted as a user task |

## Current Verification Evidence

Automated checks confirmed on 2026-05-28:

```bash
sh scripts/check-real-data-only.sh
sh scripts/check-wb-ads-feasibility.sh
git diff --check
node --check extension/chromium/options.js
node --check extension/chromium/background.js
node --check extension/chromium/content.js
env GOCACHE=/Users/panfiloveshow/Documents/ПРОЕКТЫ/marketing/.gocache go run ./tools/check-openapi-drift
env GOCACHE=/Users/panfiloveshow/Documents/ПРОЕКТЫ/marketing/.gocache go test ./internal/transport/handler -run TestExtensionEvidenceSupportReport_CampaignScope
cd /Users/panfiloveshow/Documents/front2/frontend-from-server && git diff --check
cd /Users/panfiloveshow/Documents/front2/frontend-from-server && npm run typecheck
cd /Users/panfiloveshow/Documents/front2/frontend-from-server && npm test -- CommandCenter.test.tsx AdsIntelligenceLayout.test.tsx AppRoutes.adsSupport.test.tsx SupportEvidencePage.test.tsx
cd /Users/panfiloveshow/Documents/front2/frontend-from-server && npm run build
```

Known build warning: Vite leaves existing Leaflet image URLs unresolved at build time (`images/layers.png`, `images/layers-2x.png`, `images/marker-icon.png`). This is not introduced by the WB ads support screen.

## Remaining Proof

Manual authenticated QA has started: `docs/WB_ADS_AUTHENTICATED_QA_RUN_2026-05-29.md` records a real authenticated Ads Intelligence command-center screenshot with truthful partial-data rendering. After the screenshot, the visible command center was upgraded with `Пульт решений`; final browser QA should verify that panel against a real workspace with active recommendations. The remaining internal proof is the support diagnostics route itself: open `/ads-intelligence/support` by direct URL when investigating a data issue, load a real scope, verify `GET /api/v1/extension/evidence-debug/report`, and complete `docs/WB_ADS_AUTHENTICATED_QA_CHECKLIST.md`.
