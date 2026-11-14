-- Refuse a rollback that would erase an uncertain or still-verifying write.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM ozon_price_write_attempts WHERE resource_key IS NOT NULL) THEN
        RAISE EXCEPTION 'reconcile active Ozon price attempts before rolling back';
    END IF;
END $$;

DROP TABLE ozon_price_write_attempts;
DROP INDEX idx_ozon_price_schedule_phase;
ALTER TABLE ozon_price_changes DROP CONSTRAINT ozon_price_changes_status_check;
ALTER TABLE ozon_price_changes ADD CONSTRAINT ozon_price_changes_status_check
    CHECK (status IN ('pending', 'applied', 'failed', 'rolled_back', 'shadow'));
