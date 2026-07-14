DROP INDEX IF EXISTS price_schedule_entries_task_ids;
DROP INDEX IF EXISTS price_changes_schedule_entry;
DROP INDEX IF EXISTS price_changes_stale_submitting;
DROP INDEX IF EXISTS price_changes_submission_batch;
DROP INDEX IF EXISTS price_changes_pending_claim;
DROP INDEX IF EXISTS price_changes_one_transport_per_good;
DROP INDEX IF EXISTS price_changes_one_pending_per_good;

-- The previous state machine allowed only one pending/uploaded row. Preserve
-- the WB-bound row and terminalize a newer local pending row before restoring it.
UPDATE price_changes pending
SET wb_status = 'failed',
    error = 'queue migration rolled back while an older upload was active',
    updated_at = now()
WHERE pending.wb_status = 'pending'
  AND EXISTS (
    SELECT 1
    FROM price_changes active
    WHERE active.seller_cabinet_id = pending.seller_cabinet_id
      AND active.wb_product_id = pending.wb_product_id
      AND active.wb_status IN ('submitting', 'submit_unknown', 'uploaded')
  );

UPDATE price_changes
SET wb_status = 'pending', updated_at = now()
WHERE wb_status IN ('submitting', 'submit_unknown');

ALTER TABLE price_changes DROP COLUMN submission_batch_id;

CREATE UNIQUE INDEX price_changes_one_active_per_good
    ON price_changes (seller_cabinet_id, wb_product_id)
    WHERE wb_status IN ('pending', 'uploaded');
