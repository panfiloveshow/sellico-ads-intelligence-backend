package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

var ErrManagerAdResolutionChanged = errors.New("advertising manual resolution changed, expired or target operation is no longer unknown")

func formatRub2(raw string) (string, *big.Rat, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(raw))
	if !ok || r.Sign() < 0 {
		return "", nil, errors.New("invalid observed manager monetary amount")
	}
	return r.FloatString(2), r, nil
}

func computeObservedCPO(spend *big.Rat, orders int64, verified bool) *string {
	if !verified || orders <= 0 || spend == nil {
		return nil
	}
	cpo := new(big.Rat).Quo(spend, new(big.Rat).SetInt64(orders))
	txt := cpo.FloatString(2)
	return &txt
}

// ManagerAdsAttribution computes full store/campaign spend, attributed orders/revenue,
// unattributed spend, observed CPO, and strategy-level write-capability breakdown.
func (q *Queries) ManagerAdsAttribution(ctx context.Context, cabinet domain.ManagerAdsCabinet, from, to time.Time, campaignID *uuid.UUID) (*domain.ManagerAdAttributionReport, error) {
	periodDays := int64(to.Sub(from).Hours()/24) + 1
	if periodDays < 1 {
		return nil, errors.New("invalid manager attribution period")
	}

	var totalSpendRaw, attrRevRaw, unattrSpendRaw string
	var attrOrders, observedDays, statRows, completeRows int64
	strategies := make([]domain.ManagerAdStrategyBreakdown, 0)

	switch cabinet.Marketplace {
	case "wb":
		args := []any{cabinet.ID, cabinet.WorkspaceID, from, to}
		campaignFilter := ""
		if campaignID != nil {
			campaignFilter = " AND c.id = $5"
			args = append(args, *campaignID)
		}
		summarySQL := `
			SELECT
				COALESCE(SUM(s.spend), 0)::text,
				COALESCE(SUM(s.orders), 0)::bigint,
				COALESCE(SUM(s.revenue), 0)::text,
				COALESCE(SUM(CASE WHEN s.orders IS NULL OR s.orders = 0 THEN s.spend ELSE 0 END), 0)::text,
				COUNT(DISTINCT s.date)::bigint,
				COUNT(s.id)::bigint,
				COUNT(CASE WHEN s.orders IS NOT NULL AND s.revenue IS NOT NULL THEN 1 END)::bigint
			FROM campaigns c
			LEFT JOIN campaign_stats s ON s.campaign_id = c.id AND s.date BETWEEN $3 AND $4
			WHERE c.seller_cabinet_id = $1 AND c.workspace_id = $2` + campaignFilter
		if err := q.db.QueryRow(ctx, summarySQL, args...).Scan(
			&totalSpendRaw, &attrOrders, &attrRevRaw, &unattrSpendRaw, &observedDays, &statRows, &completeRows,
		); err != nil {
			return nil, err
		}

		stratSQL := `
			SELECT
				CASE
					WHEN c.status = '4' THEN 'wb_status_4_draft'
					WHEN c.campaign_type = 8 OR lower(COALESCE(c.bid_type, '')) IN ('auto', 'unified') THEN 'wb_auto_unified'
					WHEN lower(COALESCE(c.bid_type, '')) = 'manual' AND lower(COALESCE(c.payment_type, '')) IN ('cpc', 'cpm')
						THEN 'wb_manual_' || lower(c.payment_type)
					ELSE 'wb_other'
				END AS strategy_code,
				CASE
					WHEN c.status <> '4' AND c.campaign_type <> 8
						AND lower(COALESCE(c.bid_type, '')) = 'manual'
						AND lower(COALESCE(c.payment_type, '')) IN ('cpc', 'cpm')
					THEN true ELSE false
				END AS direct_write_supported,
				COUNT(DISTINCT c.id)::bigint AS campaigns_count,
				COALESCE(SUM(s.spend), 0)::text AS spend_rub,
				COALESCE(SUM(s.orders), 0)::bigint AS attributed_orders,
				COUNT(s.id)::bigint AS stat_rows,
				COUNT(CASE WHEN s.orders IS NOT NULL AND s.revenue IS NOT NULL THEN 1 END)::bigint AS complete_rows
			FROM campaigns c
			LEFT JOIN campaign_stats s ON s.campaign_id = c.id AND s.date BETWEEN $3 AND $4
			WHERE c.seller_cabinet_id = $1 AND c.workspace_id = $2` + campaignFilter + `
			GROUP BY 1, 2
			ORDER BY 1`
		rows, err := q.db.Query(ctx, stratSQL, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var b domain.ManagerAdStrategyBreakdown
			var rawSpend string
			var sRows, sComplete int64
			if err := rows.Scan(&b.StrategyCode, &b.DirectWriteSupported, &b.CampaignsCount, &rawSpend, &b.AttributedOrders, &sRows, &sComplete); err != nil {
				return nil, err
			}
			formatted, spendRat, err := formatRub2(rawSpend)
			if err != nil {
				return nil, err
			}
			b.SpendRub = formatted
			b.ObservedCPORub = computeObservedCPO(spendRat, b.AttributedOrders, sRows > 0 && sRows == sComplete)
			strategies = append(strategies, b)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}

	case "ozon":
		args := []any{cabinet.ID, from, to}
		campaignFilter := ""
		if campaignID != nil {
			campaignFilter = " AND c.id = $4"
			args = append(args, *campaignID)
		}
		summarySQL := `
			SELECT
				COALESCE(SUM(s.spend_rub), 0)::text,
				COALESCE(SUM(s.orders), 0)::bigint,
				COALESCE(SUM(s.revenue_rub), 0)::text,
				COALESCE(SUM(CASE WHEN s.orders = 0 THEN s.spend_rub ELSE 0 END), 0)::text,
				COUNT(DISTINCT s.date)::bigint,
				COUNT(s.id)::bigint,
				COUNT(s.id)::bigint
			FROM ozon_campaigns c
			LEFT JOIN ozon_campaign_stats s ON s.campaign_id = c.id AND s.date BETWEEN $2 AND $3
			WHERE c.seller_cabinet_id = $1` + campaignFilter
		if err := q.db.QueryRow(ctx, summarySQL, args...).Scan(
			&totalSpendRaw, &attrOrders, &attrRevRaw, &unattrSpendRaw, &observedDays, &statRows, &completeRows,
		); err != nil {
			return nil, err
		}

		stratSQL := `
			SELECT
				CASE
					WHEN COALESCE(c.adv_object_type, '') = 'SKU'
						AND COALESCE(c.autopilot_strategy, '') IN ('', 'NO_AUTO_STRATEGY', 'TARGET_BIDS')
						THEN 'ozon_sku_cpc'
					WHEN COALESCE(c.adv_object_type, '') = 'SEARCH_PROMO'
						OR COALESCE(c.autopilot_strategy, '') ILIKE '%CPO%'
						THEN 'ozon_cpo_search_promo'
					ELSE 'ozon_other'
				END AS strategy_code,
				CASE
					WHEN COALESCE(c.adv_object_type, '') = 'SKU'
						AND COALESCE(c.autopilot_strategy, '') IN ('', 'NO_AUTO_STRATEGY', 'TARGET_BIDS')
						THEN true ELSE false
				END AS direct_write_supported,
				COUNT(DISTINCT c.id)::bigint AS campaigns_count,
				COALESCE(SUM(s.spend_rub), 0)::text AS spend_rub,
				COALESCE(SUM(s.orders), 0)::bigint AS attributed_orders,
				COUNT(s.id)::bigint AS stat_rows
			FROM ozon_campaigns c
			LEFT JOIN ozon_campaign_stats s ON s.campaign_id = c.id AND s.date BETWEEN $2 AND $3
			WHERE c.seller_cabinet_id = $1` + campaignFilter + `
			GROUP BY 1, 2
			ORDER BY 1`
		rows, err := q.db.Query(ctx, stratSQL, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var b domain.ManagerAdStrategyBreakdown
			var rawSpend string
			var sRows int64
			if err := rows.Scan(&b.StrategyCode, &b.DirectWriteSupported, &b.CampaignsCount, &rawSpend, &b.AttributedOrders, &sRows); err != nil {
				return nil, err
			}
			formatted, spendRat, err := formatRub2(rawSpend)
			if err != nil {
				return nil, err
			}
			b.SpendRub = formatted
			b.ObservedCPORub = computeObservedCPO(spendRat, b.AttributedOrders, sRows > 0)
			strategies = append(strategies, b)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}

	default:
		return nil, errors.New("unsupported manager marketplace")
	}

	totalSpend, totalSpendRat, err := formatRub2(totalSpendRaw)
	if err != nil {
		return nil, err
	}
	attrRev, _, err := formatRub2(attrRevRaw)
	if err != nil {
		return nil, err
	}
	unattrSpend, _, err := formatRub2(unattrSpendRaw)
	if err != nil {
		return nil, err
	}

	verified := observedDays > 0 && statRows > 0 && statRows == completeRows
	missingDays := periodDays - observedDays
	if missingDays < 0 {
		missingDays = 0
	}

	return &domain.ManagerAdAttributionReport{
		Marketplace:          cabinet.Marketplace,
		CampaignID:           campaignID,
		Period:               domain.ManagerAdsPeriod{DateFrom: from.Format(time.DateOnly), DateTo: to.Format(time.DateOnly), Timezone: "Europe/Moscow"},
		Currency:             "RUB",
		RetrievedAt:          time.Now().UTC(),
		TotalSpendRub:        totalSpend,
		AttributedOrders:     attrOrders,
		AttributedRevenueRub: attrRev,
		UnattributedSpendRub: unattrSpend,
		ObservedCPORub:       computeObservedCPO(totalSpendRat, attrOrders, verified),
		AttributionVerified:  verified,
		ObservedDays:         observedDays,
		MissingDaysCount:     missingDays,
		Strategies:           strategies,
	}, nil
}

