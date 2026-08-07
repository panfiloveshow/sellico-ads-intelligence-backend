-- Ozon module phase 5 queries (price calendar with auto-revert + repricer
-- health summary).

-- name: CreateOzonPriceScheduleEntry :one
INSERT INTO ozon_price_schedule_entries (
    seller_cabinet_id, sku, offer_id, scheduled_price_rub, revert_price_rub,
    starts_at, ends_at
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('sku'), sqlc.narg('offer_id'),
    sqlc.arg('scheduled_price_rub'), sqlc.narg('revert_price_rub'),
    sqlc.arg('starts_at'), sqlc.narg('ends_at')
)
RETURNING *;

-- name: GetOzonPriceScheduleEntry :one
SELECT * FROM ozon_price_schedule_entries WHERE id = $1;

-- name: ListOzonPriceScheduleEntriesByCabinet :many
SELECT * FROM ozon_price_schedule_entries
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY starts_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountOzonPriceScheduleEntriesByCabinet :one
SELECT COUNT(*) FROM ozon_price_schedule_entries
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- MarkOzonPriceScheduleResult finalizes one entry: 'applied' stamps
-- applied_at, 'reverted' stamps reverted_at, 'failed' records the error.
-- name: MarkOzonPriceScheduleResult :one
UPDATE ozon_price_schedule_entries
SET status = sqlc.arg('status'),
    error = sqlc.narg('error'),
    applied_at = CASE WHEN sqlc.arg('status')::text = 'applied' THEN now() ELSE applied_at END,
    reverted_at = CASE WHEN sqlc.arg('status')::text = 'reverted' THEN now() ELSE reverted_at END
WHERE id = sqlc.arg('id')
RETURNING *;

-- CancelOzonPriceScheduleEntry cancels a pending entry (only pending rows
-- can be cancelled; the WHERE clause makes the transition race-safe).
-- name: CancelOzonPriceScheduleEntry :one
UPDATE ozon_price_schedule_entries
SET status = 'cancelled'
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- ListDueOzonPriceSchedules returns pending entries whose start time has
-- passed, across all cabinets (the 15m executor task runs globally).
-- name: ListDueOzonPriceSchedules :many
SELECT * FROM ozon_price_schedule_entries
WHERE status = 'pending' AND starts_at <= sqlc.arg('now')
ORDER BY starts_at
LIMIT sqlc.arg('limit');

-- ListDueOzonPriceScheduleReverts returns applied entries whose end time has
-- passed and that carry a revert price to restore.
-- name: ListDueOzonPriceScheduleReverts :many
SELECT * FROM ozon_price_schedule_entries
WHERE status = 'applied'
  AND ends_at IS NOT NULL AND ends_at <= sqlc.arg('now')
  AND revert_price_rub IS NOT NULL
ORDER BY ends_at
LIMIT sqlc.arg('limit');

-- name: CountPendingOzonPriceSchedulesByCabinet :one
SELECT COUNT(*) FROM ozon_price_schedule_entries
WHERE seller_cabinet_id = $1 AND status = 'pending';

-- OzonPriceChanges24hSummary feeds the repricer health endpoint.
-- name: OzonPriceChanges24hSummary :one
SELECT
    COUNT(*) FILTER (WHERE status = 'applied')::bigint AS applied,
    COUNT(*) FILTER (WHERE status = 'shadow')::bigint  AS shadow,
    COUNT(*) FILTER (WHERE status = 'failed')::bigint  AS failed
FROM ozon_price_changes
WHERE seller_cabinet_id = $1 AND created_at > now() - interval '24 hours';

-- LastOzonStrategySweepAt: when the strategy sweep last recorded a decision
-- for this cabinet (max created_at over source='strategy' rows).
-- name: LastOzonStrategySweepAt :one
SELECT max(created_at)::timestamptz AS last_sweep_at
FROM ozon_price_changes
WHERE seller_cabinet_id = $1 AND source = 'strategy';
