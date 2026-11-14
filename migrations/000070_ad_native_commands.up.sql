CREATE TABLE ad_native_commands (
    id uuid PRIMARY KEY,
    marketplace text NOT NULL CHECK(marketplace IN ('wb','ozon')),
    account_key text NOT NULL,
    request_fingerprint char(64) NOT NULL,
    method text NOT NULL,
    endpoint text NOT NULL,
    resources jsonb NOT NULL CHECK(jsonb_typeof(resources)='array'),
    origin text NOT NULL CHECK(origin IN ('legacy','manager')),
    workspace_id uuid,
    actor_id uuid,
    approval_fingerprint char(64),
    approval_expires_at timestamptz,
    status text NOT NULL CHECK(status IN ('approved','dispatching','acknowledged','rejected','unknown','observed')),
    started_at timestamptz,
    finished_at timestamptz,
    http_status smallint,
    response_hash char(64),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK(origin<>'manager' OR (workspace_id IS NOT NULL AND actor_id IS NOT NULL AND approval_fingerprint IS NOT NULL AND approval_expires_at IS NOT NULL))
);
CREATE INDEX ad_native_commands_owner ON ad_native_commands(workspace_id,actor_id,created_at);
CREATE TABLE ad_native_resource_holds (
    resource_key text PRIMARY KEY,
    account_key text NOT NULL,
    command_id uuid NOT NULL REFERENCES ad_native_commands(id),
    global_scope boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ad_native_resource_holds_account ON ad_native_resource_holds(account_key);
ALTER TABLE ozon_bid_changes DROP CONSTRAINT ozon_bid_changes_status_check;
ALTER TABLE ozon_bid_changes ADD CONSTRAINT ozon_bid_changes_status_check
    CHECK(status IN ('pending','applied','failed','rolled_back','shadow','unknown'));
