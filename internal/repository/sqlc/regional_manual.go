package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type UpsertRegionalPositionAggregateParams struct {
	WorkspaceID   pgtype.UUID
	ProductID     pgtype.UUID
	Query         string
	Region        string
	AvgPosition   float64
	BestPosition  int32
	WorstPosition int32
	CheckCount    int32
	PeriodStart   pgtype.Date
	PeriodEnd     pgtype.Date
}

func (q *Queries) UpsertRegionalPositionAggregate(ctx context.Context, arg UpsertRegionalPositionAggregateParams) error {
	_, err := q.db.Exec(ctx,
		`INSERT INTO regional_position_aggregates (workspace_id, product_id, query, region, avg_position, best_position, worst_position, check_count, period_start, period_end)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (workspace_id, product_id, query, region, period_start) DO UPDATE SET
			avg_position = EXCLUDED.avg_position,
			best_position = EXCLUDED.best_position,
			worst_position = EXCLUDED.worst_position,
			check_count = EXCLUDED.check_count,
			period_end = EXCLUDED.period_end`,
		arg.WorkspaceID, arg.ProductID, arg.Query, arg.Region,
		arg.AvgPosition, arg.BestPosition, arg.WorstPosition, arg.CheckCount,
		arg.PeriodStart, arg.PeriodEnd)
	return err
}

type RegionalPositionAggregateRow struct {
	ID            pgtype.UUID        `json:"id"`
	WorkspaceID   pgtype.UUID        `json:"workspace_id"`
	ProductID     pgtype.UUID        `json:"product_id"`
	Query         string             `json:"query"`
	Region        string             `json:"region"`
	AvgPosition   float64            `json:"avg_position"`
	BestPosition  int32              `json:"best_position"`
	WorstPosition int32              `json:"worst_position"`
	CheckCount    int32              `json:"check_count"`
	PeriodStart   pgtype.Date        `json:"period_start"`
	PeriodEnd     pgtype.Date        `json:"period_end"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
}

func (q *Queries) ListRegionalAggregatesByProduct(ctx context.Context, productID pgtype.UUID, limit, offset int32) ([]RegionalPositionAggregateRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, workspace_id, product_id, query, region, avg_position, best_position, worst_position, check_count, period_start, period_end, created_at
		FROM regional_position_aggregates WHERE product_id = $1 ORDER BY avg_position ASC LIMIT $2 OFFSET $3`,
		productID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RegionalPositionAggregateRow
	for rows.Next() {
		var i RegionalPositionAggregateRow
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.Query, &i.Region, &i.AvgPosition, &i.BestPosition, &i.WorstPosition, &i.CheckCount, &i.PeriodStart, &i.PeriodEnd, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
