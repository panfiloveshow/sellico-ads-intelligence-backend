# WB Ads Authenticated QA Run

Date: 2026-05-29

Evidence source: user-provided authenticated Sellico screenshot in the Codex thread.

## Observed As Passed

- Sellico is opened in an authenticated workspace session.
- Workspace selector shows `PlaceSales`.
- Left navigation shows `Ads Intelligence` selected.
- Ads Intelligence top navigation is visible with:
  - `Центр управления`
  - `Настройки`
- The selected cabinet/category control shows `Фурнитура`.
- The page renders real-period metrics for `28.04.2026 — 28.05.2026`, including spend, revenue, DRR, orders, baskets, CTR, ROAS, and CPO.
- The page shows a truthful partial-data warning instead of inventing complete coverage:
  - campaigns with statistics: `7/79`
  - phrases with statistics: `27/64`
  - products with sales: `9/35`
  - latest sync: `29.05.2026, 00:24:47`
- Rows without detailed statistics render empty dashes instead of fabricated values.
- Follow-up frontend change after this screenshot: `CommandCenter.tsx` now renders a visible `Пульт решений` panel from real active backend recommendations, with partial-data guardrails and quick apply/pause/open/dismiss controls. This still requires authenticated browser confirmation in the real workspace.

## Still Required For Release-Closing QA

The first attempt to open `Support evidence` in the authenticated workspace exposed a frontend routing defect: the user was dropped out of the current workspace when entering the support screen.

Fix applied in `/Users/panfiloveshow/Documents/front2/frontend-from-server/src/modules/ads-intelligence/AdsIntelligenceLayout.tsx`: the diagnostics screen is no longer shown as a customer-facing Ads Intelligence navigation tab. The route remains available by direct URL for internal support/debug use, while normal users stay focused on the automated command center. Regression coverage was added in `AdsIntelligenceLayout.test.tsx`.

This screenshot plus the follow-up defect report do not yet prove the full support-evidence checklist. The remaining manual checks are:

1. Open `/ads-intelligence/support` by direct URL in the same authenticated workspace when support/debug investigation is needed.
2. Confirm the diagnostics route stays inside the selected workspace.
3. Load a real campaign/product/query scope.
4. Confirm the frontend calls `GET /api/v1/extension/evidence-debug/report`.
5. Confirm the page renders backend-provided summary, readiness/freshness, sections, checklist, issues, and next actions.
6. Confirm missing extension evidence remains a truthful empty/partial/stale/missing-evidence state.
7. Cross-check the same scope in extension Options support evidence debug.
8. Reopen `Центр управления` and confirm `Пульт решений` appears when the backend returns active recommendations, or the truthful no-actions state appears when it does not.

## Status

Authenticated workspace access is partially proven, and the first support-route workspace retention defect has been fixed. Final support-evidence QA remains open until `docs/WB_ADS_AUTHENTICATED_QA_CHECKLIST.md` pass criteria are completed after retesting the support route.
