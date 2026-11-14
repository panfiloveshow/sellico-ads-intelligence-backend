-- External operation identity is retained independently of cabinet lifetime.
CREATE TABLE manager_price_commands (
    operation_id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    seller_cabinet_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    marketplace text NOT NULL CHECK (marketplace IN ('wb','ozon')),
    fingerprint char(64) NOT NULL,
    approval jsonb NOT NULL,
    native_change_id uuid,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX manager_price_commands_owner ON manager_price_commands(workspace_id, actor_id, created_at);
CREATE UNIQUE INDEX manager_price_commands_native ON manager_price_commands(marketplace, native_change_id) WHERE native_change_id IS NOT NULL;
