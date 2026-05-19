# Project Rule: Real Data Only

This project is built as a sellable marketplace analytics service. Runtime product behavior must use only real data from Sellico, Wildberries, Ozon, Yandex Market, the production database, or explicit user input.

Do not add demo data, mock data, fake metrics, synthetic recommendations, seeded showcase entities, generated cabinets, or UI-only sample states to product code.

If real data is unavailable, show a truthful state instead:

- no connected cabinet
- sync required
- sync in progress
- empty period
- permission/API error
- stale or partial data warning

Allowed exceptions:

- Test-only mocks/fakes/fixtures in `*_test.go`, `__tests__`, or clearly isolated testdata.
- Documentation examples that are explicitly labeled as examples and are not executable product data.
- Visual loading skeletons that do not invent business metrics or entities.

Before merging any feature, check that no runtime path falls back to mock/demo/sample data. A beautiful empty state is acceptable; fabricated business data is not.
