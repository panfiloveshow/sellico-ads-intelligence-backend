DO $$ BEGIN
 LOCK TABLE manager_ad_operations IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM manager_ad_operations WHERE action='set_budget' OR budget_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'personal advertising budget history prevents downgrade';
 END IF;
END $$;
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_budget_check;
ALTER TABLE manager_ad_operations DROP COLUMN budget_plan;
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_previous_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_previous_check CHECK (
 (previous_operation_id IS NULL AND previous_fingerprint IS NULL AND action IN ('pause','set_bid')) OR
 (previous_operation_id IS NOT NULL AND previous_fingerprint IS NOT NULL AND length(previous_fingerprint)=64 AND previous_operation_id<>id)
);
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_action_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_action_check CHECK(action IN ('pause','resume','handover','set_bid'));
