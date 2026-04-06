-- name: GetLatestCampaignStatsBatch :many
-- Returns the latest stat row per campaign for a given workspace.
SELECT DISTINCT ON (cs.campaign_id) cs.*
FROM campaign_stats cs
JOIN campaigns c ON c.id = cs.campaign_id
WHERE c.workspace_id = $1
ORDER BY cs.campaign_id, cs.date DESC;

-- name: GetLatestPhraseStatsBatch :many
-- Returns the latest stat row per phrase for a given workspace.
SELECT DISTINCT ON (ps.phrase_id) ps.*
FROM phrase_stats ps
JOIN phrases p ON p.id = ps.phrase_id
WHERE p.workspace_id = $1
ORDER BY ps.phrase_id, ps.date DESC;

-- name: GetLatestBidSnapshotsBatch :many
-- Returns the latest bid snapshot per phrase for a given workspace.
SELECT DISTINCT ON (bs.phrase_id) bs.*
FROM bid_snapshots bs
JOIN phrases p ON p.id = bs.phrase_id
WHERE p.workspace_id = $1
ORDER BY bs.phrase_id, bs.created_at DESC;
