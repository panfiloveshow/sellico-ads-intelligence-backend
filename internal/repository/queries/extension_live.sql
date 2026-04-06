-- name: CreateExtensionPageContext :one
INSERT INTO extension_page_contexts (
    session_id, workspace_id, user_id, url, page_type,
    seller_cabinet_id, campaign_id, phrase_id, product_id,
    query, region, active_filters, metadata, dedupe_key, captured_at
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15
)
ON CONFLICT (workspace_id, dedupe_key) DO UPDATE
SET active_filters = COALESCE(EXCLUDED.active_filters, extension_page_contexts.active_filters),
    metadata = COALESCE(EXCLUDED.metadata, extension_page_contexts.metadata),
    captured_at = GREATEST(extension_page_contexts.captured_at, EXCLUDED.captured_at)
RETURNING *;

-- name: ListExtensionPageContextsFiltered :many
SELECT * FROM extension_page_contexts
WHERE workspace_id = $1
  AND (sqlc.narg('page_type_filter')::text IS NULL OR page_type = sqlc.narg('page_type_filter')::text)
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
  AND (sqlc.narg('phrase_id_filter')::uuid IS NULL OR phrase_id = sqlc.narg('phrase_id_filter')::uuid)
  AND (sqlc.narg('product_id_filter')::uuid IS NULL OR product_id = sqlc.narg('product_id_filter')::uuid)
  AND (sqlc.narg('query_filter')::text IS NULL OR query = sqlc.narg('query_filter')::text)
  AND (sqlc.narg('region_filter')::text IS NULL OR region = sqlc.narg('region_filter')::text)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateExtensionBidSnapshot :one
INSERT INTO extension_bid_snapshots (
    session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id,
    query, region, visible_bid, recommended_bid, competitive_bid, leadership_bid,
    cpm_min, source, confidence, metadata, dedupe_key, captured_at
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18
)
ON CONFLICT (workspace_id, dedupe_key) DO UPDATE
SET visible_bid = COALESCE(EXCLUDED.visible_bid, extension_bid_snapshots.visible_bid),
    recommended_bid = COALESCE(EXCLUDED.recommended_bid, extension_bid_snapshots.recommended_bid),
    competitive_bid = COALESCE(EXCLUDED.competitive_bid, extension_bid_snapshots.competitive_bid),
    leadership_bid = COALESCE(EXCLUDED.leadership_bid, extension_bid_snapshots.leadership_bid),
    cpm_min = COALESCE(EXCLUDED.cpm_min, extension_bid_snapshots.cpm_min),
    confidence = EXCLUDED.confidence,
    metadata = COALESCE(EXCLUDED.metadata, extension_bid_snapshots.metadata),
    captured_at = GREATEST(extension_bid_snapshots.captured_at, EXCLUDED.captured_at)
RETURNING *;

