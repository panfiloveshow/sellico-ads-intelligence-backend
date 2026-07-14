ALTER TABLE bid_changes
    DROP CONSTRAINT IF EXISTS bid_changes_placement_check;

ALTER TABLE bid_changes
    ADD CONSTRAINT bid_changes_placement_check
    CHECK (placement IN ('combined', 'search', 'recommendations', 'carousel'));
