-- A dispatch receipt is not proof of the resulting price. Keep the physical
-- seller/product hold until an acknowledged command is observed at Ozon.
ALTER TABLE ozon_price_changes DROP CONSTRAINT ozon_price_changes_status_check;
ALTER TABLE ozon_price_changes ADD CONSTRAINT ozon_price_changes_status_check
    CHECK (status IN ('pending', 'submitting', 'verifying', 'submit_unknown',
                      'applied', 'failed', 'rolled_back', 'shadow'));

CREATE TABLE ozon_price_write_attempts (
    -- Deliberately no cascading cabinet/change FK: deleting a local cabinet
    -- must not erase an uncertain command or unlock its physical product.
    change_id UUID PRIMARY KEY,
    seller_cabinet_id UUID NOT NULL,
    product_id BIGINT NOT NULL CHECK (product_id > 0),
    vendor_account_key TEXT NOT NULL CHECK (length(vendor_account_key) = 64),
    resource_key TEXT UNIQUE,
    status TEXT NOT NULL CHECK (status IN
        ('submitting', 'verifying', 'submit_unknown', 'applied', 'failed')),
    price_cents BIGINT NOT NULL CHECK (price_cents > 0),
    old_price_cents BIGINT CHECK (old_price_cents >= 0),
    min_price_cents BIGINT CHECK (min_price_cents >= 0),
    baseline JSONB NOT NULL,
    observed JSONB,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    last_observed_at TIMESTAMPTZ,
    error TEXT,
    CHECK ((status IN ('submitting', 'verifying', 'submit_unknown')) = (resource_key IS NOT NULL)),
    CHECK (acknowledged_at IS NULL OR dispatched_at IS NOT NULL),
    CHECK (status <> 'applied' OR (acknowledged_at IS NOT NULL AND verified_at IS NOT NULL))
);

CREATE INDEX idx_ozon_price_attempts_account_budget
    ON ozon_price_write_attempts (vendor_account_key, product_id, dispatched_at DESC);
CREATE INDEX idx_ozon_price_attempts_recovery
    ON ozon_price_write_attempts (seller_cabinet_id, change_id)
    WHERE resource_key IS NOT NULL;

-- A calendar phase has one immutable command, including across worker
-- retries. Legacy rows have no schedule_phase and remain unchanged.
CREATE UNIQUE INDEX idx_ozon_price_schedule_phase
    ON ozon_price_changes ((decision_context->>'schedule_id'), (decision_context->>'schedule_phase'))
    WHERE decision_context ? 'schedule_id' AND decision_context ? 'schedule_phase';
