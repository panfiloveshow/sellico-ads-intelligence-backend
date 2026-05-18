ALTER TABLE phrase_stats
    DROP COLUMN IF EXISTS avg_pos,
    DROP COLUMN IF EXISTS cpm,
    DROP COLUMN IF EXISTS cpc,
    DROP COLUMN IF EXISTS orders,
    DROP COLUMN IF EXISTS atbs;

DROP INDEX IF EXISTS idx_phrases_wb_product_id;
DROP INDEX IF EXISTS idx_phrases_product_id;
DROP INDEX IF EXISTS idx_phrases_campaign_product_norm_query;

CREATE UNIQUE INDEX IF NOT EXISTS idx_phrases_campaign_norm_query
    ON phrases (campaign_id, wb_norm_query);

ALTER TABLE phrases
    DROP COLUMN IF EXISTS wb_product_id,
    DROP COLUMN IF EXISTS product_id;
