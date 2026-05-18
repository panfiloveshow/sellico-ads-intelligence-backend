-- name: CreatePhraseStat :one
INSERT INTO phrase_stats (phrase_id, date, impressions, clicks, spend, atbs, orders, cpc, cpm, avg_pos)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpsertPhraseStat :one
INSERT INTO phrase_stats (phrase_id, date, impressions, clicks, spend, atbs, orders, cpc, cpm, avg_pos)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (phrase_id, date) DO UPDATE SET
    impressions = EXCLUDED.impressions,
    clicks = EXCLUDED.clicks,
    spend = EXCLUDED.spend,
    atbs = EXCLUDED.atbs,
    orders = EXCLUDED.orders,
    cpc = EXCLUDED.cpc,
    cpm = EXCLUDED.cpm,
    avg_pos = EXCLUDED.avg_pos,
    updated_at = now()
RETURNING *;

-- name: GetPhraseStatsByDateRange :many
SELECT * FROM phrase_stats
WHERE phrase_id = $1 AND date BETWEEN $2 AND $3
ORDER BY date
LIMIT $4 OFFSET $5;

-- name: GetLatestPhraseStat :one
SELECT * FROM phrase_stats
WHERE phrase_id = $1
ORDER BY date DESC
LIMIT 1;

-- name: ListPhraseStatsByWorkspace :many
SELECT ps.* FROM phrase_stats ps
JOIN phrases p ON p.id = ps.phrase_id
WHERE p.workspace_id = $1
ORDER BY ps.date DESC
LIMIT $2 OFFSET $3;
