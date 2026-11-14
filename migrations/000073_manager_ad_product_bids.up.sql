ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_action_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_action_check CHECK(action IN ('pause','resume','handover','set_bid'));
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_previous_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_previous_check CHECK (
 (previous_operation_id IS NULL AND previous_fingerprint IS NULL AND action IN ('pause','set_bid')) OR
 (previous_operation_id IS NOT NULL AND previous_fingerprint IS NOT NULL AND length(previous_fingerprint)=64 AND previous_operation_id<>id)
);
ALTER TABLE manager_ad_operations ADD COLUMN bid_plan jsonb;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_bid_check CHECK ((action='set_bid')=(bid_plan IS NOT NULL));

-- Native CPC bids are integer micro-rubles. Preserve all six decimal places
-- when a personally observed result is mirrored; the former cents column
-- silently rounded a successful exact native change.
ALTER TABLE ozon_campaign_products ALTER COLUMN bid_rub TYPE NUMERIC(20,6);
