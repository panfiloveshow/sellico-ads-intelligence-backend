package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type GetAIDecisionExecutionGuardStateParams struct {
	DayStart          pgtype.Timestamptz
	SellerCabinetID   pgtype.UUID
	ActionType        string
	Target            []byte
	ExcludeDecisionID pgtype.UUID
}

// Proposed/shadow rows are observations, not writes. Opposite state changes
// share a cooldown; approval excludes its own claimed row.
func (q *Queries) GetAIDecisionExecutionGuardState(ctx context.Context, p GetAIDecisionExecutionGuardStateParams) (GetAIDecisionGuardStateRow, error) {
	var row GetAIDecisionGuardStateRow
	err := q.db.QueryRow(ctx, `SELECT
	 COUNT(*) FILTER(WHERE COALESCE(applied_at,created_at)>=$1)::bigint,
	 MAX(COALESCE(applied_at,created_at))
	 FROM ai_decisions WHERE seller_cabinet_id=$2
	 AND (action_type=$3
	 OR ($3 IN ('campaign_pause','campaign_activate') AND action_type IN ('campaign_pause','campaign_activate'))
	 OR ($3 IN ('cpo_enable','cpo_disable') AND action_type IN ('cpo_enable','cpo_disable')))
	 AND target @> $4::jsonb AND ($5::uuid IS NULL OR id<>$5)
	 AND status IN ('approved','auto_applied','applied')`, p.DayStart, p.SellerCabinetID, p.ActionType, p.Target, p.ExcludeDecisionID).Scan(&row.ChangesToday, &row.LastChangeAt)
	return row, err
}

// Return failures as well as applied decisions, plus a separate allocation
// for mature outcomes so frequent recent attempts cannot hide measured results.
func (q *Queries) ListAIDecisionFeedback(ctx context.Context, cabinetID pgtype.UUID, limit int32) ([]AiDecision, error) {
	rows, err := q.db.Query(ctx, `SELECT `+aiImpactDecisionColumns+`, `+aiImpactCompleteEvaluationPredicate+`
 FROM ai_decisions d WHERE d.id IN (
	 (SELECT id FROM ai_decisions WHERE seller_cabinet_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2)
	 UNION
	 (SELECT d.id FROM ai_decisions d WHERE d.seller_cabinet_id=$1
	  AND d.status IN ('auto_applied','applied') AND `+aiImpactCompleteEvaluationPredicate+`
	  ORDER BY d.applied_at DESC,d.id DESC LIMIT $2)
	 ) ORDER BY d.created_at DESC,d.id DESC`, cabinetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AiDecision{}
	for rows.Next() {
		var d AiDecision
		var complete bool
		if err := rows.Scan(append(aiImpactDecisionScanTargets(&d), &complete)...); err != nil {
			return nil, err
		}
		// Legacy day-3 results cannot become model evidence while waiting for
		// the repair sweep. Keep the action itself, but hide invalid numbers.
		if !complete {
			if d.OutcomeStatus.String == "evaluated" {
				d.OutcomeStatus = pgtype.Text{String: "pending_eval", Valid: true}
			}
			d.DrrBefore, d.DrrAfter = pgtype.Numeric{}, pgtype.Numeric{}
			d.SpendBeforeRub, d.SpendAfterRub = pgtype.Numeric{}, pgtype.Numeric{}
			d.RevenueBeforeRub, d.RevenueAfterRub = pgtype.Numeric{}, pgtype.Numeric{}
			d.TotalDrrBefore, d.TotalDrrAfter = pgtype.Numeric{}, pgtype.Numeric{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// The newest successful state event, including manual changes in Sellico.
// Historical AI rows without a state audit remain usable for observation.
type OzonAIStateEvent struct {
	Action string
	Source string
	At     pgtype.Timestamptz
}

func (q *Queries) GetLastOzonAIStateEvent(ctx context.Context, cabinetID, campaignID pgtype.UUID, ozonCampaignID int64) (OzonAIStateEvent, error) {
	var r OzonAIStateEvent
	err := q.db.QueryRow(ctx, `SELECT action,source,at FROM (
	 SELECT action_type AS action,'ai'::text AS source,applied_at AS at FROM ai_decisions
	 WHERE seller_cabinet_id=$1 AND target->>'ozon_campaign_id'=$3::bigint::text
	 AND action_type IN ('campaign_pause','campaign_activate') AND status IN ('applied','auto_applied')
	 UNION ALL
	 SELECT metadata->>'action_type',metadata->>'source',created_at FROM audit_logs
	 WHERE entity_id=$2 AND action='ozon_campaign_state_applied'
	 ) e WHERE at IS NOT NULL ORDER BY at DESC LIMIT 1`, cabinetID, campaignID, ozonCampaignID).Scan(&r.Action, &r.Source, &r.At)
	return r, err
}

func (q *Queries) HasOzonAIRepairAfter(ctx context.Context, campaignID, cabinetID pgtype.UUID, ozonCampaignID int64, after pgtype.Timestamptz) (bool, error) {
	var ok bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS(
	 SELECT 1 FROM ozon_bid_changes WHERE campaign_id=$1 AND applied_at>$4 AND status='applied' AND old_bid_rub IS DISTINCT FROM new_bid_rub
	 UNION ALL SELECT 1 FROM ai_decisions WHERE seller_cabinet_id=$2 AND target->>'ozon_campaign_id'=$3::bigint::text
	 AND applied_at>$4 AND status IN ('auto_applied','applied') AND action_type IN ('bid_change','budget_change')
	 )`, campaignID, cabinetID, ozonCampaignID, after).Scan(&ok)
	return ok, err
}
