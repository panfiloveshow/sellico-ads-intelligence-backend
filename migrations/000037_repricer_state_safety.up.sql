-- Repricer state safety: only one active price write may exist for a cabinet good.
-- Existing duplicates are left untouched so the migration is non-destructive;
-- fail fast if they exist and require reconciliation before rollout.
CREATE UNIQUE INDEX price_changes_one_active_per_good
    ON price_changes (seller_cabinet_id, wb_product_id)
    WHERE wb_status IN ('pending', 'uploaded');

CREATE INDEX price_schedule_entries_stale_executing
    ON price_schedule_entries (updated_at)
    WHERE status = 'executing';

CREATE INDEX price_changes_stale_pending
    ON price_changes (updated_at)
    WHERE wb_status = 'pending' AND upload_task_id IS NULL;
