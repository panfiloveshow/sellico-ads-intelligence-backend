-- Additive personal review ledger. It never rewrites or guesses Native outcomes.
CREATE TABLE IF NOT EXISTS legacy_price_personal_reviews (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    cabinet_id uuid NOT NULL,
    external_user_id text NOT NULL,
    external_workspace_id text NOT NULL,
    integration_id text NOT NULL,
    integration_name text NOT NULL,
    marketplace text NOT NULL CHECK (marketplace IN ('wb','ozon')),
    account_key text NOT NULL CHECK (account_key ~ '^(wb|ozon):[a-f0-9]{64}$'),
    credential_digest text NOT NULL CHECK (credential_digest ~ '^[a-f0-9]{64}$'),
    grant_id uuid NOT NULL REFERENCES legacy_price_dispatch_authorizations(command_id),
    grant_fingerprint text NOT NULL CHECK (grant_fingerprint ~ '^[a-f0-9]{64}$'),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot)='object'),
    status text NOT NULL CHECK (status IN ('awaiting_confirmation','resolved_manually','cancelled','expired')),
    confirmations jsonb,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    resolved_at timestamptz,
    CHECK (expires_at=created_at+interval '5 minutes'),
    CHECK ((status='resolved_manually') = (resolved_at IS NOT NULL)),
    CHECK (status<>'resolved_manually' OR (
      resolved_at>=created_at AND resolved_at<expires_at AND confirmations IS NOT NULL AND confirmations =
      '{"legacy_history_and_support_checked":true,"legacy_no_native_work_in_progress":true,"legacy_late_effect_risk_accepted":true,"legacy_new_approvals_only":true}'::jsonb
    ))
);
CREATE UNIQUE INDEX IF NOT EXISTS legacy_price_one_personal_resolution
 ON legacy_price_personal_reviews(grant_id) WHERE status='resolved_manually';
CREATE INDEX IF NOT EXISTS legacy_price_review_owner
 ON legacy_price_personal_reviews(workspace_id,actor_id,created_at DESC);
CREATE INDEX IF NOT EXISTS legacy_price_review_account_receipt
 ON legacy_price_personal_reviews(account_key,grant_id,grant_fingerprint) WHERE status='resolved_manually';
