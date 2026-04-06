ALTER TABLE seller_cabinets
    ADD COLUMN external_integration_id TEXT,
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual',
    ADD COLUMN integration_type TEXT,
    ADD COLUMN last_sellico_sync_at TIMESTAMPTZ NULL;

CREATE UNIQUE INDEX idx_seller_cabinets_external_integration_id
ON seller_cabinets (external_integration_id)
WHERE external_integration_id IS NOT NULL;

CREATE INDEX idx_seller_cabinets_workspace_source
ON seller_cabinets (workspace_id, source)
WHERE deleted_at IS NULL;
