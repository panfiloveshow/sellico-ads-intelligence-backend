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

-- --- AI impact («ИИ заработал/сэкономил») ---

-- ListAIDecisionsForImpactEval feeds the ozon:ai_impact_sweep job: applied
-- decisions at least 4 days old that have no final evaluation yet.
-- 'pending_eval' rows keep evaluated_at NULL and are rescanned until they
-- either gather 3 after-days of stats or age out past 14 days.
-- name: ListAIDecisionsForImpactEval :many
SELECT * FROM ai_decisions
WHERE status IN ('applied', 'auto_applied')
  AND evaluated_at IS NULL
  AND applied_at IS NOT NULL
  AND applied_at <= now() - INTERVAL '4 days'
ORDER BY applied_at
LIMIT 500;

-- name: SetAIDecisionOutcome :exec
UPDATE ai_decisions SET
    outcome_status = sqlc.arg('outcome_status'),
    drr_before = sqlc.narg('drr_before'),
    drr_after = sqlc.narg('drr_after'),
    spend_before_rub = sqlc.narg('spend_before_rub'),
    spend_after_rub = sqlc.narg('spend_after_rub'),
    revenue_before_rub = sqlc.narg('revenue_before_rub'),
    revenue_after_rub = sqlc.narg('revenue_after_rub'),
    evaluated_at = CASE WHEN sqlc.arg('outcome_status')::text IN ('evaluated', 'not_evaluable')
                        THEN now() ELSE evaluated_at END
WHERE id = sqlc.arg('id');

-- GetOzonCampaignStatsWindowTotals sums one campaign's stats over a closed
-- date window for the before/after impact comparison. days counts rows, i.e.
-- days that actually have data.
-- name: GetOzonCampaignStatsWindowTotals :one
SELECT
    COALESCE(SUM(spend_rub), 0)::numeric AS spend_rub,
    COALESCE(SUM(revenue_rub), 0)::numeric AS revenue_rub,
    COUNT(*)::bigint AS days
FROM ozon_campaign_stats
WHERE campaign_id = sqlc.arg('campaign_id')
  AND date BETWEEN sqlc.arg('date_from') AND sqlc.arg('date_to');

-- name: GetOzonCampaignByCabinetAndOzonID :one
SELECT * FROM ozon_campaigns
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND ozon_campaign_id = sqlc.arg('ozon_campaign_id');

-- ListAIDecisionImpactRows powers GET /ozon/ai/impact: every applied decision
-- of the last N days with its evaluation numbers (aggregation is done in Go —
-- the formulas are unit-tested there).
-- name: ListAIDecisionImpactRows :many
SELECT id, action_type, status, outcome_status, applied_at,
       drr_before, drr_after, spend_before_rub, spend_after_rub,
       revenue_before_rub, revenue_after_rub
FROM ai_decisions
WHERE workspace_id = sqlc.arg('workspace_id')
  AND seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND status IN ('applied', 'auto_applied')
  AND applied_at >= sqlc.arg('since');

-- --- Feedback loop: recent applied decisions + their measured outcomes ---

-- ListRecentAppliedAIDecisions feeds the «твои прошлые решения и что вышло»
-- section of the context pack: the cabinet's newest applied/auto_applied
-- decisions with the impact numbers written by the sweep (outcome_status,
-- drr_before/after, spend/revenue before/after). Newest first.
-- name: ListRecentAppliedAIDecisions :many
SELECT action_type, target, proposal, status, outcome_status, created_at,
       drr_before, drr_after, spend_before_rub, spend_after_rub,
       revenue_before_rub, revenue_after_rub
FROM ai_decisions
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND status IN ('applied', 'auto_applied')
ORDER BY created_at DESC
LIMIT sqlc.arg('lim');

-- --- Shadow → next-level readiness («готов ли к повышению уровня») ---

-- GetAIReadinessStats is a pure aggregate over a cabinet's ai_decisions for the
-- readiness endpoint (no writes). shadow_passed/shadow_total gives the
-- within-guardrails share; the evaluated drr delta feeds projected_drr_delta.
-- name: GetAIReadinessStats :one
SELECT
    COUNT(*)::bigint AS decisions_total,
    COUNT(*) FILTER (WHERE status = 'shadow')::bigint AS shadow_total,
    COUNT(*) FILTER (WHERE status = 'shadow' AND guardrail_verdict = 'passed')::bigint AS shadow_passed,
    MIN(created_at) FILTER (WHERE status = 'shadow')::timestamptz AS first_shadow_at,
    COUNT(*) FILTER (
        WHERE outcome_status = 'evaluated' AND drr_before IS NOT NULL AND drr_after IS NOT NULL
    )::bigint AS evaluated_pairs,
    COALESCE(AVG(drr_after - drr_before) FILTER (
        WHERE outcome_status = 'evaluated' AND drr_before IS NOT NULL AND drr_after IS NOT NULL
    ), 0)::numeric AS avg_drr_delta
FROM ai_decisions
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id');

-- ListAIDecisionsSince returns a cabinet's decisions created since a moment,
-- for the weekly recap's «что ИИ предложил/применил за неделю» summary.
-- name: ListAIDecisionsSince :many
SELECT action_type, status, target, proposal, rationale, created_at
FROM ai_decisions
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND created_at >= sqlc.arg('since')
ORDER BY created_at DESC
LIMIT 200;

-- --- Weekly natural-language report (ozon_ai_weekly_reports) ---

-- name: InsertOzonAIWeeklyReport :one
INSERT INTO ozon_ai_weekly_reports (
    seller_cabinet_id, period_start, period_end, drr_start, drr_end, text
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('period_start'), sqlc.arg('period_end'),
    sqlc.narg('drr_start'), sqlc.narg('drr_end'), sqlc.arg('text')
)
ON CONFLICT (seller_cabinet_id, period_start) DO NOTHING
RETURNING *;

-- GetLatestOzonAIWeeklyReport powers GET /ozon/ai/weekly-report (newest report
-- for a cabinet, or no rows).
-- name: GetLatestOzonAIWeeklyReport :one
SELECT * FROM ozon_ai_weekly_reports
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
ORDER BY period_start DESC
LIMIT 1;

-- GetOzonAIWeeklyReportForPeriod is the once-per-ISO-week generation guard.
-- name: GetOzonAIWeeklyReportForPeriod :one
SELECT * FROM ozon_ai_weekly_reports
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND period_start = sqlc.arg('period_start');
