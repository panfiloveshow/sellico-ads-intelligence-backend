package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// OzonDRRWindowEvidence describes actual persisted rows for the same closed
// calendar window on both sides of the ratio. Absence is not evidence of zero.
// Daily coverage cannot prove the upstream API returned every SKU, but it
// detects missing dates and unreported campaigns instead of hiding them in SUM.
// Missing daily rows are checked for running/unknown-state campaigns and for
// campaigns observed in the requested window. Old inactive campaigns with no
// window activity are not required to have invented zero rows. Their stored
// spend is still included whenever present. This is observed window coverage,
// not an upstream report-completeness manifest.
type OzonDRRWindowEvidence struct {
	SpendRub            pgtype.Numeric
	RevenueRub          pgtype.Numeric
	OrderedUnits        int64
	SalesDays           int64
	AdDays              int64
	SalesLastDate       pgtype.Date
	AdLastDate          pgtype.Date
	MissingCampaignDays int64
	InvalidRows         int64
}

func (q *Queries) GetOzonDRRWindowEvidence(ctx context.Context, cabinetID pgtype.UUID, from, to pgtype.Date) (OzonDRRWindowEvidence, error) {
	var result OzonDRRWindowEvidence
	err := q.db.QueryRow(ctx, `WITH sales AS (
	 SELECT COALESCE(SUM(revenue_rub),0)::numeric AS revenue,
	        COALESCE(SUM(ordered_units),0)::bigint AS orders,
	        COUNT(DISTINCT date)::bigint AS days, MAX(date) AS last_date,
	        COUNT(*) FILTER (WHERE revenue_rub IS NULL OR revenue_rub < 0 OR ordered_units < 0)::bigint AS invalid
	 FROM ozon_sales_daily WHERE seller_cabinet_id=$1 AND date BETWEEN $2::date AND $3::date
	), ads AS (
	 SELECT COALESCE(SUM(s.spend_rub),0)::numeric AS spend,
	        COUNT(DISTINCT s.date)::bigint AS days, MAX(s.date) AS last_date,
	        COUNT(*) FILTER (WHERE s.spend_rub < 0)::bigint AS invalid
	 FROM ozon_campaign_stats s JOIN ozon_campaigns c ON c.id=s.campaign_id
	 WHERE c.seller_cabinet_id=$1 AND s.date BETWEEN $2::date AND $3::date
	), missing AS (
	 SELECT COUNT(*)::bigint AS days FROM ozon_campaigns c
	 CROSS JOIN generate_series($2::date,$3::date,interval '1 day') d(day)
	 WHERE c.seller_cabinet_id=$1
	   AND (COALESCE(UPPER(TRIM(c.state)),'') NOT IN (
	     'CAMPAIGN_STATE_INACTIVE','CAMPAIGN_STATE_STOPPED','CAMPAIGN_STATE_ARCHIVED',
	     'CAMPAIGN_STATE_FINISHED','CAMPAIGN_STATE_PLANNED','CAMPAIGN_STATE_MODERATION_PASSED')
	     OR EXISTS (SELECT 1 FROM ozon_campaign_stats observed
	       WHERE observed.campaign_id=c.id AND observed.date BETWEEN $2::date AND $3::date))
	   AND (c.from_date IS NULL OR c.from_date<=d.day::date)
	   AND (c.to_date IS NULL OR c.to_date>=d.day::date)
	   AND NOT EXISTS (SELECT 1 FROM ozon_campaign_stats s WHERE s.campaign_id=c.id AND s.date=d.day::date)
	)
	SELECT ads.spend,sales.revenue,sales.orders,sales.days,ads.days,
	       sales.last_date,ads.last_date,missing.days,sales.invalid+ads.invalid
	FROM sales CROSS JOIN ads CROSS JOIN missing`, cabinetID, from, to).Scan(
		&result.SpendRub, &result.RevenueRub, &result.OrderedUnits,
		&result.SalesDays, &result.AdDays, &result.SalesLastDate, &result.AdLastDate,
		&result.MissingCampaignDays, &result.InvalidRows,
	)
	return result, err
}

// OzonCampaignAttributedTurnoverWindow bounds both the turnover and the
// campaign-spend weights to the same closed window. Future and partial-current
// day rows must not influence a ratio whose numerator uses completed days.
func (q *Queries) OzonCampaignAttributedTurnoverWindow(ctx context.Context, cabinetID pgtype.UUID, from, to pgtype.Date) ([]OzonCampaignAttributedTurnoverByCabinetRow, error) {
	rows, err := q.db.Query(ctx, `WITH campaign_spend AS (
	 SELECT c.id AS campaign_id, COALESCE(SUM(s.spend_rub),0)::numeric AS spend_rub
	 FROM ozon_campaigns c LEFT JOIN ozon_campaign_stats s
	 ON s.campaign_id=c.id AND s.date BETWEEN $2::date AND $3::date
	 WHERE c.seller_cabinet_id=$1 GROUP BY c.id
	), sku_spend AS (
	 SELECT cp.sku,SUM(cs.spend_rub)::numeric AS spend_rub
	 FROM ozon_campaign_products cp JOIN campaign_spend cs ON cs.campaign_id=cp.campaign_id
	 GROUP BY cp.sku
	), sku_turnover AS (
	 SELECT sku,COALESCE(SUM(revenue_rub),0)::numeric AS revenue_rub,MAX(date) AS last_date
	 FROM ozon_sales_daily WHERE seller_cabinet_id=$1 AND date BETWEEN $2::date AND $3::date
	 GROUP BY sku
	)
	SELECT cp.campaign_id,
	 COALESCE(SUM(st.revenue_rub*cs.spend_rub/NULLIF(ss.spend_rub,0)),0)::numeric AS revenue_rub,
	 COALESCE(BOOL_OR(ss.spend_rub>cs.spend_rub),FALSE)::boolean AS revenue_shared,
	 MAX(st.last_date)::date AS last_date
	FROM ozon_campaign_products cp
	JOIN campaign_spend cs ON cs.campaign_id=cp.campaign_id
	JOIN sku_spend ss ON ss.sku=cp.sku
	JOIN sku_turnover st ON st.sku=cp.sku
	GROUP BY cp.campaign_id`, cabinetID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OzonCampaignAttributedTurnoverByCabinetRow
	for rows.Next() {
		var row OzonCampaignAttributedTurnoverByCabinetRow
		if err := rows.Scan(&row.CampaignID, &row.RevenueRub, &row.RevenueShared, &row.LastDate); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// AggregateOzonCampaignStatsWindow keeps deterministic campaign decisions on
// the same completed days as their cabinet and attributed-turnover guardrails.
func (q *Queries) AggregateOzonCampaignStatsWindow(ctx context.Context, campaignID pgtype.UUID, from, to pgtype.Date) (AggregateOzonCampaignStatsSinceRow, error) {
	var result AggregateOzonCampaignStatsSinceRow
	err := q.db.QueryRow(ctx, `SELECT COALESCE(SUM(views),0)::bigint,
	 COALESCE(SUM(clicks),0)::bigint,COALESCE(SUM(spend_rub),0)::numeric,
	 COALESCE(SUM(orders),0)::bigint,COALESCE(SUM(revenue_rub),0)::numeric
	 FROM ozon_campaign_stats WHERE campaign_id=$1 AND date BETWEEN $2::date AND $3::date`,
		campaignID, from, to).Scan(&result.Views, &result.Clicks, &result.SpendRub, &result.Orders, &result.RevenueRub)
	return result, err
}
