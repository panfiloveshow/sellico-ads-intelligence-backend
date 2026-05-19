ALTER TABLE campaign_stats
    DROP COLUMN IF EXISTS canceled,
    DROP COLUMN IF EXISTS atbs;
