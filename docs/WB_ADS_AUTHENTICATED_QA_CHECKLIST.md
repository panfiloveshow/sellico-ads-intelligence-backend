# WB Ads Authenticated QA Checklist

Date: 2026-05-28

Use this checklist for the final manual verification that cannot be proven by unauthenticated headless tests. It must be run with a real Sellico user, a real workspace, and real WB/Sellico evidence data.

## Preconditions

- Sellico frontend is running from `/Users/panfiloveshow/Documents/front2/frontend-from-server`.
- Backend is running with the same API base that the frontend uses.
- The tester is logged in as a real Sellico user.
- The selected workspace has at least one connected WB seller cabinet or a truthful empty/sync-required state.
- No local demo/sample/fake campaign, cabinet, metric, recommendation, or support report data is injected.

Current run evidence:

- `docs/WB_ADS_AUTHENTICATED_QA_RUN_2026-05-29.md` partially proves authenticated workspace access and truthful partial-data rendering on the Ads Intelligence command center.
- The support evidence screen checks below still must be completed before release closure.

## Support Evidence Screen

1. Open `/ads-intelligence/support` in the authenticated Sellico app.
2. Confirm the page is shown under the Ads Intelligence layout by direct support/debug URL. This screen should not be promoted as a normal customer workflow.
3. Load a real campaign scope:
   - `scope=campaign`
   - a real campaign UUID if available
4. Confirm the request goes to `GET /api/v1/extension/evidence-debug/report`.
5. Confirm the page renders backend-provided:
   - summary/source label
   - readiness and freshness
   - evidence sections
   - checklist
   - issues
   - next actions
6. Confirm missing data is shown truthfully as empty, partial, stale, missing evidence, permission/API error, or ready.
7. Confirm the page does not show demo/sample/fake business metrics.

## Empty Or Partial Real Data

1. Use a campaign/product/query with no recent extension evidence.
2. Confirm the UI shows missing or partial evidence from the backend report.
3. Confirm no synthetic spend, revenue, orders, bids, positions, recommendations, or cabinet rows are invented.

## Extension Cross-Check

1. Open the extension Options support evidence debug screen.
2. Load the same campaign/product/query scope.
3. Confirm both extension Options and Sellico web app report the same backend readiness/checklist/issue state.

## Pass Criteria

- The authenticated route renders without redirecting to login or workspace selection.
- The page uses the backend `evidence-debug/report` response as its source of truth.
- Missing evidence remains a truthful support state.
- No runtime path falls back to demo/mock/sample business data.
