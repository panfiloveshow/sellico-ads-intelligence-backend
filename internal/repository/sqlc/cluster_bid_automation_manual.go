package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// AutomationClusterSnapshot contains only authoritative, persisted WB cluster
// data. Revenue is deliberately absent: WB does not attribute revenue to a
// normquery, so callers must not infer it from product or campaign totals.
type AutomationClusterSnapshot struct {
	PhraseID            pgtype.UUID
	ProductID           pgtype.UUID
	WBProductID         int64
	NormQuery           string
	CurrentBid          int64
	BidObservedAt       pgtype.Timestamptz
	StatsObservedAt     pgtype.Timestamptz
	StatRows            int64
	Impressions         int64
	PreviousImpressions int64
	RecentImpressions   int64
	Clicks              int64
	Spend               int64
	Orders              int64
	OrdersKnown         bool
	AveragePosition     pgtype.Float8
}

type ListAutomationClusterSnapshotsParams struct {
	CampaignID pgtype.UUID
	ProductID  pgtype.UUID
	DateFrom   pgtype.Date
	DateTo     pgtype.Date
	RecentFrom pgtype.Date
	Limit      int32
}

type PhraseBidObservation struct {
	CampaignID  pgtype.UUID
	WorkspaceID pgtype.UUID
	ProductID   pgtype.UUID
	WBProductID pgtype.Int8
	NormQuery   string
	CurrentBid  pgtype.Int8
	ObservedAt  pgtype.Timestamptz
}

func (q *Queries) GetPhraseBidObservation(ctx context.Context, phraseID pgtype.UUID) (PhraseBidObservation, error) {
	row := q.db.QueryRow(ctx, `SELECT campaign_id, workspace_id, product_id, wb_product_id,
		wb_norm_query, current_bid, current_bid_observed_at
		FROM phrases WHERE id = $1`, phraseID)
	var observation PhraseBidObservation
	err := row.Scan(&observation.CampaignID, &observation.WorkspaceID, &observation.ProductID,
		&observation.WBProductID, &observation.NormQuery, &observation.CurrentBid, &observation.ObservedAt)
	return observation, err
}

// ListAutomationClusterSnapshots aggregates completed-day stats per exact
// (campaign, product, normquery) target. The optional product filter is the
// strategy binding target; a NULL filter intentionally means the whole campaign.
func (q *Queries) ListAutomationClusterSnapshots(ctx context.Context, arg ListAutomationClusterSnapshotsParams) ([]AutomationClusterSnapshot, error) {
	rows, err := q.db.Query(ctx, `
		SELECT p.id, p.product_id, p.wb_product_id, p.wb_norm_query,
		       p.current_bid, p.current_bid_observed_at, max(ps.updated_at), count(ps.id),
		       COALESCE(sum(ps.impressions), 0),
		       COALESCE(sum(ps.impressions) FILTER (WHERE ps.date < $5), 0),
		       COALESCE(sum(ps.impressions) FILTER (WHERE ps.date >= $5), 0),
		       COALESCE(sum(ps.clicks), 0), COALESCE(sum(ps.spend), 0),
		       COALESCE(sum(ps.orders), 0),
		       COALESCE(bool_and(ps.orders IS NOT NULL) FILTER (WHERE ps.id IS NOT NULL), false),
		       sum(ps.avg_pos * GREATEST(ps.impressions, 1))
		           / NULLIF(sum(GREATEST(ps.impressions, 1)) FILTER (WHERE ps.avg_pos IS NOT NULL), 0)
		FROM phrases p
		LEFT JOIN phrase_stats ps ON ps.phrase_id = p.id
		  AND ps.date BETWEEN $3 AND $4
		WHERE p.campaign_id = $1
		  AND ($2::uuid IS NULL OR p.product_id = $2)
		  AND p.product_id IS NOT NULL
		  AND p.wb_product_id > 0
		  AND length(btrim(p.wb_norm_query)) > 0
		  AND p.current_bid > 0
		GROUP BY p.id, p.product_id, p.wb_product_id, p.wb_norm_query, p.current_bid, p.updated_at
		ORDER BY p.wb_product_id, p.wb_norm_query
		LIMIT $6
	`, arg.CampaignID, arg.ProductID, arg.DateFrom, arg.DateTo, arg.RecentFrom, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AutomationClusterSnapshot, 0)
	for rows.Next() {
		var item AutomationClusterSnapshot
		if err := rows.Scan(
			&item.PhraseID, &item.ProductID, &item.WBProductID, &item.NormQuery,
			&item.CurrentBid, &item.BidObservedAt, &item.StatsObservedAt, &item.StatRows,
			&item.Impressions, &item.PreviousImpressions, &item.RecentImpressions,
			&item.Clicks, &item.Spend, &item.Orders, &item.OrdersKnown,
			&item.AveragePosition,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
