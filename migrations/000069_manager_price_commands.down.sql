DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM manager_price_commands m JOIN price_changes p ON p.id=m.native_change_id
        WHERE m.marketplace='wb' AND p.wb_status IN ('pending','submitting','submit_unknown','uploaded'))
       OR EXISTS (SELECT 1 FROM manager_price_commands m JOIN ozon_price_changes p ON p.id=m.native_change_id
        WHERE m.marketplace='ozon' AND p.status IN ('pending','submitting','submit_unknown','verifying')) THEN
        RAISE EXCEPTION 'unresolved manager price commands prevent downgrade';
    END IF;
END $$;
DROP TABLE manager_price_commands;
