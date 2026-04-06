-- 000009_workspace_settings.up.sql
-- Adds per-workspace settings (recommendation thresholds, notification config).

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN workspaces.settings IS 'Per-workspace settings: recommendation_thresholds, notifications (telegram bot token / chat id), etc.';
