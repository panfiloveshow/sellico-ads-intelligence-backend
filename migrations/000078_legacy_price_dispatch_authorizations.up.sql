-- One service-authenticated barrier per exact legacy Products price request.
-- This records permission before dispatch, never native completion.
CREATE TABLE legacy_price_dispatch_authorizations (
    command_id uuid PRIMARY KEY,
    origin text NOT NULL CHECK (origin = 'products'),
    account_key text NOT NULL CHECK (account_key ~ '^(wb|ozon):[a-f0-9]{64}$'),
    marketplace text NOT NULL CHECK (marketplace IN ('wb', 'ozon')),
    endpoint text NOT NULL,
    payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
    request_fingerprint text NOT NULL CHECK (request_fingerprint ~ '^[a-f0-9]{64}$'),
    sent_at timestamptz NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at = issued_at + interval '5 seconds')
);
CREATE INDEX legacy_price_dispatch_account ON legacy_price_dispatch_authorizations(account_key, issued_at DESC);
