package sqlcgen

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type ListCampaignStatsByWorkspaceDateRangeParams struct {
	WorkspaceID pgtype.UUID
	DateFrom    time.Time
	DateTo      time.Time
}

// ListCampaignStatsByWorkspaceDateRange returns campaign stats filtered by date range at SQL level.
// Uses explicit date casting to avoid timestamp/date comparison issues.
func (q *Queries) ListCampaignStatsByWorkspaceDateRange(ctx context.Context, arg ListCampaignStatsByWorkspaceDateRangeParams) ([]CampaignStat, error) {
	const query = `
		SELECT cs.id, cs.campaign_id, cs.date, cs.impressions, cs.clicks, cs.spend,
		       cs.orders, cs.revenue, cs.created_at, cs.updated_at
		FROM campaign_stats cs
		JOIN campaigns c ON c.id = cs.campaign_id
		WHERE c.workspace_id = $1
		  AND cs.date >= $2::date
		  AND cs.date <= $3::date
		ORDER BY cs.date DESC
	`
	rows, err := q.db.Query(ctx, query, arg.WorkspaceID, arg.DateFrom, arg.DateTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CampaignStat
	for rows.Next() {
		var i CampaignStat
		if err := rows.Scan(
			&i.ID, &i.CampaignID, &i.Date, &i.Impressions, &i.Clicks, &i.Spend,
			&i.Orders, &i.Revenue, &i.CreatedAt, &i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type ListPhraseStatsByWorkspaceDateRangeParams struct {
	WorkspaceID pgtype.UUID
	DateFrom    time.Time
	DateTo      time.Time
}

// ListPhraseStatsByWorkspaceDateRange returns phrase stats filtered by date range at SQL level.
func (q *Queries) ListPhraseStatsByWorkspaceDateRange(ctx context.Context, arg ListPhraseStatsByWorkspaceDateRangeParams) ([]PhraseStat, error) {
	const query = `
		SELECT ps.id, ps.phrase_id, ps.date, ps.impressions, ps.clicks, ps.spend,
		       ps.created_at, ps.updated_at
		FROM phrase_stats ps
		JOIN phrases p ON p.id = ps.phrase_id
		WHERE p.workspace_id = $1
		  AND ps.date >= $2::date
		  AND ps.date <= $3::date
		ORDER BY ps.date DESC
	`
	rows, err := q.db.Query(ctx, query, arg.WorkspaceID, arg.DateFrom, arg.DateTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PhraseStat
	for rows.Next() {
		var i PhraseStat
		if err := rows.Scan(
			&i.ID, &i.PhraseID, &i.Date, &i.Impressions, &i.Clicks, &i.Spend,
			&i.CreatedAt, &i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
