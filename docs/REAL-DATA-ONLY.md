# Real Data Only Policy

Sellico Ads Intelligence must be trusted by sellers, marketers, and marketplace managers. Trust starts with one rule: product screens and backend decisions use real data only.

## Rule

No runtime mocks, demo payloads, seeded showcase data, fake campaigns, fake cabinets, synthetic metrics, or fallback recommendations.

The product may be empty, loading, stale, partial, or blocked by permissions. It must not pretend.

## Runtime Sources Allowed

- Sellico user/workspace/integration APIs.
- Marketplace APIs such as Wildberries, Ozon, and Yandex Market.
- Data previously synced from those APIs into our database.
- Data explicitly entered or uploaded by the user.
- Extension/browser evidence captured from the user's real cabinet.

## Required UX For Missing Data

When real data is missing, show the real state:

- connect a cabinet
- validate credentials
- run first sync
- wait for sync
- widen/change period
- fix API permissions
- show stale/partial data warning

Do not fill tables, charts, cards, recommendations, exports, or widgets with invented values.

## Tests

Mocks, fakes, fixtures, and synthetic records are allowed only in isolated tests and testdata. They must never be imported by production code or used as product fallbacks.

## Review Checklist

- No `mock*`, `demo*`, `sample*`, `fake*`, `seed*`, or hard-coded business entities in runtime code.
- Empty/error/loading states are explicit and useful.
- Recommendations include evidence or stay absent.
- Metrics shown in UI come from backend/domain data, not frontend guesses.
- Frontend never merges real API failures into demo success states.
