-- name: CreateRecommendation :one
INSERT INTO recommendations (workspace_id, campaign_id, phrase_id, product_id, title, description, type, severity, confidence, source_metrics, next_action)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetRecommendationByID :one
SELECT * FROM recommendations WHERE id = $1;

-- name: ListRecommendationsByWorkspace :many
SELECT * FROM recommendations
WHERE workspace_id = $1
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
  AND (sqlc.narg('phrase_id_filter')::uuid IS NULL OR phrase_id = sqlc.narg('phrase_id_filter')::uuid)
  AND (sqlc.narg('product_id_filter')::uuid IS NULL OR product_id = sqlc.narg('product_id_filter')::uuid)
  AND (sqlc.narg('type_filter')::text IS NULL OR type = sqlc.narg('type_filter')::text)
  AND (sqlc.narg('severity_filter')::text IS NULL OR severity = sqlc.narg('severity_filter')::text)
  AND (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter')::text)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateRecommendationStatus :one
UPDATE recommendations
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateRecommendationContent :one
UPDATE recommendations
SET title = $2,
    description = $3,
    severity = $4,
    confidence = $5,
    source_metrics = $6,
    next_action = $7,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetActiveRecommendation :one
SELECT * FROM recommendations
WHERE workspace_id = $1
  AND type = $2
  AND status = 'active'
  AND (
    (sqlc.narg('campaign_id_filter')::uuid IS NOT NULL AND campaign_id = sqlc.narg('campaign_id_filter')::uuid)
    OR (sqlc.narg('phrase_id_filter')::uuid IS NOT NULL AND phrase_id = sqlc.narg('phrase_id_filter')::uuid)
    OR (sqlc.narg('product_id_filter')::uuid IS NOT NULL AND product_id = sqlc.narg('product_id_filter')::uuid)
  )
LIMIT 1;
