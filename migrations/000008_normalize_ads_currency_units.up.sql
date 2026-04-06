UPDATE campaign_stats
SET
    spend = ROUND(spend / 100.0),
    revenue = CASE
        WHEN revenue IS NULL THEN NULL
        ELSE ROUND(revenue / 100.0)
    END;

UPDATE phrase_stats
SET spend = ROUND(spend / 100.0);
