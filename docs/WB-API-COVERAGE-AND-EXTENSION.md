# WB API Coverage and Browser Extension Plan

Sellico must treat official marketplace APIs as the primary source of business metrics and use the browser extension only as real cabinet evidence when API data is missing, delayed, rate-limited, or not exposed.

This document follows the project real-data-only rule: extension data is allowed only when it is captured from the user's real Wildberries cabinet. It must never become demo, mock, or synthetic analytics.

## Source Priority

| Priority | Source | Use for |
| --- | --- | --- |
| 1 | Official WB API | Spend, revenue, orders, carts, campaign settings, budgets, balances, action responses. |
| 2 | WB cabinet network capture | Real cabinet payloads shown to the seller, when the endpoint is allowlisted and captured from the user's browser. |
| 3 | WB cabinet DOM capture | UI-only hints: disabled actions, warnings, visible bid widgets, position snippets, rendered table values. |
| 4 | User input | Explicit limits, strategy settings, desired targets, manual notes. |

When a screen mixes sources, the API response should expose evidence metadata so the frontend can label the state as `api`, `extension`, or `mixed`.

## What Official API Covers

| Area | API source | Product state |
| --- | --- | --- |
| Campaign inventory | `/adv/v1/promotion/count`, `/api/advert/v2/adverts` | Core source for campaign IDs, names, statuses, payment type, bid type, placements, products. |
| Campaign metrics | `/adv/v3/fullstats` | Core source for campaign spend, revenue, views, clicks, `atbs`, orders, `shks`, cancellations. Limited by period, status, chunks, and rate limits. |
| Product ad metrics | `fullstats.days[].apps[].nms[]` | Core source for `advertId:nmId` metrics. If this path is missing, do not allocate campaign metrics to products. |
| Phrase / cluster metrics | `/adv/v1/normquery/stats` | Core source for `advertId:nmId:normQuery` views, clicks, spend, `atbs`, orders, CTR, CPC, CPM, avg position. No revenue, so no phrase DRR/ROAS. |
| Cluster bid state | `/adv/v0/normquery/get-bids`, `/adv/v0/normquery/bids`, `/adv/v0/normquery/set-minus` | Useful mostly for manual CPM cluster workflows. Must be guarded by campaign type. |
| Product bids | `/api/advert/v1/bids`, `/api/advert/v1/bids/min`, `/api/advert/v0/bids/recommendations` | Product bid changes and bid context. Recommendations are not universal across payment/bid types. |
| Budget and finance | `/adv/v1/balance`, `/adv/v1/budget`, `/adv/v1/upd`, `/adv/v1/payments` | Budget readiness, top-up history, finance context. |
| General product funnel | `/api/analytics/v3/sales-funnel/products` | Product-level total carts/orders. Keep separate from advertising `atbs`. |
| Organic search reports | `/api/v2/search-report/...` | SEO/organic demand only when the seller has access/Jam. Do not mix with advertising normquery attribution. |
| Reputation evidence | `/api/v1/feedbacks`, `/api/v1/questions`, `/api/v1/new-feedbacks-questions`, unanswered count endpoints on WB User Communication API | Real product feedback/question text and counters for card diagnostics. Exposed read-only through Sellico; no generated review data. |

## Gaps Where Extension Helps

| Gap | Why API is not enough | Extension capture |
| --- | --- | --- |
| Live cabinet warnings | API often returns status codes but not the exact seller-facing reason. | `ui_signals` from rendered warnings, disabled buttons, tooltips, validation text. |
| Action availability | Start/pause/bid controls can be disabled by cabinet-specific state. | DOM + network evidence before and after user-approved actions. |
| Auction hints | API bid recommendations can be type-limited or incomplete. | Visible bid/min/recommended/competitive/leadership snapshots from cabinet UI. |
| Historical holes | WB periods and rate limits can leave sync gaps. | Captured cabinet payloads and rendered table snapshots during real seller sessions. |
| Organic search without Jam | API search-report access depends on seller entitlement. | Public/cabinet search page position snapshots, clearly labeled as extension evidence. |
| UI-only filters/tables | Cabinet may expose slices that are not documented or not yet in public API. | Network captures and DOM table snapshots, stored as evidence until normalized. |
| Confirmation after actions | API success is not the same as seller seeing the changed state. | Before/after capture of visible status, bids, warnings, and action result messages. |

## Current Implementation State

Already implemented:

