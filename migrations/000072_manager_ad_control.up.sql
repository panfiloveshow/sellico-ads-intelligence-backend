ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_action_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_action_check CHECK(action IN ('pause','resume','handover'));
ALTER TABLE manager_ad_operations ADD COLUMN previous_operation_id uuid REFERENCES manager_ad_operations(id);
ALTER TABLE manager_ad_operations ADD COLUMN previous_fingerprint char(64);
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_previous_check CHECK (
 (previous_operation_id IS NULL AND previous_fingerprint IS NULL AND action='pause') OR
 (previous_operation_id IS NOT NULL AND previous_fingerprint IS NOT NULL AND length(previous_fingerprint)=64 AND previous_operation_id<>id)
);
