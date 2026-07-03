CREATE TABLE IF NOT EXISTS wb_commission_tariffs (
    id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id                UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id           UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    parent_id                   BIGINT      NOT NULL,
    parent_name                 TEXT        NOT NULL,
    subject_id                  BIGINT      NOT NULL,
    subject_name                TEXT        NOT NULL,
    kgvp_booking                DOUBLE PRECISION NULL,
    kgvp_pickup                 DOUBLE PRECISION NULL,
    kgvp_supplier               DOUBLE PRECISION NULL,
    kgvp_supplier_express       DOUBLE PRECISION NULL,
    kgvp_marketplace            DOUBLE PRECISION NULL,
    paid_storage_kgvp           DOUBLE PRECISION NULL,
    source                      TEXT        NOT NULL DEFAULT 'wb_tariffs_commission',
    captured_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, subject_id)
);

CREATE INDEX IF NOT EXISTS idx_wb_commission_tariffs_workspace
    ON wb_commission_tariffs (workspace_id, subject_name);
