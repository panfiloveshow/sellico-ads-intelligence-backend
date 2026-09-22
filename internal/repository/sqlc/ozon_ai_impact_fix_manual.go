package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Every consumer uses the same validity rule, including before the first repair
// sweep after an upgrade. A legacy evaluation at day 3 cannot become trustworthy
// merely because the remaining days were synced later. Totals must also match
// the current complete windows, catching late attribution/sync corrections.
const aiImpactCompleteEvaluationPredicate = `COALESCE((
 d.outcome_status = 'evaluated'
 AND d.applied_at IS NOT NULL
 AND d.evaluated_at >= (((d.applied_at AT TIME ZONE 'UTC')::date + 8)::timestamp AT TIME ZONE 'UTC')
 AND now() >= (((d.applied_at AT TIME ZONE 'UTC')::date + 8)::timestamp AT TIME ZONE 'UTC')
 AND d.revenue_before_rub IS NOT NULL AND d.revenue_after_rub IS NOT NULL
 AND ((d.action_type IN ('cpo_bid', 'cpo_enable', 'cpo_disable')
  AND (SELECT count(DISTINCT s.date) FROM ozon_sales_daily s WHERE s.seller_cabinet_id=d.seller_cabinet_id
       AND s.sku::text=d.target->>'sku' AND s.date BETWEEN (d.applied_at AT TIME ZONE 'UTC')::date - 7
       AND (d.applied_at AT TIME ZONE 'UTC')::date - 1) = 7
  AND (SELECT count(DISTINCT s.date) FROM ozon_sales_daily s WHERE s.seller_cabinet_id=d.seller_cabinet_id
       AND s.sku::text=d.target->>'sku' AND s.date BETWEEN (d.applied_at AT TIME ZONE 'UTC')::date + 1
       AND (d.applied_at AT TIME ZONE 'UTC')::date + 7) = 7
  AND d.revenue_before_rub = (SELECT round(sum(s.revenue_rub),2) FROM ozon_sales_daily s
       WHERE s.seller_cabinet_id=d.seller_cabinet_id AND s.sku::text=d.target->>'sku'
       AND s.date BETWEEN (d.applied_at AT TIME ZONE 'UTC')::date - 7 AND (d.applied_at AT TIME ZONE 'UTC')::date - 1)
  AND d.revenue_after_rub = (SELECT round(sum(s.revenue_rub),2) FROM ozon_sales_daily s
       WHERE s.seller_cabinet_id=d.seller_cabinet_id AND s.sku::text=d.target->>'sku'
       AND s.date BETWEEN (d.applied_at AT TIME ZONE 'UTC')::date + 1 AND (d.applied_at AT TIME ZONE 'UTC')::date + 7)
 ) OR (
  d.spend_before_rub IS NOT NULL AND d.spend_after_rub IS NOT NULL
  AND EXISTS (
   SELECT 1 FROM ozon_campaigns c WHERE c.seller_cabinet_id=d.seller_cabinet_id
    AND c.ozon_campaign_id::text=d.target->>'ozon_campaign_id'
    AND (SELECT count(DISTINCT s.date) FROM ozon_campaign_stats s WHERE s.campaign_id=c.id
         AND s.date BETWEEN (d.applied_at AT TIME ZONE 'UTC')::date - 7
                        AND (d.applied_at AT TIME ZONE 'UTC')::date - 1) = 7
    AND (SELECT count(DISTINCT s.date) FROM ozon_campaign_stats s WHERE s.campaign_id=c.id
         AND s.date BETWEEN (d.applied_at AT TIME ZONE 'UTC')::date + 1
                        AND (d.applied_at AT TIME ZONE 'UTC')::date + 7) = 7
    AND (d.spend_before_rub,d.revenue_before_rub) = (SELECT round(sum(s.spend_rub),2),round(sum(s.revenue_rub),2)
         FROM ozon_campaign_stats s WHERE s.campaign_id=c.id AND s.date BETWEEN
         (d.applied_at AT TIME ZONE 'UTC')::date - 7 AND (d.applied_at AT TIME ZONE 'UTC')::date - 1)
    AND (d.spend_after_rub,d.revenue_after_rub) = (SELECT round(sum(s.spend_rub),2),round(sum(s.revenue_rub),2)
         FROM ozon_campaign_stats s WHERE s.campaign_id=c.id AND s.date BETWEEN
         (d.applied_at AT TIME ZONE 'UTC')::date + 1 AND (d.applied_at AT TIME ZONE 'UTC')::date + 7)
  )
 ))
), false)`

const aiImpactDecisionColumns = `d.id, d.run_id, d.workspace_id, d.seller_cabinet_id,
 d.action_type, d.target, d.proposal, d.rationale, d.expected_effect, d.guardrail_verdict,
 d.status, d.error, d.created_at, d.applied_at, d.applied_by, d.outcome_status,
 d.drr_before, d.drr_after, d.spend_before_rub, d.spend_after_rub,
 d.revenue_before_rub, d.revenue_after_rub, d.evaluated_at, d.total_drr_before, d.total_drr_after`

