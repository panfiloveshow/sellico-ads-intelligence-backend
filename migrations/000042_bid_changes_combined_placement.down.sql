ALTER TABLE bid_changes
    DROP CONSTRAINT IF EXISTS bid_changes_placement_check;

-- Existing combined audit rows remain truthful on rollback; the NOT VALID
-- constraint blocks new combined rows without rewriting historical actions.
ALTER TABLE bid_changes
    ADD CONSTRAINT bid_changes_placement_check
    CHECK (placement IN ('search', 'recommendations', 'carousel')) NOT VALID;
