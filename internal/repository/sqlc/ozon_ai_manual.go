package sqlcgen

import (
	"context"
	"time"

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

// ExpireStaleAIRuns closes ai_runs left in 'running' by a worker that died
// mid-flight — a deploy restart is enough. Nothing ever reset them, so the row
// stayed 'running' forever and the UI kept showing «Выполняется» for a run that
// had been dead for days. Returns how many were closed.
func (q *Queries) ExpireStaleAIRuns(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx,
		`UPDATE ai_runs
		    SET status = 'failed',
		        error = COALESCE(error, 'Прогон прерван: воркер остановлен во время выполнения'),
		        finished_at = COALESCE(finished_at, now())
		  WHERE status = 'running' AND started_at < $1`, olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