func (q *Queries) GetWorkspaceManagerAdOperation(ctx context.Context, id, workspace uuid.UUID) (domain.ManagerAdOperation, error) {
	return scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2`, id, workspace))
}

func (q *Queries) ManagerAdOperationHoldActive(ctx context.Context, operationID uuid.UUID) (bool, error) {
	var held bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ad_native_resource_holds WHERE command_id=$1)`, operationID).Scan(&held)
	return held, err
}

const managerAdResolutionColumns = `id,fingerprint,workspace_id,actor_id,seller_cabinet_id,external_user_id,external_workspace_id,
 status,operation_id,operation_type,integration_id,integration_name,marketplace,campaign_id,external_id,
 reason,support_reference,confirmations,created_at,expires_at,resolved_at`

func scanManagerAdResolution(row rowScanner) (domain.ManagerAdManualResolution, error) {
	var r domain.ManagerAdManualResolution
	var checks []byte
	if err := row.Scan(
		&r.ID, &r.Fingerprint, &r.WorkspaceID, &r.ActorID, &r.CabinetID, &r.ExternalUserID, &r.ExternalWorkspaceID,
		&r.Status, &r.OperationID, &r.OperationType, &r.IntegrationID, &r.IntegrationName, &r.Marketplace,
		&r.CampaignID, &r.ExternalID, &r.Reason, &r.SupportReference, &checks, &r.CreatedAt, &r.ExpiresAt, &r.ResolvedAt,
	); err != nil {
		return r, err
	}
	r.Fingerprint = strings.TrimSpace(r.Fingerprint)
	r.CreatedAt = r.CreatedAt.UTC()
	r.ExpiresAt = r.ExpiresAt.UTC()
	if r.ResolvedAt != nil {
		utc := r.ResolvedAt.UTC()
		r.ResolvedAt = &utc
	}
	if len(checks) > 0 && string(checks) != "null" {
		if err := json.Unmarshal(checks, &r.Confirmations); err != nil {
			return r, err
		}
	}
	r.NativeOutcomeVerified = false
	r.NativeCampaignsChanged = false
	return r, nil
}

