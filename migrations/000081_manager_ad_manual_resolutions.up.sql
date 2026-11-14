-- Additive personal manual resolution ledger for unresolved ('unknown') manager ad operations.
-- Never rewrites ad_native_commands outcomes and never replays vendor mutations.
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_status_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_status_check
    CHECK (status IN ('awaiting_confirmation','executing','verifying','succeeded','failed','unknown','resolved_manually','cancelled','expired'));

CREATE TABLE IF NOT EXISTS manager_ad_manual_resolutions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    seller_cabinet_id uuid NOT NULL,
    external_user_id text NOT NULL,
    external_workspace_id text NOT NULL,
    integration_id text NOT NULL,
    integration_name text NOT NULL,
    marketplace text NOT NULL CHECK (marketplace IN ('wb','ozon')),
    operation_id uuid NOT NULL REFERENCES manager_ad_operations(id),
    operation_type text NOT NULL CHECK (operation_type IN ('ad_change','ad_bid_change','ad_budget_change','ad_launch')),
    campaign_id uuid NOT NULL,
    external_id text NOT NULL,
    reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 2000),
    support_reference text NOT NULL CHECK (char_length(support_reference) BETWEEN 12 AND 256),
    fingerprint char(64) NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    status text NOT NULL CHECK (status IN ('awaiting_confirmation','resolved_manually','cancelled','expired')),
    confirmations jsonb,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    resolved_at timestamptz,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes'),
    CHECK ((status = 'resolved_manually') = (resolved_at IS NOT NULL)),
    CHECK (status <> 'resolved_manually' OR (
        resolved_at >= created_at AND resolved_at <= expires_at AND confirmations IS NOT NULL AND confirmations =
        '{"ad_cabinet_state_checked":true,"ad_hold_release_understood":true,"ad_late_effect_risk_accepted":true,"ad_no_pending_mutation":true}'::jsonb
    ))
);

CREATE UNIQUE INDEX IF NOT EXISTS manager_ad_one_manual_resolution
    ON manager_ad_manual_resolutions(operation_id) WHERE status = 'resolved_manually';

CREATE INDEX IF NOT EXISTS manager_ad_manual_resolutions_owner
    ON manager_ad_manual_resolutions(workspace_id, actor_id, created_at DESC);
