package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type SellerCabinetSyncState struct {
	SellerCabinetID   pgtype.UUID
	WorkspaceID       pgtype.UUID
	Status            string
	StartedAt         pgtype.Timestamptz
	CompletedAt       pgtype.Timestamptz
	DataThroughDate   pgtype.Date
	IssueCount        int32
	WBErrorCount      int32
	RateLimited       bool
	RetryAfterSeconds pgtype.Int4
	LastError         pgtype.Text
	UpdatedAt         pgtype.Timestamptz
}

type BeginSellerCabinetSyncParams struct {
	SellerCabinetID pgtype.UUID
	WorkspaceID     pgtype.UUID
	StartedAt       pgtype.Timestamptz
}

func (q *Queries) BeginSellerCabinetSync(ctx context.Context, arg BeginSellerCabinetSyncParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO seller_cabinet_sync_states (seller_cabinet_id, workspace_id, status, started_at, completed_at, data_through_date, issue_count, wb_error_count, rate_limited, retry_after_seconds, last_error)
VALUES ($1, $2, 'running', $3, NULL, NULL, 0, 0, FALSE, NULL, NULL)
ON CONFLICT (seller_cabinet_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  status = 'running',
  started_at = EXCLUDED.started_at,
  completed_at = NULL,
  data_through_date = NULL,
  issue_count = 0,
  wb_error_count = 0,
  rate_limited = FALSE,
  retry_after_seconds = NULL,
  last_error = NULL,
  updated_at = NOW()`, arg.SellerCabinetID, arg.WorkspaceID, arg.StartedAt)
	return err
}

type CompleteSellerCabinetSyncParams struct {
	SellerCabinetID   pgtype.UUID
	WorkspaceID       pgtype.UUID
	Status            string
	CompletedAt       pgtype.Timestamptz
	DataThroughDate   pgtype.Date
	IssueCount        int32
	WBErrorCount      int32
	RateLimited       bool
	RetryAfterSeconds pgtype.Int4
	LastError         pgtype.Text
}

func (q *Queries) CompleteSellerCabinetSync(ctx context.Context, arg CompleteSellerCabinetSyncParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO seller_cabinet_sync_states (
  seller_cabinet_id, workspace_id, status, started_at, completed_at,
  data_through_date, issue_count, wb_error_count, rate_limited,
  retry_after_seconds, last_error
)
VALUES ($1, $2, $3, $4, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (seller_cabinet_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  status = EXCLUDED.status,
  completed_at = EXCLUDED.completed_at,
  data_through_date = EXCLUDED.data_through_date,
  issue_count = EXCLUDED.issue_count,
  wb_error_count = EXCLUDED.wb_error_count,
  rate_limited = EXCLUDED.rate_limited,
  retry_after_seconds = EXCLUDED.retry_after_seconds,
  last_error = EXCLUDED.last_error,
  updated_at = NOW()
	`, arg.SellerCabinetID, arg.WorkspaceID, arg.Status, arg.CompletedAt,
		arg.DataThroughDate, arg.IssueCount, arg.WBErrorCount, arg.RateLimited,
		arg.RetryAfterSeconds, arg.LastError)
	return err
}

func (q *Queries) GetSellerCabinetSyncState(ctx context.Context, sellerCabinetID pgtype.UUID) (SellerCabinetSyncState, error) {
	row := q.db.QueryRow(ctx, `
SELECT seller_cabinet_id, workspace_id, status, started_at, completed_at, data_through_date,
       issue_count, wb_error_count, rate_limited, retry_after_seconds, last_error, updated_at
FROM seller_cabinet_sync_states
WHERE seller_cabinet_id = $1`, sellerCabinetID)
	var state SellerCabinetSyncState
	err := row.Scan(&state.SellerCabinetID, &state.WorkspaceID, &state.Status, &state.StartedAt,
		&state.CompletedAt, &state.DataThroughDate, &state.IssueCount, &state.WBErrorCount,
		&state.RateLimited, &state.RetryAfterSeconds, &state.LastError, &state.UpdatedAt)
	return state, err
}

func (q *Queries) ListSellerCabinetSyncStatesByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]SellerCabinetSyncState, error) {
	rows, err := q.db.Query(ctx, `
SELECT seller_cabinet_id, workspace_id, status, started_at, completed_at, data_through_date,
       issue_count, wb_error_count, rate_limited, retry_after_seconds, last_error, updated_at
FROM seller_cabinet_sync_states
WHERE workspace_id = $1
ORDER BY seller_cabinet_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []SellerCabinetSyncState
	for rows.Next() {
		var state SellerCabinetSyncState
		if err := rows.Scan(&state.SellerCabinetID, &state.WorkspaceID, &state.Status, &state.StartedAt,
			&state.CompletedAt, &state.DataThroughDate, &state.IssueCount, &state.WBErrorCount,
			&state.RateLimited, &state.RetryAfterSeconds, &state.LastError, &state.UpdatedAt); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}
