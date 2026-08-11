-- bid_changes.wb_status has been non-nullable in practice since 000011: it
-- carries a DEFAULT and every insert path sets it. The Go layer has always
-- treated it as a plain string; only the column definition disagreed, which
-- made the typed layer and the schema drift apart.
--
-- Constrain the column to match reality (price_changes.wb_status already is
-- NOT NULL), so the generated type is `string` and no NULL can ever reach a
-- status comparison.
UPDATE bid_changes SET wb_status = 'pending' WHERE wb_status IS NULL;

ALTER TABLE bid_changes
    ALTER COLUMN wb_status SET DEFAULT 'pending',
    ALTER COLUMN wb_status SET NOT NULL;
