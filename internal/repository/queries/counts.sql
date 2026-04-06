-- name: CountCampaignsByWorkspace :one
SELECT COUNT(*) FROM campaigns WHERE workspace_id = $1;

-- name: CountPhrasesByWorkspace :one
SELECT COUNT(*) FROM phrases WHERE workspace_id = $1;

-- name: CountProductsByWorkspace :one
SELECT COUNT(*) FROM products WHERE workspace_id = $1;

-- name: CountRecommendationsByWorkspace :one
SELECT COUNT(*) FROM recommendations WHERE workspace_id = $1 AND status = 'active';

-- name: CountExportsByWorkspace :one
SELECT COUNT(*) FROM exports WHERE workspace_id = $1;

-- name: CountJobRunsByWorkspace :one
SELECT COUNT(*) FROM job_runs WHERE workspace_id = $1;

-- name: CountPositionsByWorkspace :one
SELECT COUNT(*) FROM positions WHERE workspace_id = $1;

-- name: CountBidSnapshotsByWorkspace :one
SELECT COUNT(*) FROM bid_snapshots bs
JOIN phrases p ON p.id = bs.phrase_id
WHERE p.workspace_id = $1;

-- name: CountAuditLogsByWorkspace :one
SELECT COUNT(*) FROM audit_logs WHERE workspace_id = $1;
