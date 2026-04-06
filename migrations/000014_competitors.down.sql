ALTER TABLE serp_result_items DROP COLUMN IF EXISTS promo_type;
ALTER TABLE serp_result_items DROP COLUMN IF EXISTS is_promoted;

DROP TABLE IF EXISTS competitor_snapshots;
DROP TABLE IF EXISTS competitors;
