-- name: CreateCampaignStat :one
INSERT INTO campaign_stats (campaign_id, date, impressions, clicks, spend, orders, revenue)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpsertCampaignStat :one
INSERT INTO campaign_stats (campaign_id, date, impressions, clicks, spend, orders, revenue)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (campaign_id, date) DO UPDATE SET
    impressions = EXCLUDED.impressions,
    clicks = EXCLUDED.clicks,
    spend = EXCLUDED.spend,
    orders = EXCLUDED.orders,
    revenue = EXCLUDED.revenue,
    updated_at = now()
RETURNING *;

-- name: GetCampaignStatsByDateRange :many
SELECT * FROM campaign_stats
WHERE campaign_id = $1 AND date BETWEEN $2 AND $3
ORDER BY date
LIMIT $4 OFFSET $5;

-- name: GetLatestCampaignStat :one
SELECT * FROM campaign_stats
WHERE campaign_id = $1
ORDER BY date DESC
LIMIT 1;

-- name: ListCampaignStatsByWorkspace :many
SELECT cs.* FROM campaign_stats cs
JOIN campaigns c ON c.id = cs.campaign_id
WHERE c.workspace_id = $1
ORDER BY cs.date DESC
LIMIT $2 OFFSET $3;
