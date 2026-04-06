DROP INDEX IF EXISTS idx_workspaces_external_workspace_id;
ALTER TABLE workspaces
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS external_workspace_id;

DROP INDEX IF EXISTS idx_users_external_user_id;
ALTER TABLE users
    DROP COLUMN IF EXISTS auth_source,
    DROP COLUMN IF EXISTS external_user_id;
