DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM legacy_price_dispatch_authorizations) THEN
        RAISE EXCEPTION 'cannot discard legacy price dispatch authorization audit';
    END IF;
END $$;
DROP TABLE legacy_price_dispatch_authorizations;
