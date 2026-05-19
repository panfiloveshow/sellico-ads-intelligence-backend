# Ads Intelligence RK Matrix

## Goal

Build an advertising operations core for WB campaigns: analytics first, controlled automation second.
The system must never invent unavailable WB data. Every metric must have a source and a quality state.

## Source Matrix

| Block | Product Question | WB Source | Current State | Next Backend Work | UI / Automation Use |
| --- | --- | --- | --- | --- | --- |
| Campaign inventory | What campaigns exist, what status/type/payment model, which products are inside? | Promotion: `GET /api/advert/v2/adverts`, `GET /adv/v1/promotion/count` | Partial: campaigns and campaign-product links exist | Harden sync status, preserve WB nmIDs, show cabinet-level data health | Campaign list, stale campaign warnings, automation eligibility |
| Campaign stats | How much did a campaign spend and return? | Promotion: `GET /adv/v3/fullstats` | Exists, but WB returns repeated `429` for some cabinets | Cooldown/partial metadata, retry windows, source freshness | KPI cards, ROAS/CTR/CPO diagnostics, rate-limit state |
| Product ad stats | Which product inside an RK drove impressions/clicks/spend/orders? | Promotion: nested `days[].apps[].nms[]` / `days[].nms[]` in `fullstats` | Implemented where WB returns breakdown | Keep exact-only; no artificial allocation | Product table with exact/ unavailable data mode |
| Campaign budgets | Can this RK run, pause, or scale safely? | Promotion: `GET /adv/v1/budget` | Implemented as `campaign_budgets` snapshots | Add cabinet balance and bid-state context | Budget warnings, launch prechecks, automation guardrail |
| Account balance | Does the cabinet have money for advertising? | Promotion: `GET /adv/v1/balance` | Client wrapper exists, not stored | Add cabinet balance snapshots | Cabinet health, top-up recommendations |
| Product card bids | What bid is set for nm in RK placement? | Promotion: `PATCH /api/advert/v1/bids`, bid recommendations endpoint | Update wrapper exists; snapshots incomplete | Sync/read current/recommended/min bids per nm placement | Bid history, bid recommendations, safe manual apply |
| Search cluster bids | What queries have custom bids? | Promotion: `/adv/v0/normquery/get-bids`, `/adv/v0/normquery/bids` | Fetch/set partially exists | Store query bid state per campaign/nm/query | Query-level bidding, bid diff, apply/revert |
| Minus phrases | Which queries are excluded? | Promotion: `/adv/v0/normquery/set-minus` | Local table exists, WB action not complete | Implement WB set/delete and sync local state | Anti-waste recommendations, apply minus phrase |
| Campaign actions | Can we start/pause/stop safely? | Promotion: `/adv/v0/start`, `/adv/v0/pause`, `/adv/v0/stop` | Wrappers/service exist | Add preflight checks: status, budget, data health, cooldown | Manual actions, later autopilot |
| Sales by nm | Did the product actually sell regardless of ad breakdown? | Reports: `/api/v1/supplier/orders`, `/api/v1/supplier/sales`, `/api/v5/supplier/reportDetailByPeriod` | Implemented first-pass daily `product_sales_daily` from orders/sales | Add realization report detail and UI surfacing | ROAS fallback, organic vs ad context, product quality |
| Search demand | Which search texts drive orders/positions? | Analytics: `/api/v2/search-report/product/orders` | Not implemented | Add optional analytics enrichment with access checks | Query expansion, SEO/ad query planning |

## Build Order

1. Data health and cooldown: no zombie jobs, clear `partial/rate_limited`, no duplicate sync.
2. Campaign budget snapshots: make automation-aware of money and runnable state.
3. Campaign balance snapshots: cabinet-level financial readiness.
4. Bid state snapshots: product/card and query bids, with exact source and timestamp.
5. Action preflight: start/pause/bid change only after budget/status/token/data checks.
6. Reports enrichment: sales/orders by `nmId` to contextualize ad decisions.
7. Automation dry-run: generate proposed actions with reasons and safety caps.
8. Automation apply: user-approved first, then guarded scheduled autopilot.

## Non-Negotiables

- Never allocate campaign spend to products unless WB returns product-level breakdown.
- Every automation action must be traceable to source metrics and a strategy.
- Every WB `429/401/403` must be visible in job metadata and UI.
- Refresh spam must return existing active job or cooldown state, not create duplicate load.
