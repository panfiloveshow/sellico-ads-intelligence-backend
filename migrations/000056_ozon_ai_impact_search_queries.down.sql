DROP TABLE IF EXISTS ozon_search_queries;

DROP INDEX IF EXISTS idx_ai_decisions_pending_impact;

ALTER TABLE ai_decisions
    DROP COLUMN IF EXISTS outcome_status,
    DROP COLUMN IF EXISTS drr_before,
    DROP COLUMN IF EXISTS drr_after,
    DROP COLUMN IF EXISTS spend_before_rub,
    DROP COLUMN IF EXISTS spend_after_rub,
    DROP COLUMN IF EXISTS revenue_before_rub,
    DROP COLUMN IF EXISTS revenue_after_rub,
    DROP COLUMN IF EXISTS evaluated_at;
