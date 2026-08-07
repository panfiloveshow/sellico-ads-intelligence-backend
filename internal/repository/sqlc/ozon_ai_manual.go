package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// GetLastCompletedAIRunAt returns the finished_at of the newest completed AI
// run for a cabinet (pgx.ErrNoRows when none). Used for the min-gap guard so
// deploy-driven startup kicks don't stack runs back to back.
func (q *Queries) GetLastCompletedAIRunAt(ctx context.Context, sellerCabinetID pgtype.UUID) (pgtype.Timestamptz, error) {
	row := q.db.QueryRow(ctx,
		`SELECT finished_at FROM ai_runs
		 WHERE seller_cabinet_id = $1 AND status = 'completed' AND finished_at IS NOT NULL
		 ORDER BY finished_at DESC LIMIT 1`, sellerCabinetID)
	var finishedAt pgtype.Timestamptz
	err := row.Scan(&finishedAt)
	return finishedAt, err
}
