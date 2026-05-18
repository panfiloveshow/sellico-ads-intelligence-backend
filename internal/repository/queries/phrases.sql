-- name: CreatePhrase :one
INSERT INTO phrases (campaign_id, workspace_id, product_id, wb_product_id, wb_cluster_id, wb_norm_query, keyword, count, current_bid)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetPhraseByID :one
SELECT * FROM phrases WHERE id = $1;

-- name: UpsertPhrase :one
INSERT INTO phrases (campaign_id, workspace_id, product_id, wb_product_id, wb_cluster_id, wb_norm_query, keyword, count, current_bid)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (campaign_id, wb_product_id, wb_norm_query) DO UPDATE SET
    product_id = EXCLUDED.product_id,
    wb_cluster_id = EXCLUDED.wb_cluster_id,
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
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
ORDER BY keyword
LIMIT $2 OFFSET $3;
