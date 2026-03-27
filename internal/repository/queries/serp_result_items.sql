-- name: CreateSERPResultItem :one
INSERT INTO serp_result_items (snapshot_id, position, wb_product_id, title, price, rating, reviews_count)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListSERPResultItemsBySnapshot :many
SELECT * FROM serp_result_items
WHERE snapshot_id = $1
ORDER BY position;

-- name: BatchCreateSERPResultItems :copyfrom
INSERT INTO serp_result_items (snapshot_id, position, wb_product_id, title, price, rating, reviews_count) VALUES ($1, $2, $3, $4, $5, $6, $7);