func (q *Queries) CreateManagerAdManualResolution(ctx context.Context, r domain.ManagerAdManualResolution) error {
	_, err := q.db.Exec(ctx, `INSERT INTO manager_ad_manual_resolutions
 (id,workspace_id,actor_id,seller_cabinet_id,external_user_id,external_workspace_id,integration_id,integration_name,
  marketplace,operation_id,operation_type,campaign_id,external_id,reason,support_reference,fingerprint,status,created_at,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'awaiting_confirmation',$17,$18)`,
		r.ID, r.WorkspaceID, r.ActorID, r.CabinetID, r.ExternalUserID, r.ExternalWorkspaceID, r.IntegrationID, r.IntegrationName,
		r.Marketplace, r.OperationID, r.OperationType, r.CampaignID, r.ExternalID, r.Reason, r.SupportReference, r.Fingerprint, r.CreatedAt, r.ExpiresAt)
	return err
}

func (q *Queries) GetManagerAdManualResolution(ctx context.Context, id, operationID, workspace, actor uuid.UUID) (domain.ManagerAdManualResolution, error) {
	return scanManagerAdResolution(q.db.QueryRow(ctx, `SELECT `+managerAdResolutionColumns+`
 FROM manager_ad_manual_resolutions
 WHERE id=$1 AND operation_id=$2 AND workspace_id=$3 AND actor_id=$4`, id, operationID, workspace, actor))
}

