-- The raw legacy protocol has no native product IDs. Account-level lookups
-- must remain bounded by active holds rather than scan the full price history.
CREATE INDEX idx_ozon_active_price_account
    ON ozon_price_write_attempts (vendor_account_key)
    WHERE resource_key IS NOT NULL;

CREATE INDEX idx_wb_active_price_account
    ON price_changes ((decision_context#>>'{wb_dispatch,account_key}'))
    WHERE wb_status IN ('submitting','submit_unknown','uploaded')
      AND jsonb_typeof(decision_context#>'{wb_dispatch,account_key}') = 'string'
      AND decision_context#>>'{wb_dispatch,account_key}' ~ '^wb:[a-f0-9]{64}$';
