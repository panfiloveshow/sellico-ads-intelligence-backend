package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const getLatestCampaignStatsBatch = `
SELECT DISTINCT ON (cs.campaign_id)
  cs.id, cs.campaign_id, cs.date, cs.impressions, cs.clicks, cs.spend, cs.orders, cs.revenue, cs.created_at
FROM campaign_stats cs
JOIN campaigns c ON c.id = cs.campaign_id
WHERE c.workspace_id = $1
ORDER BY cs.campaign_id, cs.date DESC
`

func (q *Queries) GetLatestCampaignStatsBatch(ctx context.Context, workspaceID pgtype.UUID) ([]CampaignStat, error) {
	rows, err := q.db.Query(ctx, getLatestCampaignStatsBatch, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CampaignStat
	for rows.Next() {
		var i CampaignStat
		if err := rows.Scan(
			&i.ID, &i.CampaignID, &i.Date, &i.Impressions, &i.Clicks, &i.Spend, &i.Orders, &i.Revenue, &i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getLatestPhraseStatsBatch = `
SELECT DISTINCT ON (ps.phrase_id)
  ps.id, ps.phrase_id, ps.date, ps.impressions, ps.clicks, ps.spend, ps.created_at
FROM phrase_stats ps
JOIN phrases p ON p.id = ps.phrase_id
WHERE p.workspace_id = $1
ORDER BY ps.phrase_id, ps.date DESC
`

func (q *Queries) GetLatestPhraseStatsBatch(ctx context.Context, workspaceID pgtype.UUID) ([]PhraseStat, error) {
	rows, err := q.db.Query(ctx, getLatestPhraseStatsBatch, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PhraseStat
	for rows.Next() {
		var i PhraseStat
		if err := rows.Scan(
			&i.ID, &i.PhraseID, &i.Date, &i.Impressions, &i.Clicks, &i.Spend, &i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getLatestBidSnapshotsBatch = `
SELECT DISTINCT ON (bs.phrase_id)
  bs.id, bs.phrase_id, bs.competitive_bid, bs.leadership_bid, bs.cpm_min, bs.created_at
FROM bid_snapshots bs
JOIN phrases p ON p.id = bs.phrase_id
WHERE p.workspace_id = $1
ORDER BY bs.phrase_id, bs.created_at DESC
`

func (q *Queries) GetLatestBidSnapshotsBatch(ctx context.Context, workspaceID pgtype.UUID) ([]BidSnapshot, error) {
	rows, err := q.db.Query(ctx, getLatestBidSnapshotsBatch, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []BidSnapshot
	for rows.Next() {
		var i BidSnapshot
		if err := rows.Scan(
			&i.ID, &i.PhraseID, &i.CompetitiveBid, &i.LeadershipBid, &i.CpmMin, &i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
