package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)




func (q *Queries) CountActiveRecommendationsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM recommendations WHERE workspace_id = $1 AND status = 'active'`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}



