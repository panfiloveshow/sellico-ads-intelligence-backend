DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM ad_account_execution_controls)
       OR EXISTS (SELECT 1 FROM manager_ad_execution_control_operations)
       OR EXISTS (SELECT 1 FROM ad_native_commands WHERE execution_control_version<>0) THEN
        RAISE EXCEPTION 'cannot discard personal account execution controls or their audit history';
    END IF;
END $$;
ALTER TABLE ad_native_commands DROP COLUMN execution_control_version;
DROP TABLE manager_ad_execution_control_operations;
DROP TABLE ad_account_execution_controls;
