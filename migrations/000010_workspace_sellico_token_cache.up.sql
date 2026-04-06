-- 000010_workspace_sellico_token_cache.up.sql
-- Caches the latest Sellico user token per workspace for background integration refresh.
-- Token is encrypted with the same AES-256-GCM key as WB API tokens.

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS encrypted_sellico_token TEXT NULL;
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS sellico_token_updated_at TIMESTAMPTZ NULL;

COMMENT ON COLUMN workspaces.encrypted_sellico_token IS 'AES-256-GCM encrypted Sellico user token for background integration refresh';
COMMENT ON COLUMN workspaces.sellico_token_updated_at IS 'When the cached Sellico token was last updated';
