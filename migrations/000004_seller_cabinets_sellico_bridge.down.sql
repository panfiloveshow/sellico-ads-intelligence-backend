DROP INDEX IF EXISTS idx_seller_cabinets_workspace_source;
DROP INDEX IF EXISTS idx_seller_cabinets_external_integration_id;

ALTER TABLE seller_cabinets
    DROP COLUMN IF EXISTS last_sellico_sync_at,
    DROP COLUMN IF EXISTS integration_type,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS external_integration_id;
