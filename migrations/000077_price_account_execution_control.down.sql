DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM price_account_execution_controls)
       OR EXISTS (SELECT 1 FROM manager_price_execution_control_operations)
       OR EXISTS (SELECT 1 FROM manager_price_commands WHERE approval ? 'execution_control_version')
       OR EXISTS (SELECT 1 FROM price_changes WHERE (decision_context->'manager_approval') ? 'execution_control_version')
       OR EXISTS (SELECT 1 FROM ozon_price_changes WHERE (decision_context->'manager_approval') ? 'execution_control_version') THEN
        RAISE EXCEPTION 'cannot discard personal price account execution controls or their audit history';
    END IF;
END $$;
DROP TABLE manager_price_execution_control_operations;
DROP TABLE price_account_execution_controls;
