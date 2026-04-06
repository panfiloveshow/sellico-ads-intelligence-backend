package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const createPositionTrackingTarget = `
INSERT INTO position_tracking_targets (
    workspace_id, product_id, query, region, is_active, baseline_position, baseline_checked_at
)
VALUES ($1, $2, $3, $4, TRUE, $5, $6)
ON CONFLICT (workspace_id, product_id, query, region)
DO UPDATE SET
    is_active = TRUE,
    updated_at = now()
RETURNING id, workspace_id, product_id, query, region, is_active, baseline_position, baseline_checked_at, created_at, updated_at
`

type CreatePositionTrackingTargetParams struct {
	WorkspaceID       pgtype.UUID        `json:"workspace_id"`
	ProductID         pgtype.UUID        `json:"product_id"`
	Query             string             `json:"query"`
	Region            string             `json:"region"`
	BaselinePosition  pgtype.Int4        `json:"baseline_position"`
	BaselineCheckedAt pgtype.Timestamptz `json:"baseline_checked_at"`
}

func (q *Queries) CreatePositionTrackingTarget(ctx context.Context, arg CreatePositionTrackingTargetParams) (PositionTrackingTarget, error) {
	row := q.db.QueryRow(ctx, createPositionTrackingTarget,
		arg.WorkspaceID,
		arg.ProductID,
		arg.Query,
		arg.Region,
		arg.BaselinePosition,
		arg.BaselineCheckedAt,
	)

	var item PositionTrackingTarget
	err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ProductID,
		&item.Query,
		&item.Region,
		&item.IsActive,
		&item.BaselinePosition,
		&item.BaselineCheckedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

const listPositionTrackingTargetsFiltered = `
SELECT id, workspace_id, product_id, query, region, is_active, baseline_position, baseline_checked_at, created_at, updated_at
FROM position_tracking_targets
WHERE workspace_id = $1
  AND ($4::uuid IS NULL OR product_id = $4::uuid)
  AND ($5::text IS NULL OR query = $5::text)
  AND ($6::text IS NULL OR region = $6::text)
  AND ($7::bool IS NULL OR is_active = $7::bool)
ORDER BY updated_at DESC, created_at DESC
LIMIT $2 OFFSET $3
`

type ListPositionTrackingTargetsFilteredParams struct {
	WorkspaceID     pgtype.UUID `json:"workspace_id"`
	Limit           int32       `json:"limit"`
	Offset          int32       `json:"offset"`
	ProductIDFilter pgtype.UUID `json:"product_id_filter"`
	QueryFilter     pgtype.Text `json:"query_filter"`
	RegionFilter    pgtype.Text `json:"region_filter"`
	ActiveFilter    pgtype.Bool `json:"active_filter"`
}

func (q *Queries) ListPositionTrackingTargetsFiltered(ctx context.Context, arg ListPositionTrackingTargetsFilteredParams) ([]PositionTrackingTarget, error) {
	rows, err := q.db.Query(ctx, listPositionTrackingTargetsFiltered,
		arg.WorkspaceID,
		arg.Limit,
		arg.Offset,
		arg.ProductIDFilter,
		arg.QueryFilter,
		arg.RegionFilter,
		arg.ActiveFilter,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PositionTrackingTarget{}
	for rows.Next() {
		var item PositionTrackingTarget
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.ProductID,
			&item.Query,
			&item.Region,
			&item.IsActive,
			&item.BaselinePosition,
			&item.BaselineCheckedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
