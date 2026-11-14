DO $$ BEGIN
 LOCK TABLE manager_ad_operations,ozon_campaign_products IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM manager_ad_operations WHERE action='set_bid' OR bid_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'personal advertising bid history prevents downgrade';
 END IF;
 IF EXISTS(SELECT 1 FROM ozon_campaign_products WHERE bid_rub IS NOT NULL AND (bid_rub<>trunc(bid_rub,2) OR abs(bid_rub)>=10000000000)) THEN
  RAISE EXCEPTION 'exact Ozon bid values prevent a lossy cents downgrade';
 END IF;
 ALTER TABLE ozon_campaign_products ALTER COLUMN bid_rub TYPE NUMERIC(12,2);
END $$;
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_bid_check;
ALTER TABLE manager_ad_operations DROP COLUMN bid_plan;
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_previous_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_previous_check CHECK (
 (previous_operation_id IS NULL AND previous_fingerprint IS NULL AND action='pause') OR
 (previous_operation_id IS NOT NULL AND previous_fingerprint IS NOT NULL AND length(previous_fingerprint)=64 AND previous_operation_id<>id)
);
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_action_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_action_check CHECK(action IN ('pause','resume','handover'));
