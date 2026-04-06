-- name: CreateJobRun :one
INSERT INTO job_runs (workspace_id, task_type, status, started_at, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetJobRunByID :one
SELECT * FROM job_runs WHERE id = $1;

-- name: UpdateJobRunStatus :one
UPDATE job_runs
SET status = $2, finished_at = $3, error_message = $4, metadata = $5
WHERE id = $1
RETURNING *;

-- name: ListJobRunsByWorkspace :many
SELECT * FROM job_runs
WHERE workspace_id = $1
  AND (sqlc.narg('task_type_filter')::text IS NULL OR task_type = sqlc.narg('task_type_filter')::text)
  AND (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter')::text)
ORDER BY started_at DESC
LIMIT $2 OFFSET $3;

-- name: ListJobRunsByTaskType :many
SELECT * FROM job_runs
WHERE task_type = $1
ORDER BY started_at DESC
LIMIT $2 OFFSET $3;
