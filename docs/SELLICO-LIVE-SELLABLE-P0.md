# Sellico Live Sellable P0

This plan keeps the project real-data-only: no demo metrics, mock campaigns, synthetic recommendations, or UI-only sample data in runtime flows.

## Scope

Sellico Live should become the evidence layer for WB Ads Intelligence: official WB API remains the source of business metrics, while the browser extension captures real cabinet context, visible bids, warnings, UI state, and allowlisted network responses.

Unit economics is not duplicated here. Ads Intelligence should consume or link to the existing unit-economics frontend and microservice when margin, profit, target DRR, or product profitability is needed.

## P0 Workstreams

### 1. Extension Auth Boundary

- Extension JWT is accepted only on extension routes.
- Extension JWT must contain the expected audience and workspace id.
- Extension JWT must not inherit owner/manager privileges.
- Extension ingest and widget routes validate workspace ownership for all entity ids.
- Extension raw payloads must be size-limited and sensitive fields redacted before storage.

Acceptance:

- Extension token cannot call normal campaigns, actions, settings, or sync endpoints.
- Extension token can call only widget and capture endpoints for its workspace.
- Cross-workspace extension token calls return access denied.

### 2. Evidence Normalizers

Convert captured cabinet evidence into typed facts instead of leaving it only as raw JSON:

- bid facts: current bid, recommended bid, competitive bid, min bid, placement, query, nmId, advertId;
- position facts: query, nmId, visible position, page, region;
- action facts: disabled controls, visible warning text, success/error messages;
- budget facts: visible budget/balance only when the endpoint is allowlisted and redacted.

Acceptance:

- Product, campaign, and query widgets can explain which cabinet evidence they used.
- Raw captures are evidence, not final analytics, until normalized and validated.

### 3. Value-First Extension Widget

The panel must lead with seller value:

- money/result summary when campaign stats exist;
- live bid and position evidence when captured;
- exact reason when Sellico has no recommendation;
- clear next action: capture, sync, open Sellico, or save visible bid/position.

Acceptance:

- The first visible block answers “what matters right now?”.
- Technical source/freshness/coverage is still visible, but secondary.
- Empty states explain the missing real-data prerequisite.

### 4. Daily Control Center

Ads Intelligence should surface a daily decision queue:

- waste without orders;
- working queries/products;
- campaigns with stale/partial data;
- high-spend rows with missing unit-economics profitability;
- actions requiring user approval.

Acceptance:

- The user sees concrete real-data tasks, not only tables.
- Each task has source metrics and a link to evidence/detail.

### 5. Existing Unit Economics Integration

Do not rebuild unit economics in Ads Intelligence. Use the existing service as a decision input:

- margin/profitability checks;
- target DRR/CPO;
- product-level profitability warnings;
- “ads are profitable/unprofitable” labels only when unit-economics data is available.

Acceptance:

- If unit economics is unavailable, show a truthful missing-data state.
- Never infer profit from ROAS alone.

### 6. Agency And Report Packaging

Prepare sellable workflows for agencies and power sellers:

- multi-cabinet health summary;
- client-ready 30-day WB ads audit;
- action history and before/after evidence;
- exports that state source and freshness.

Acceptance:

- A seller or agency can show what changed, why, and what result followed.
- Reports never include fabricated rows or fallback examples.

