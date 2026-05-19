DROP INDEX IF EXISTS idx_phrases_campaign_norm_query;

UPDATE phrases
SET wb_cluster_id = 0
WHERE wb_cluster_id IS NULL;

ALTER TABLE phrases
    ALTER COLUMN wb_cluster_id SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_phrases_wb_cluster_campaign
    ON phrases (wb_cluster_id, campaign_id);

ALTER TABLE phrases
    DROP COLUMN IF EXISTS wb_norm_query;
