CREATE TABLE IF NOT EXISTS wb_api_rate_limits (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id      UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    endpoint_key           VARCHAR(64) NOT NULL,
    next_allowed_at        TIMESTAMPTZ NOT NULL,
    retry_after_seconds    INTEGER     NOT NULL DEFAULT 0,
    last_status            INTEGER     NOT NULL DEFAULT 429,
    last_error             TEXT        NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, endpoint_key)
);

CREATE INDEX IF NOT EXISTS idx_wb_api_rate_limits_active
    ON wb_api_rate_limits (seller_cabinet_id, next_allowed_at);
