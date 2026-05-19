package sqlcgen

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const findActiveWBSyncJobRunByCabinet = `
SELECT id, workspace_id, task_type, status, started_at, finished_at, error_message, metadata, created_at
FROM job_runs
WHERE workspace_id = $1
  AND task_type = 'wb:sync_workspace'
  AND status IN ('pending', 'running')
  AND metadata->>'seller_cabinet_id' = $2
ORDER BY started_at DESC
LIMIT 1
`

func (q *Queries) FindActiveWBSyncJobRunByCabinet(ctx context.Context, workspaceID pgtype.UUID, cabinetID string) (JobRun, error) {
	row := q.db.QueryRow(ctx, findActiveWBSyncJobRunByCabinet, workspaceID, cabinetID)
	var i JobRun
	err := row.Scan(
		&i.ID,
		&i.WorkspaceID,
		&i.TaskType,
		&i.Status,
		&i.StartedAt,
		&i.FinishedAt,
		&i.ErrorMessage,
		&i.Metadata,
		&i.CreatedAt,
	)
	return i, err
}

const expireStaleJobRuns = `
UPDATE job_runs
SET status = 'expired',
    finished_at = now(),
    error_message = 'worker recovered stale running job',
    metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('result_state', 'failed', 'expired_by_worker', true)
WHERE status IN ('pending', 'running')
  AND started_at < $1
RETURNING id
`

func (q *Queries) ExpireStaleJobRuns(ctx context.Context, olderThan time.Time) (int64, error) {
	rows, err := q.db.Query(ctx, expireStaleJobRuns, pgtype.Timestamptz{Time: olderThan, Valid: true})
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int64
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil && err != pgx.ErrNoRows {
		return count, err
	}
	return count, nil
}
