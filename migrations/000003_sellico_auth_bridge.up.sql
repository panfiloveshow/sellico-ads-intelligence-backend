ALTER TABLE users
    ADD COLUMN external_user_id TEXT,
    ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'local';

CREATE UNIQUE INDEX idx_users_external_user_id ON users (external_user_id)
WHERE external_user_id IS NOT NULL;

ALTER TABLE workspaces
    ADD COLUMN external_workspace_id TEXT,
    ADD COLUMN source TEXT NOT NULL DEFAULT 'local';

CREATE UNIQUE INDEX idx_workspaces_external_workspace_id ON workspaces (external_workspace_id)
WHERE external_workspace_id IS NOT NULL;