- MV3 extension for `seller.wildberries.ru` and `cmp.wildberries.ru`.
- Page context detection and Sellico floating panel.
- Allowlisted fetch/XHR capture through `page-bridge.js`.
- Backend ingest for page contexts, network captures, UI signals, bid snapshots, and position snapshots.
- Evidence attachment to campaign/product/phrase read models.

Added coverage in this pass:

- Network capture allowlist now includes campaign inventory/settings, normquery stats/bids/minus, product bids, budget, balance, finance, sales funnel, and search-report endpoints.
- Network capture allowlist also includes observed WB cabinet UI endpoints such as `/api/v5/fullstat`, `/api/v1/advert/...`, `/api/v1/advert/.../placement`, and `/api/v5/analyst-info`. These are stored as cabinet evidence, not treated as official API metrics until normalized and validated.
- Backend allowed endpoint keys now accepts the same expanded capture categories.
- Backend normalizers derive typed bid snapshots, position snapshots, and UI signals from allowlisted real WB cabinet network captures.
- Extension widget responses include a `primary_insight` block so the first visible panel section can explain what matters now, which real evidence was used, and the safest next action.
- Backend stores visible WB cabinet table rows in `extension_dom_row_snapshots` via `POST /api/v1/extension/dom-row-snapshots`. These rows are evidence only and never backfill missing spend, revenue, orders, or WB API metrics.
- Backend exposes `GET /api/v1/extension/evidence-debug` for scoped campaign/product/query support, showing the latest real page contexts, network captures, DOM row snapshots, typed facts, freshness, and missing-evidence issues.
- Backend exposes `GET /api/v1/extension/evidence-debug/report` as a support-screen view model over the same real evidence: source label, readiness, sections, checklist, issues, and next actions. It does not add another data source and does not synthesize missing business metrics.
- The extension panel consumes scoped evidence debug when an entity is known and shows a guided capture checklist for page context, network captures, DOM table rows, bids, positions, UI signals, and Sellico recommendations.
- The extension panel now includes a first-run guided evidence flow: campaign, query/cluster table, bid/auction evidence, product/position evidence, and final Sellico recommendation readiness. The flow is computed from real page context, widget data, and evidence-debug counts only.
- The extension Options page now includes a support evidence debug screen that renders `GET /api/v1/extension/evidence-debug/report` for real campaign/product/query troubleshooting.
- The Sellico frontend in `/Users/panfiloveshow/Documents/front2/frontend-from-server` now includes `/ads-intelligence/support`, a support evidence screen backed by `GET /api/v1/extension/evidence-debug/report`. It renders backend readiness, freshness, evidence sections, checklist, issues, and next actions without local sample metrics.
- Ads read-model evidence now exposes explicit source labels: `API WB`, `Кабинет WB`, `API WB + кабинет WB`, and `Расчет Sellico`. If official synced bid state differs from live cabinet bid evidence, the response surfaces an `api_extension_bid_mismatch` issue instead of silently choosing one value.
- Autopilot bid increases now use extension bid evidence as a preflight guardrail: if the latest live cabinet bid differs from the synced WB API bid, the strategy skips the increase until sync is refreshed or cabinet evidence is recaptured. Extension evidence never replaces the official bid value for the action payload.
- Backend now has a read-only WB User Communication API client for questions, feedbacks, new-item flags, and unanswered counters. `GET /api/v1/seller-cabinets/{id}/communication/reputation?nm_id=...` returns real WB reputation evidence for product diagnostics and surfaces WB API/permission errors truthfully.

Remaining follow-up:

- No known dedicated support evidence screen gap remains after wiring the user-supplied frontend.
- Product tables/details should keep expanding visible source labels and mismatch issues where they help operators diagnose data-health problems.

## Recommended Build Order

1. Verify the Sellico frontend support screen with typecheck/build and a real backend response.
2. Use source labels, mismatch issues, and User Communication reputation evidence in the Sellico frontend tables/details.
3. Keep extension support debug as the QA/support fallback for real cabinet evidence troubleshooting.

## Safety Rules

- Do not capture cookies, auth headers, or unrelated personal cabinet data.
- Keep endpoint allowlist narrow and explicit.
- Store raw captures as evidence, not as final analytics until parsed and validated.
- Never backfill missing spend/revenue/orders with inferred values.
- When WB API and extension disagree, show a data-health issue instead of silently choosing the prettier number.
