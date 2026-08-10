-- Ozon module: weekly natural-language AI recap (manager-facing summary only,
-- no actions). One row per cabinet per ISO week — the generator guards against
-- duplicates on (seller_cabinet_id, period_start).
CREATE TABLE ozon_ai_weekly_reports (
    id                UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID          NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    period_start      DATE          NOT NULL,
    period_end        DATE          NOT NULL,
    drr_start         NUMERIC(8,2),
    drr_end           NUMERIC(8,2),
    text              TEXT          NOT NULL,
    generated_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, period_start)
);

CREATE INDEX idx_ozon_ai_weekly_reports_cabinet
    ON ozon_ai_weekly_reports (seller_cabinet_id, period_start DESC);
