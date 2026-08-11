ALTER TABLE ai_decisions
    DROP COLUMN IF EXISTS total_drr_before,
    DROP COLUMN IF EXISTS total_drr_after;
