-- Ozon Performance API introduces hourly and daily request quotas on
-- 2026-08-25 (campaign create/copy, adding products, budget and bid changes,
-- report generation). The client already paces requests per second, but a
-- per-second limiter says nothing about an hourly or daily allowance.
--
-- Hourly buckets: the hourly usage is one row, the daily usage is the sum of
-- the last 24. One table answers both questions.
CREATE TABLE ozon_api_call_counters (
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    category          TEXT        NOT NULL,
    hour_start        TIMESTAMPTZ NOT NULL,
    calls             INTEGER     NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (seller_cabinet_id, category, hour_start)
);

COMMENT ON COLUMN ozon_api_call_counters.category IS
    'bid_write | budget_write | campaign_write | product_write | report — mirrors the operation groups Ozon meters separately';
COMMENT ON COLUMN ozon_api_call_counters.hour_start IS
    'UTC hour bucket the calls landed in';

-- Counters older than a couple of days are only useful for reporting; the
-- index keeps both the rolling-window sum and the cleanup scan cheap.
CREATE INDEX idx_ozon_api_call_counters_window
    ON ozon_api_call_counters (seller_cabinet_id, category, hour_start DESC);
