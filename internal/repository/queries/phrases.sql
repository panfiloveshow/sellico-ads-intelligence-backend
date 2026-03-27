-- name: CreatePhrase :one
INSERT INTO phrases (campaign_id, workspace_id, wb_cluster_id, keyword, count, current_bid)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPhraseByID :one
SELECT * FROM phrases WHERE id = $1;

-- name: UpsertPhrase :one
INSERT INTO phrases (campaign_id, workspace_id, wb_cluster_id, keyword, count, current_bid)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (wb_cluster_id, campaign_id) DO UPDATE SET
    keyword = EXCLUDED.keyword,
    count = EXCLUDED.count,
    current_bid = EXCLUDED.current_bid,
    updated_at = now()
RETURNING *;

-- name: ListPhrasesByCampaign :many
SELECT * FROM phrases
WHERE campaign_id = $1
ORDER BY keyword
LIMIT $2 OFFSET $3;

-- name: ListPhrasesByWorkspace :many
SELECT * FROM phrases
WHERE workspace_id = $1
ORDER BY keyword
LIMIT $2 OFFSET $3;
