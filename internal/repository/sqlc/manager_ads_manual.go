package sqlcgen

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"math/big"
	"time"
)

// ManagerAdsCabinetByIntegration reads metadata only and rejects duplicate mappings.
func (q *Queries) ManagerAdsCabinetByIntegration(ctx context.Context, workspaceID uuid.UUID, integrationID string) (*domain.ManagerAdsCabinet, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, external_integration_id, marketplace, status, last_synced_at FROM seller_cabinets WHERE workspace_id=$1 AND external_integration_id=$2 AND deleted_at IS NULL LIMIT 2`, workspaceID, integrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result *domain.ManagerAdsCabinet
	for rows.Next() {
		if result != nil {
			return nil, errors.New("ambiguous manager cabinet mapping")
		}
		row := new(domain.ManagerAdsCabinet)
		if err := rows.Scan(&row.ID, &row.WorkspaceID, &row.IntegrationID, &row.Marketplace, &row.Status, &row.LastSyncedAt); err != nil {
			return nil, err
		}
		result = row
	}
	return result, rows.Err()
}

// Only the two constant schemas below can select identifiers. Dates and IDs are bound.
func (q *Queries) ManagerAdsCampaigns(ctx context.Context, cabinet domain.ManagerAdsCabinet, from, to time.Time, limit, offset int) ([]domain.ManagerAdsCampaign, int64, error) {
	campaigns, stats, external, name, status, impressions, spend, revenue, observed, tenant := "campaigns", "campaign_stats", "wb_campaign_id", "name", "status", "impressions", "spend", "revenue", "MAX(s.updated_at)", " AND c.workspace_id=$5"
	if cabinet.Marketplace == "ozon" {
		campaigns, stats, external, name, status, impressions, spend, revenue, observed, tenant = "ozon_campaigns", "ozon_campaign_stats", "ozon_campaign_id", "title", "state", "views", "spend_rub", "revenue_rub", "NULL::timestamptz", ""
	} else if cabinet.Marketplace != "wb" {
		return nil, 0, errors.New("unsupported manager marketplace")
	}
	var total int64
	countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM %s c WHERE c.seller_cabinet_id=$1`, campaigns)
	countArgs := []any{cabinet.ID}
	if cabinet.Marketplace == "wb" {
		countSQL += " AND c.workspace_id=$2"
		countArgs = append(countArgs, cabinet.WorkspaceID)
	}
	if err := q.db.QueryRow(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	sql := fmt.Sprintf(`SELECT c.id,c.%s::text,COALESCE(c.%s,''),COALESCE(c.%s,''),COUNT(s.id),%s,
 SUM(s.%s)::bigint,SUM(s.clicks)::bigint,SUM(s.%s)::text,
 CASE WHEN COUNT(s.id)>0 AND COUNT(s.orders)=COUNT(s.id) THEN SUM(s.orders)::bigint END,
 CASE WHEN COUNT(s.id)>0 AND COUNT(s.%s)=COUNT(s.id) THEN SUM(s.%s)::text END
 FROM %s c LEFT JOIN %s s ON s.campaign_id=c.id AND s.date BETWEEN $2 AND $3
 WHERE c.seller_cabinet_id=$1%s GROUP BY c.id ORDER BY c.%s,c.id LIMIT $4 OFFSET $%d`, external, name, status, observed, impressions, spend, revenue, revenue, campaigns, stats, tenant, external, map[bool]int{true: 6, false: 5}[cabinet.Marketplace == "wb"])
	args := []any{cabinet.ID, from, to, limit}
	if cabinet.Marketplace == "wb" {
		args = append(args, cabinet.WorkspaceID)
	}
	args = append(args, offset)
	rows, err := q.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]domain.ManagerAdsCampaign, 0)
	for rows.Next() {
		var item domain.ManagerAdsCampaign
		var views, clicks, orders *int64
		var spendValue, revenueValue *string
		if err := rows.Scan(&item.ID, &item.ExternalID, &item.Name, &item.Status, &item.ObservedDays, &item.DataUpdatedAt, &views, &clicks, &spendValue, &orders, &revenueValue); err != nil {
			return nil, 0, err
		}
		if item.ObservedDays > 0 {
			if views == nil || clicks == nil || spendValue == nil {
				return nil, 0, errors.New("incomplete observed manager metrics")
			}
			s, ok := new(big.Rat).SetString(*spendValue)
			if !ok {
				return nil, 0, errors.New("invalid observed manager spend")
			}
			metrics := &domain.ManagerAdsMetrics{Impressions: *views, Clicks: *clicks, SpendRub: s.FloatString(2), Orders: orders}
			metrics.CTRPercent = managerAdsRatio(new(big.Rat).SetInt64(*clicks), new(big.Rat).SetInt64(*views), 100)
			metrics.CPCRub = managerAdsRatio(s, new(big.Rat).SetInt64(*clicks), 1)
			if revenueValue != nil {
				r, ok := new(big.Rat).SetString(*revenueValue)
				if !ok {
					return nil, 0, errors.New("invalid observed manager revenue")
				}
				value := r.FloatString(2)
				metrics.RevenueRub = &value
				metrics.DRRPercent = managerAdsRatio(s, r, 100)
				metrics.ROAS = managerAdsRatio(r, s, 1)
			}
			item.Metrics = metrics
		}
		result = append(result, item)
	}
	return result, total, rows.Err()
}

func managerAdsRatio(numerator, denominator *big.Rat, multiplier int64) *string {
	if denominator.Sign() == 0 {
		return nil
	}
	value := new(big.Rat).Quo(numerator, denominator)
	value.Mul(value, new(big.Rat).SetInt64(multiplier))
	text := value.FloatString(4)
	return &text
}
