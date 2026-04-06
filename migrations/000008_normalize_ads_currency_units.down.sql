UPDATE campaign_stats
SET
    spend = spend * 100,
    revenue = CASE
        WHEN revenue IS NULL THEN NULL
        ELSE revenue * 100
    END;

UPDATE phrase_stats
SET spend = spend * 100;