func (q *Queries) ExpireManagerAdManualResolution(ctx context.Context, r domain.ManagerAdManualResolution) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_manual_resolutions SET status='expired'
 WHERE id=$1 AND operation_id=$2 AND workspace_id=$3 AND actor_id=$4
   AND status='awaiting_confirmation' AND expires_at<=statement_timestamp()`,
		r.ID, r.OperationID, r.WorkspaceID, r.ActorID)
	return err
}

func (q *Queries) CancelManagerAdManualResolution(ctx context.Context, r domain.ManagerAdManualResolution) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_manual_resolutions SET status='cancelled'
 WHERE id=$1 AND operation_id=$2 AND workspace_id=$3 AND actor_id=$4
   AND fingerprint=$5 AND status='awaiting_confirmation'`,
		r.ID, r.OperationID, r.WorkspaceID, r.ActorID, r.Fingerprint)
	return err
}

func (q *Queries) ApplyManagerAdManualResolution(ctx context.Context, r domain.ManagerAdManualResolution, checks map[string]bool, validate func(context.Context) error) error {
	if validate == nil || r.NativeOutcomeVerified || r.NativeCampaignsChanged {
		return ErrManagerAdResolutionChanged
	}
	expected := map[string]bool{
		"ad_cabinet_state_checked":      true,
		"ad_no_pending_mutation":        true,
		"ad_late_effect_risk_accepted":  true,
		"ad_hold_release_understood":    true,
	}
	if len(checks) != len(expected) {
		return ErrManagerAdResolutionChanged
	}
	for k := range expected {
		if !checks[k] {
			return ErrManagerAdResolutionChanged
		}
	}

	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())

	op, err := scanManagerAd(tx.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, r.OperationID, r.WorkspaceID))
	if err != nil {
		return err
	}
	if err = acquireAdAccount(ctx, tx, op.Request.AccountKey); err != nil {
		return err
	}

	current, err := scanManagerAdResolution(tx.QueryRow(ctx, `SELECT `+managerAdResolutionColumns+`
 FROM manager_ad_manual_resolutions
 WHERE id=$1 AND operation_id=$2 AND workspace_id=$3 AND actor_id=$4 FOR UPDATE`,
		r.ID, r.OperationID, r.WorkspaceID, r.ActorID))
	if err != nil {
		return err
	}
	if current.Status == "resolved_manually" {
		return nil
	}
	if current.Status != "awaiting_confirmation" || current.Fingerprint != r.Fingerprint ||
		current.CabinetID != r.CabinetID || current.ExternalUserID != r.ExternalUserID || current.ExternalWorkspaceID != r.ExternalWorkspaceID ||
		current.IntegrationID != r.IntegrationID || current.Marketplace != r.Marketplace || current.CampaignID != r.CampaignID ||
		current.ExternalID != r.ExternalID || current.Reason != r.Reason || current.SupportReference != r.SupportReference ||
		!current.CreatedAt.Equal(r.CreatedAt) || !current.ExpiresAt.Equal(r.ExpiresAt) {
		return ErrManagerAdResolutionChanged
	}
	if op.Status != "unknown" || op.IntegrationID != r.IntegrationID || op.CampaignID != r.CampaignID || op.Marketplace != r.Marketplace {
		return ErrManagerAdResolutionChanged
	}
	if err = validate(ctx); err != nil {
		return err
	}

	body, err := json.Marshal(checks)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE manager_ad_manual_resolutions
 SET status='resolved_manually', confirmations=$5, resolved_at=GREATEST(statement_timestamp(), created_at)
 WHERE id=$1 AND operation_id=$2 AND workspace_id=$3 AND actor_id=$4
   AND status='awaiting_confirmation' AND expires_at>statement_timestamp()`,
		r.ID, r.OperationID, r.WorkspaceID, r.ActorID, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrManagerAdResolutionChanged
	}

	if _, err = tx.Exec(ctx, `DELETE FROM ad_native_resource_holds WHERE command_id=$1`, r.OperationID); err != nil {
		return err
	}
	opTag, err := tx.Exec(ctx, `UPDATE manager_ad_operations
 SET status='resolved_manually', message_code='resolved_manually', updated_at=statement_timestamp()
 WHERE id=$1 AND workspace_id=$2 AND status='unknown'`,
		r.OperationID, r.WorkspaceID)
	if err != nil {
		return err
	}
	if opTag.RowsAffected() != 1 {
		return ErrManagerAdResolutionChanged
	}
	return tx.Commit(ctx)
}