-- name: ListExtensionBidSnapshotsFiltered :many
SELECT * FROM extension_bid_snapshots
WHERE workspace_id = $1
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
  AND (sqlc.narg('phrase_id_filter')::uuid IS NULL OR phrase_id = sqlc.narg('phrase_id_filter')::uuid)
  AND (sqlc.narg('query_filter')::text IS NULL OR query = sqlc.narg('query_filter')::text)
  AND (sqlc.narg('region_filter')::text IS NULL OR region = sqlc.narg('region_filter')::text)
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR captured_at >= sqlc.narg('date_from')::timestamptz)
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR captured_at <= sqlc.narg('date_to')::timestamptz)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateExtensionPositionSnapshot :one
INSERT INTO extension_position_snapshots (
    session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id, product_id,
    query, region, visible_position, visible_page, page_subtype, source, confidence, metadata, dedupe_key, captured_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (workspace_id, dedupe_key) DO UPDATE
SET visible_position = EXCLUDED.visible_position,
    visible_page = COALESCE(EXCLUDED.visible_page, extension_position_snapshots.visible_page),
    page_subtype = COALESCE(EXCLUDED.page_subtype, extension_position_snapshots.page_subtype),
    confidence = EXCLUDED.confidence,
    metadata = COALESCE(EXCLUDED.metadata, extension_position_snapshots.metadata),
    captured_at = GREATEST(extension_position_snapshots.captured_at, EXCLUDED.captured_at)
RETURNING *;

-- name: ListExtensionPositionSnapshotsFiltered :many
SELECT * FROM extension_position_snapshots
WHERE workspace_id = $1
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
  AND (sqlc.narg('phrase_id_filter')::uuid IS NULL OR phrase_id = sqlc.narg('phrase_id_filter')::uuid)
  AND (sqlc.narg('product_id_filter')::uuid IS NULL OR product_id = sqlc.narg('product_id_filter')::uuid)
  AND (sqlc.narg('query_filter')::text IS NULL OR query = sqlc.narg('query_filter')::text)
  AND (sqlc.narg('region_filter')::text IS NULL OR region = sqlc.narg('region_filter')::text)
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR captured_at >= sqlc.narg('date_from')::timestamptz)
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR captured_at <= sqlc.narg('date_to')::timestamptz)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateExtensionUISignal :one
INSERT INTO extension_ui_signals (
    session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id, product_id,
    query, region, signal_type, severity, title, message, confidence, metadata, dedupe_key, captured_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (workspace_id, dedupe_key) DO UPDATE
SET severity = EXCLUDED.severity,
    title = EXCLUDED.title,
    message = COALESCE(EXCLUDED.message, extension_ui_signals.message),
    confidence = EXCLUDED.confidence,
    metadata = COALESCE(EXCLUDED.metadata, extension_ui_signals.metadata),
    captured_at = GREATEST(extension_ui_signals.captured_at, EXCLUDED.captured_at)
RETURNING *;

-- name: ListExtensionUISignalsFiltered :many
SELECT * FROM extension_ui_signals
WHERE workspace_id = $1
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
  AND (sqlc.narg('phrase_id_filter')::uuid IS NULL OR phrase_id = sqlc.narg('phrase_id_filter')::uuid)
  AND (sqlc.narg('product_id_filter')::uuid IS NULL OR product_id = sqlc.narg('product_id_filter')::uuid)
  AND (sqlc.narg('query_filter')::text IS NULL OR query = sqlc.narg('query_filter')::text)
  AND (sqlc.narg('region_filter')::text IS NULL OR region = sqlc.narg('region_filter')::text)
  AND (sqlc.narg('signal_type_filter')::text IS NULL OR signal_type = sqlc.narg('signal_type_filter')::text)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateExtensionNetworkCapture :one
INSERT INTO extension_network_captures (
    session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id, product_id,
    page_type, endpoint_key, query, region, payload, dedupe_key, captured_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (workspace_id, dedupe_key) DO UPDATE
SET payload = EXCLUDED.payload,
    captured_at = GREATEST(extension_network_captures.captured_at, EXCLUDED.captured_at)
RETURNING *;

-- name: ListExtensionNetworkCapturesFiltered :many
SELECT * FROM extension_network_captures
WHERE workspace_id = $1
  AND (sqlc.narg('page_type_filter')::text IS NULL OR page_type = sqlc.narg('page_type_filter')::text)
  AND (sqlc.narg('endpoint_key_filter')::text IS NULL OR endpoint_key = sqlc.narg('endpoint_key_filter')::text)
  AND (sqlc.narg('campaign_id_filter')::uuid IS NULL OR campaign_id = sqlc.narg('campaign_id_filter')::uuid)
  AND (sqlc.narg('phrase_id_filter')::uuid IS NULL OR phrase_id = sqlc.narg('phrase_id_filter')::uuid)
  AND (sqlc.narg('product_id_filter')::uuid IS NULL OR product_id = sqlc.narg('product_id_filter')::uuid)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;
