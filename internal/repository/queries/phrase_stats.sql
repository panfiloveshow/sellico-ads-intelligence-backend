-- name: CreatePhraseStat :one
INSERT INTO phrase_stats (phrase_id, date, impressions, clicks, spend)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpsertPhraseStat :one
INSERT INTO phrase_stats (phrase_id, date, impressions, clicks, spend)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (phrase_id, date) DO UPDATE SET
    impressions = EXCLUDED.impressions,
    clicks = EXCLUDED.clicks,
    spend = EXCLUDED.spend,
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
