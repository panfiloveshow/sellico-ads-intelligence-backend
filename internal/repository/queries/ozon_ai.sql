-- Ozon module phase 3 queries (AI autopilot: runs, decisions, context pack).

-- name: InsertAIRun :one
INSERT INTO ai_runs (workspace_id, seller_cabinet_id, strategy_id, status, trigger)
VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('seller_cabinet_id'),
    sqlc.narg('strategy_id'), sqlc.arg('status'), sqlc.arg('trigger')
)
RETURNING *;

-- name: FinishAIRun :exec
UPDATE ai_runs SET
    status = sqlc.arg('status'),
    summary = sqlc.narg('summary'),
    error = sqlc.narg('error'),
    prompt_tokens = sqlc.arg('prompt_tokens'),
    completion_tokens = sqlc.arg('completion_tokens'),
    finished_at = now()
WHERE id = sqlc.arg('id');

-- GetRunningAIRunForCabinet powers the 409 on manual runs. Runs older than
-- one hour are treated as stale (crashed worker) and no longer block.
-- name: GetRunningAIRunForCabinet :one
SELECT * FROM ai_runs
WHERE seller_cabinet_id = $1
  AND status = 'running'
  AND started_at > now() - INTERVAL '1 hour'
ORDER BY started_at DESC
LIMIT 1;

-- name: ListAIRunsByCabinet :many
SELECT * FROM ai_runs
WHERE workspace_id = sqlc.arg('workspace_id')
  AND seller_cabinet_id = sqlc.arg('seller_cabinet_id')
ORDER BY started_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAIRunsByCabinet :one
SELECT COUNT(*) FROM ai_runs
WHERE workspace_id = sqlc.arg('workspace_id')
  AND seller_cabinet_id = sqlc.arg('seller_cabinet_id');

-- name: InsertAIDecision :one
INSERT INTO ai_decisions (
    run_id, workspace_id, seller_cabinet_id, action_type, target, proposal,
    rationale, expected_effect, guardrail_verdict, status, error
)
VALUES (
    sqlc.arg('run_id'), sqlc.arg('workspace_id'), sqlc.arg('seller_cabinet_id'),
    sqlc.arg('action_type'), sqlc.arg('target'), sqlc.arg('proposal'),
    sqlc.narg('rationale'), sqlc.narg('expected_effect'),
    sqlc.arg('guardrail_verdict'), sqlc.arg('status'), sqlc.narg('error')
)
RETURNING *;

-- name: GetAIDecisionByID :one
SELECT * FROM ai_decisions
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id');

-- name: SetAIDecisionStatus :exec
UPDATE ai_decisions SET
    status = sqlc.arg('status'),
    error = sqlc.narg('error'),
    applied_at = CASE WHEN sqlc.arg('status')::text IN ('applied', 'auto_applied')
                      THEN now() ELSE applied_at END,
    applied_by = COALESCE(sqlc.narg('applied_by'), applied_by)
WHERE id = sqlc.arg('id');

-- name: ListAIDecisions :many
SELECT * FROM ai_decisions
WHERE workspace_id = sqlc.arg('workspace_id')
  AND seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('run_id')::uuid IS NULL OR run_id = sqlc.narg('run_id')::uuid)
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAIDecisions :one
SELECT COUNT(*) FROM ai_decisions
WHERE workspace_id = sqlc.arg('workspace_id')
  AND seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('run_id')::uuid IS NULL OR run_id = sqlc.narg('run_id')::uuid);

-- GetAIDecisionGuardState feeds the per-target cooldown/daily-cap guardrail
-- from the AI side of history (ozon_bid_changes covers the write side).
-- Only decisions that were or still may be applied count: shadow rows count
-- too — promoting a strategy level must not bypass a cooldown the shadow run
-- just started (same rule as the deterministic strategy guard).
-- name: GetAIDecisionGuardState :one
SELECT
    COUNT(*) FILTER (WHERE created_at >= sqlc.arg('day_start'))::bigint AS changes_today,
    MAX(created_at)::timestamptz AS last_change_at
FROM ai_decisions
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND action_type = sqlc.arg('action_type')
  AND target @> sqlc.arg('target')::jsonb
  AND status IN ('shadow', 'proposed', 'approved', 'auto_applied', 'applied');

-- ListActiveOzonAICabinets enumerates cabinets the ozon:ai_sweep fans out to:
-- every active Ozon cabinet that has at least one active ozon_ai_autopilot
-- strategy.
-- name: ListActiveOzonAICabinets :many
SELECT DISTINCT s.seller_cabinet_id
FROM strategies s
JOIN seller_cabinets sc ON sc.id = s.seller_cabinet_id
WHERE s.type = 'ozon_ai_autopilot'
  AND s.is_active = TRUE
  AND sc.marketplace = 'ozon'
  AND sc.deleted_at IS NULL;

-- GetActiveOzonAIStrategyForCabinet picks the strategy a cabinet run uses.
-- Multiple active AI strategies on one cabinet make no sense; the newest wins.
-- name: GetActiveOzonAIStrategyForCabinet :one
SELECT * FROM strategies
WHERE seller_cabinet_id = $1
  AND type = 'ozon_ai_autopilot'
  AND is_active = TRUE
ORDER BY updated_at DESC
LIMIT 1;

-- ListOzonCampaignDailyStatsSince returns per-campaign daily rows for the
-- context pack (14-day window), keyed by the local campaign UUID.
-- name: ListOzonCampaignDailyStatsSince :many
SELECT s.campaign_id, s.date, s.views, s.clicks, s.spend_rub, s.orders, s.revenue_rub
FROM ozon_campaign_stats s
JOIN ozon_campaigns c ON c.id = s.campaign_id
WHERE c.seller_cabinet_id = $1 AND s.date >= $2
ORDER BY s.campaign_id, s.date;

-- name: ListOzonProductPricesBySkus :many
SELECT * FROM ozon_product_prices
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND sku = ANY(sqlc.arg('skus')::bigint[]);

-- GetOzonAIBidGuardState mirrors GetOzonStrategyGuardState for a single
-- (campaign, sku) target, counting both strategy and AI writes so the two
-- automation paths share one cooldown budget.
-- name: GetOzonAIBidGuardState :one
SELECT
    COUNT(*) FILTER (WHERE created_at >= sqlc.arg('day_start'))::bigint AS changes_today,
    MAX(created_at)::timestamptz AS last_change_at
FROM ozon_bid_changes
WHERE campaign_id = sqlc.arg('campaign_id')
  AND sku = sqlc.arg('sku')
  AND source IN ('strategy', 'ai')
  AND status IN ('pending', 'applied', 'shadow');

-- GetOzonAICPOGuardState is the CPO flavor (no campaign; cabinet + sku).
-- name: GetOzonAICPOGuardState :one
SELECT
    COUNT(*) FILTER (WHERE created_at >= sqlc.arg('day_start'))::bigint AS changes_today,
    MAX(created_at)::timestamptz AS last_change_at
FROM ozon_bid_changes
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND sku = sqlc.arg('sku')
  AND kind = 'cpo'
  AND source IN ('strategy', 'ai')
  AND status IN ('pending', 'applied', 'shadow');
