-- 000001_init.down.sql
-- Drop all tables in reverse order of creation (respecting FK dependencies)

DROP TABLE IF EXISTS job_runs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS extension_sessions;
DROP TABLE IF EXISTS exports;
DROP TABLE IF EXISTS recommendations;
DROP TABLE IF EXISTS bid_snapshots;
DROP TABLE IF EXISTS serp_result_items;
DROP TABLE IF EXISTS serp_snapshots;
DROP TABLE IF EXISTS positions;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS phrase_stats;
DROP TABLE IF EXISTS phrases;
DROP TABLE IF EXISTS campaign_stats;
DROP TABLE IF EXISTS campaigns;
DROP TABLE IF EXISTS seller_cabinets;
DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
