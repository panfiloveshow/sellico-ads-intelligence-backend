ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_action_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_action_check CHECK(action IN ('pause','resume','handover','set_bid','set_budget'));
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_previous_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_previous_check CHECK (
 (previous_operation_id IS NULL AND previous_fingerprint IS NULL AND action IN ('pause','set_bid','set_budget')) OR
 (previous_operation_id IS NOT NULL AND previous_fingerprint IS NOT NULL AND length(previous_fingerprint)=64 AND previous_operation_id<>id)
);
ALTER TABLE manager_ad_operations ADD COLUMN budget_plan jsonb;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_budget_check CHECK ((action='set_budget')=(budget_plan IS NOT NULL));
