DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_ad_operations WHERE action<>'pause' OR previous_operation_id IS NOT NULL) THEN
  RAISE EXCEPTION 'advertising control history prevents downgrade';
 END IF;
END $$;
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_previous_check;
ALTER TABLE manager_ad_operations DROP COLUMN previous_operation_id;
ALTER TABLE manager_ad_operations DROP COLUMN previous_fingerprint;
ALTER TABLE manager_ad_operations DROP CONSTRAINT manager_ad_operations_action_check;
ALTER TABLE manager_ad_operations ADD CONSTRAINT manager_ad_operations_action_check CHECK(action='pause');
