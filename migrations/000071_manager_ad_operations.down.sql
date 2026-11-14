DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM manager_ad_operations WHERE status IN ('executing','verifying','unknown'))
 OR EXISTS(SELECT 1 FROM manager_ad_operations o JOIN ad_native_resource_holds h ON h.command_id=o.id) THEN
  RAISE EXCEPTION 'unresolved or managed advertising controls prevent downgrade';
 END IF;
END $$;
DROP TABLE manager_ad_operations;
