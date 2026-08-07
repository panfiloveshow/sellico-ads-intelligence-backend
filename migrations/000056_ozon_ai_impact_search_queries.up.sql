-- Ozon module: AI impact measurement + search query statistics.

-- A. «Эффект ИИ»: before/after windows measured around each applied decision.
--    outcome_status: NULL = never swept yet; 'pending_eval' = seen by the
--    sweep but the after-window has < 3 days of data; 'evaluated' = numbers
--    written; 'not_evaluable' = target has no campaign-level stats (cpo_*) or
--    the decision aged out (> 14 days) without enough data.
ALTER TABLE ai_decisions
    ADD COLUMN outcome_status     TEXT
        CHECK (outcome_status IN ('pending_eval', 'evaluated', 'not_evaluable')),
    ADD COLUMN drr_before         NUMERIC(8,2),
    ADD COLUMN drr_after          NUMERIC(8,2),
    ADD COLUMN spend_before_rub   NUMERIC(14,2),
    ADD COLUMN spend_after_rub    NUMERIC(14,2),
    ADD COLUMN revenue_before_rub NUMERIC(14,2),
    ADD COLUMN revenue_after_rub  NUMERIC(14,2),
    ADD COLUMN evaluated_at       TIMESTAMPTZ;

-- The impact sweep scans applied decisions that are not finally evaluated yet
-- (evaluated_at stays NULL for 'pending_eval' so the row is rescanned).
CREATE INDEX idx_ai_decisions_pending_impact
    ON ai_decisions (applied_at)
    WHERE status IN ('applied', 'auto_applied') AND evaluated_at IS NULL;

-- B. Search query statistics from the Performance API phrases report
--    (POST /api/client/statistics/phrases, async UUID flow). sku = 0 when the
--    report row carries no SKU (campaign-level phrase rows).
CREATE TABLE ozon_search_queries (
    id                UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID          NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku               BIGINT        NOT NULL DEFAULT 0,
    query             TEXT          NOT NULL,
    date              DATE          NOT NULL,
    views             BIGINT        NOT NULL DEFAULT 0,
    clicks            BIGINT        NOT NULL DEFAULT 0,
    orders            BIGINT        NOT NULL DEFAULT 0,
    spend_rub         NUMERIC(14,2) NOT NULL DEFAULT 0,
    avg_position      NUMERIC(8,2),
    UNIQUE (seller_cabinet_id, sku, query, date)
);

CREATE INDEX idx_ozon_search_queries_cabinet_date
    ON ozon_search_queries (seller_cabinet_id, date DESC);
CREATE INDEX idx_ozon_search_queries_cabinet_sku
    ON ozon_search_queries (seller_cabinet_id, sku);
