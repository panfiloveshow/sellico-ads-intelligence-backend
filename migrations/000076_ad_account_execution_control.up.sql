-- Explicit account-wide service write controls. Native campaign state is untouched.
CREATE TABLE ad_account_execution_controls (
    account_key text PRIMARY KEY CHECK (account_key ~ '^(wb|ozon):[a-f0-9]{64}$'),
    marketplace text NOT NULL CHECK (marketplace IN ('wb','ozon')),
    mode text NOT NULL CHECK (mode IN ('stopped','manual_only')),
    version bigint NOT NULL CHECK (version > 0 AND version <= 9007199254740991),
    changed_at timestamptz NOT NULL,
    changed_by_operation uuid NOT NULL
);
CREATE TABLE manager_ad_execution_control_operations (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    immutable_payload jsonb NOT NULL CHECK (jsonb_typeof(immutable_payload)='object'),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    status text NOT NULL CHECK (status IN ('awaiting_confirmation','succeeded','cancelled','expired')),
    result_control jsonb,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes'),
    CHECK ((status='succeeded') = (result_control IS NOT NULL))
);
CREATE INDEX manager_ad_execution_controls_owner ON manager_ad_execution_control_operations(workspace_id,actor_id,created_at DESC);
ALTER TABLE ad_native_commands ADD COLUMN execution_control_version bigint NOT NULL DEFAULT 0 CHECK (execution_control_version >= 0);
