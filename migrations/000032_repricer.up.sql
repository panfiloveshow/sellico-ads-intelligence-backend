-- Repricer. All *_rub amounts are INTEGER RUBLES (WB Prices API convention),
-- matching products.price and product_economics costs. NOT kopecks.

-- Current prices/discounts synced from GET /api/v2/list/goods/filter.
CREATE TABLE product_prices (
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id           UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id      UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    wb_product_id          BIGINT      NOT NULL,          -- nmID
    price_rub              BIGINT      NOT NULL,          -- base price, integer rubles
    discount_percent       INT         NOT NULL DEFAULT 0,
    club_discount_percent  INT         NOT NULL DEFAULT 0,
    discounted_price_rub   BIGINT      NULL,              -- WB-computed effective price, integer rubles
    editable_size_price    BOOLEAN     NOT NULL DEFAULT FALSE,
    synced_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_product_prices_workspace_product ON product_prices (workspace_id, wb_product_id);
CREATE INDEX idx_product_prices_cabinet ON product_prices (seller_cabinet_id);

-- Async price-upload tasks (POST /api/v2/upload/task returns a WB task id we poll).
CREATE TABLE price_upload_tasks (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    wb_task_id        BIGINT      NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'uploaded', -- uploaded|processing|applied|partial|failed
    items_count       INT         NOT NULL DEFAULT 0,
    poll_count        INT         NOT NULL DEFAULT 0,
    last_polled_at    TIMESTAMPTZ NULL,
    completed_at      TIMESTAMPTZ NULL,
    error             TEXT        NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT price_upload_tasks_unique UNIQUE (seller_cabinet_id, wb_task_id)
);
CREATE INDEX idx_price_upload_tasks_pending ON price_upload_tasks (status)
    WHERE status IN ('uploaded', 'processing');

-- Every recommended/applied/rolled-back price change (audit + rollback source).
CREATE TABLE price_changes (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id    UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    strategy_id          UUID        NULL REFERENCES strategies(id) ON DELETE SET NULL,
    schedule_entry_id    UUID        NULL,
    upload_task_id       UUID        NULL REFERENCES price_upload_tasks(id),
    wb_product_id        BIGINT      NOT NULL,
    old_price_rub        BIGINT      NOT NULL,            -- integer rubles
    new_price_rub        BIGINT      NOT NULL,            -- integer rubles
    old_discount_percent INT         NOT NULL DEFAULT 0,
    new_discount_percent INT         NOT NULL DEFAULT 0,
    min_price_rub        BIGINT      NULL,                -- margin floor at decision time, integer rubles
    reason               TEXT        NOT NULL,
    source               TEXT        NOT NULL,            -- strategy|manual|rollback|schedule
    wb_status            TEXT        NOT NULL DEFAULT 'pending', -- recommended|pending|uploaded|applied|failed|rolled_back
    error                TEXT        NULL,
    can_rollback         BOOLEAN     NOT NULL DEFAULT TRUE,
    rollback_of          UUID        NULL REFERENCES price_changes(id),
    decision_context     JSONB       NULL,
    created_by           UUID        NULL REFERENCES users(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT price_changes_non_negative CHECK (old_price_rub >= 0 AND new_price_rub >= 0)
);
CREATE INDEX idx_price_changes_workspace_created ON price_changes (workspace_id, created_at DESC);
CREATE INDEX idx_price_changes_product ON price_changes (workspace_id, wb_product_id, created_at DESC);
CREATE INDEX idx_price_changes_task ON price_changes (upload_task_id);

-- Products WB parked in price quarantine (new discounted price ≥3x below old).
CREATE TABLE price_quarantine_goods (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    wb_product_id     BIGINT      NOT NULL,
    old_price_rub     BIGINT      NULL,                   -- integer rubles
    new_price_rub     BIGINT      NULL,                   -- integer rubles
    detected_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at       TIMESTAMPTZ NULL,
    notified          BOOLEAN     NOT NULL DEFAULT FALSE,
    CONSTRAINT price_quarantine_unique UNIQUE (workspace_id, wb_product_id, detected_at)
);
CREATE INDEX idx_price_quarantine_active ON price_quarantine_goods (workspace_id)
    WHERE resolved_at IS NULL;

-- Scheduled price changes (calendar: plan weeks ahead, optional auto-revert).
CREATE TABLE price_schedule_entries (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id  UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    scope_type         TEXT        NOT NULL,              -- product|list|all
    product_ids        BIGINT[]    NULL,                  -- nmIDs for scope_type=list; NULL for all
    adjustment_type    TEXT        NOT NULL,              -- target_rub|delta_percent
    adjustment_value   DOUBLE PRECISION NOT NULL,
    direction          TEXT        NULL,                  -- up|down (for delta_percent)
    scheduled_at       TIMESTAMPTZ NOT NULL,
    revert_at          TIMESTAMPTZ NULL,
    revert_to_previous BOOLEAN     NOT NULL DEFAULT FALSE,
    revert_of          UUID        NULL REFERENCES price_schedule_entries(id) ON DELETE CASCADE,
    status             TEXT        NOT NULL DEFAULT 'planned', -- planned|executing|done|failed|canceled
    executed_task_ids  UUID[]      NULL,
    error              TEXT        NULL,
    comment            TEXT        NULL,
    created_by         UUID        NULL REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_price_schedule_due ON price_schedule_entries (status, scheduled_at);
CREATE INDEX idx_price_schedule_workspace ON price_schedule_entries (workspace_id, scheduled_at);

-- Track whether a cabinet's WB token carries the "Цены и скидки" scope.
ALTER TABLE seller_cabinets
    ADD COLUMN prices_scope_status     TEXT        NOT NULL DEFAULT 'unknown', -- unknown|ok|missing
    ADD COLUMN prices_scope_checked_at TIMESTAMPTZ NULL;
