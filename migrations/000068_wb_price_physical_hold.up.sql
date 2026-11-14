-- The existing queue already persists transport states and freezes payloads.
-- Its active-resource constraint must follow WB's nmID, not a local cabinet
-- alias. Standard cabinet deletion is soft and preserves these audit rows.
-- Switch API/workers together after stopping old repricer writers.
DO $$ BEGIN
    IF EXISTS (
        SELECT wb_product_id FROM price_changes
        WHERE wb_status IN ('submitting','submit_unknown','uploaded')
        GROUP BY wb_product_id HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'reconcile concurrent WB price changes before enabling physical product holds';
    END IF;
END $$;

CREATE UNIQUE INDEX price_changes_one_transport_per_nm
    ON price_changes (wb_product_id)
    WHERE wb_status IN ('submitting','submit_unknown','uploaded');
