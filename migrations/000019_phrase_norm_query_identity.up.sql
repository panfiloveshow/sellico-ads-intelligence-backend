ALTER TABLE phrases
    ADD COLUMN IF NOT EXISTS wb_norm_query TEXT;

UPDATE phrases
SET wb_norm_query = keyword
WHERE wb_norm_query IS NULL;

ALTER TABLE phrases
    ALTER COLUMN wb_norm_query SET NOT NULL;

DROP INDEX IF EXISTS idx_phrases_wb_cluster_campaign;

ALTER TABLE phrases
    ALTER COLUMN wb_cluster_id DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_phrases_campaign_norm_query
    ON phrases (campaign_id, wb_norm_query);
