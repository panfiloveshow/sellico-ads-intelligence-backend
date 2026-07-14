-- Latest-intent price queue. A good may have one WB-bound write and one newer
-- local intent waiting behind it.
-- Deploy with repricer workers stopped: the previous binary does not know the
-- transport states introduced by this migration.
ALTER TABLE price_changes ADD COLUMN submission_batch_id UUID NULL;

DROP INDEX IF EXISTS price_changes_one_active_per_good;

CREATE UNIQUE INDEX price_changes_one_pending_per_good
    ON price_changes (seller_cabinet_id, wb_product_id)
    WHERE wb_status = 'pending';

CREATE UNIQUE INDEX price_changes_one_transport_per_good
    ON price_changes (seller_cabinet_id, wb_product_id)
    WHERE wb_status IN ('submitting', 'submit_unknown', 'uploaded');

CREATE INDEX price_changes_pending_claim
    ON price_changes (workspace_id, seller_cabinet_id, created_at)
    WHERE wb_status = 'pending';

CREATE INDEX price_changes_stale_submitting
    ON price_changes (workspace_id, updated_at)
    WHERE wb_status IN ('submitting', 'submit_unknown') AND upload_task_id IS NULL;

CREATE INDEX price_changes_submission_batch
    ON price_changes (submission_batch_id)
    WHERE submission_batch_id IS NOT NULL AND upload_task_id IS NULL;

CREATE INDEX price_changes_schedule_entry
    ON price_changes (schedule_entry_id)
    WHERE schedule_entry_id IS NOT NULL;

CREATE INDEX price_schedule_entries_task_ids
    ON price_schedule_entries USING gin (executed_task_ids);
