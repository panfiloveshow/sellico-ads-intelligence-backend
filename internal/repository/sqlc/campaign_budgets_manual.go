package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type CampaignBudget struct {
	ID         pgtype.UUID        `json:"id"`
	CampaignID pgtype.UUID        `json:"campaign_id"`
	Cash       int64              `json:"cash"`
	Netting    int64              `json:"netting"`
	Total      int64              `json:"total"`
	CapturedAt pgtype.Timestamptz `json:"captured_at"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
	UpdatedAt  pgtype.Timestamptz `json:"updated_at"`
}

type UpsertCampaignBudgetParams struct {
	CampaignID pgtype.UUID        `json:"campaign_id"`
	Cash       int64              `json:"cash"`
	Netting    int64              `json:"netting"`
	Total      int64              `json:"total"`
	CapturedAt pgtype.Timestamptz `json:"captured_at"`
}

const upsertCampaignBudget = `
INSERT INTO campaign_budgets (campaign_id, cash, netting, total, captured_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (campaign_id, captured_at) DO UPDATE
SET cash = EXCLUDED.cash,
    netting = EXCLUDED.netting,
    total = EXCLUDED.total,
    updated_at = now()
RETURNING id, campaign_id, cash, netting, total, captured_at, created_at, updated_at
`

func (q *Queries) UpsertCampaignBudget(ctx context.Context, arg UpsertCampaignBudgetParams) (CampaignBudget, error) {
	row := q.db.QueryRow(ctx, upsertCampaignBudget, arg.CampaignID, arg.Cash, arg.Netting, arg.Total, arg.CapturedAt)
	var i CampaignBudget
	err := row.Scan(
		&i.ID,
		&i.CampaignID,
		&i.Cash,
		&i.Netting,
		&i.Total,
		&i.CapturedAt,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listLatestCampaignBudgetsByWorkspace = `
SELECT DISTINCT ON (cb.campaign_id)
  cb.id, cb.campaign_id, cb.cash, cb.netting, cb.total, cb.captured_at, cb.created_at, cb.updated_at
FROM campaign_budgets cb
JOIN campaigns c ON c.id = cb.campaign_id
WHERE c.workspace_id = $1
ORDER BY cb.campaign_id, cb.captured_at DESC
`

func (q *Queries) ListLatestCampaignBudgetsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]CampaignBudget, error) {
	rows, err := q.db.Query(ctx, listLatestCampaignBudgetsByWorkspace, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CampaignBudget
	for rows.Next() {
		var i CampaignBudget
		if err := rows.Scan(
			&i.ID,
			&i.CampaignID,
			&i.Cash,
			&i.Netting,
			&i.Total,
			&i.CapturedAt,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
