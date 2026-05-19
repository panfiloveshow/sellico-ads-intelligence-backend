CREATE TABLE IF NOT EXISTS campaign_budgets (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    cash        BIGINT      NOT NULL DEFAULT 0,
    netting     BIGINT      NOT NULL DEFAULT 0,
    total       BIGINT      NOT NULL DEFAULT 0,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_campaign_budgets_campaign_captured
    ON campaign_budgets (campaign_id, captured_at);

CREATE INDEX IF NOT EXISTS idx_campaign_budgets_latest
    ON campaign_budgets (campaign_id, captured_at DESC);
