-- 000009_workspace_settings.down.sql

ALTER TABLE workspaces DROP COLUMN IF EXISTS settings;
