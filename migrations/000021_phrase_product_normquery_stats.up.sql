ALTER TABLE phrases
    ADD COLUMN IF NOT EXISTS product_id UUID NULL REFERENCES products(id),
    ADD COLUMN IF NOT EXISTS wb_product_id BIGINT NULL;

DROP INDEX IF EXISTS idx_phrases_campaign_norm_query;
CREATE UNIQUE INDEX IF NOT EXISTS idx_phrases_campaign_product_norm_query
    ON phrases (campaign_id, wb_product_id, wb_norm_query);
CREATE INDEX IF NOT EXISTS idx_phrases_product_id ON phrases (product_id);
CREATE INDEX IF NOT EXISTS idx_phrases_wb_product_id ON phrases (wb_product_id);

ALTER TABLE phrase_stats
    ADD COLUMN IF NOT EXISTS atbs BIGINT NULL,
    ADD COLUMN IF NOT EXISTS orders BIGINT NULL,
    ADD COLUMN IF NOT EXISTS cpc DOUBLE PRECISION NULL,
    ADD COLUMN IF NOT EXISTS cpm DOUBLE PRECISION NULL,
    ADD COLUMN IF NOT EXISTS avg_pos DOUBLE PRECISION NULL;
