package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

func (q *Queries) CountCampaignsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM campaigns WHERE workspace_id = $1`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountPhrasesByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM phrases WHERE workspace_id = $1`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountProductsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE workspace_id = $1`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountActiveRecommendationsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM recommendations WHERE workspace_id = $1 AND status = 'active'`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountExportsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM exports WHERE workspace_id = $1`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountJobRunsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM job_runs WHERE workspace_id = $1`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountAuditLogsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE workspace_id = $1`, workspaceID)
	var count int64
	err := row.Scan(&count)
	return count, err
}
