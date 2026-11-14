DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM price_changes WHERE wb_status IN ('submitting','submit_unknown','uploaded')) THEN
        RAISE EXCEPTION 'reconcile active WB price changes before removing physical product holds';
    END IF;
END $$;

DROP INDEX price_changes_one_transport_per_nm;
