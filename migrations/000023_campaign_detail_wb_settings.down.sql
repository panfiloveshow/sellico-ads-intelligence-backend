ALTER TABLE campaign_products
    DROP COLUMN IF EXISTS bid_recommendations,
    DROP COLUMN IF EXISTS bid_search,
    DROP COLUMN IF EXISTS subject_name;

ALTER TABLE campaigns
    DROP COLUMN IF EXISTS wb_deleted_at,
    DROP COLUMN IF EXISTS wb_updated_at,
    DROP COLUMN IF EXISTS wb_started_at,
    DROP COLUMN IF EXISTS wb_created_at,
    DROP COLUMN IF EXISTS placement_recommendations,
    DROP COLUMN IF EXISTS placement_search;