func aiImpactDecisionScanTargets(d *AiDecision) []any {
	return []any{&d.ID, &d.RunID, &d.WorkspaceID, &d.SellerCabinetID,
		&d.ActionType, &d.Target, &d.Proposal, &d.Rationale, &d.ExpectedEffect, &d.GuardrailVerdict,
		&d.Status, &d.Error, &d.CreatedAt, &d.AppliedAt, &d.AppliedBy, &d.OutcomeStatus,
		&d.DrrBefore, &d.DrrAfter, &d.SpendBeforeRub, &d.SpendAfterRub,
		&d.RevenueBeforeRub, &d.RevenueAfterRub, &d.EvaluatedAt, &d.TotalDrrBefore, &d.TotalDrrAfter}
}

// InvalidateIncompleteAIImpactOutcomes clears legacy partial results before they
// can continue to teach the model, unlock automation, or enter a money aggregate.
// Once repaired, valid outcomes are untouched. A missing-data result ages out
// through the ordinary evaluation path instead of silently becoming zero.
func (q *Queries) InvalidateIncompleteAIImpactOutcomes(ctx context.Context) error {
	_, err := q.db.Exec(ctx, `UPDATE ai_decisions d SET outcome_status='pending_eval',
 evaluated_at=NULL, drr_before=NULL, drr_after=NULL, spend_before_rub=NULL, spend_after_rub=NULL,
 revenue_before_rub=NULL, revenue_after_rub=NULL, total_drr_before=NULL, total_drr_after=NULL
 WHERE d.status IN ('applied','auto_applied') AND d.outcome_status='evaluated'
 AND NOT `+aiImpactCompleteEvaluationPredicate)
	return err
}

func collectAIImpactDecisions(rows pgx.Rows) ([]AiDecision, error) {
	defer rows.Close()
	decisions := []AiDecision{}
	for rows.Next() {
		var d AiDecision
		if err := rows.Scan(aiImpactDecisionScanTargets(&d)...); err != nil {
			return nil, err
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}

// ListAIDecisionsForCompleteImpactEval only schedules windows whose final day
// has finished; calendar dates are explicitly UTC, matching the service math.
func (q *Queries) ListAIDecisionsForCompleteImpactEval(ctx context.Context) ([]AiDecision, error) {
	rows, err := q.db.Query(ctx, `SELECT `+aiImpactDecisionColumns+` FROM ai_decisions d
 WHERE d.status IN ('applied','auto_applied') AND d.evaluated_at IS NULL
 AND d.applied_at IS NOT NULL
 AND now() >= (((d.applied_at AT TIME ZONE 'UTC')::date + 8)::timestamp AT TIME ZONE 'UTC')
 ORDER BY d.applied_at, d.id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	return collectAIImpactDecisions(rows)
}

type CompleteAIImpactDecision struct {
	Decision AiDecision
	Complete bool
}

func (q *Queries) ListCompleteAIImpactSummaryRows(ctx context.Context, arg ListAIDecisionImpactRowsParams) ([]CompleteAIImpactDecision, error) {
	rows, err := q.db.Query(ctx, `SELECT `+aiImpactDecisionColumns+`, `+aiImpactCompleteEvaluationPredicate+`
 FROM ai_decisions d WHERE d.workspace_id=$1 AND d.seller_cabinet_id=$2
 AND d.status IN ('applied','auto_applied') AND d.applied_at >= $3
 ORDER BY d.applied_at DESC, d.id DESC`, arg.WorkspaceID, arg.SellerCabinetID, arg.Since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []CompleteAIImpactDecision{}
	for rows.Next() {
		var result CompleteAIImpactDecision
		if err := rows.Scan(append(aiImpactDecisionScanTargets(&result.Decision), &result.Complete)...); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// Filter evaluated, complete pairs before selecting the newest observations.
// The service discards overlapping campaign windows before counting a streak;
// limiting raw decisions here would let duplicate or pending rows hide it.
func (q *Queries) ListRecentCompleteAIImpactDecisions(ctx context.Context, cabinetID pgtype.UUID) ([]AiDecision, error) {
	rows, err := q.db.Query(ctx, `SELECT `+aiImpactDecisionColumns+` FROM ai_decisions d
 WHERE d.seller_cabinet_id=$1 AND d.status IN ('applied','auto_applied')
 AND d.drr_before IS NOT NULL AND d.drr_after IS NOT NULL
 AND `+aiImpactCompleteEvaluationPredicate+` ORDER BY d.applied_at DESC, d.id DESC`, cabinetID)
	if err != nil {
		return nil, err
	}
	return collectAIImpactDecisions(rows)
}

// DowngradeOzonAIForImpact changes only the still-enabled autopilot level. A
// concurrent owner change to observation must not be overwritten by a stale
// whole-params update from the impact sweep.
func (q *Queries) DowngradeOzonAIForImpact(ctx context.Context, strategyID pgtype.UUID) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE strategies SET params=jsonb_set(params,'{automation_level}','2'::jsonb), updated_at=now()
 WHERE id=$1 AND is_active=true AND params->>'automation_level'='3'`, strategyID)
	return tag.RowsAffected() > 0, err
}
