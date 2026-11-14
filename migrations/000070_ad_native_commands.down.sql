DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ad_native_resource_holds) OR EXISTS(SELECT 1 FROM ozon_bid_changes WHERE status='unknown') THEN
   RAISE EXCEPTION 'unresolved advertising writes prevent downgrade';
 END IF;
END $$;
ALTER TABLE ozon_bid_changes DROP CONSTRAINT ozon_bid_changes_status_check;
ALTER TABLE ozon_bid_changes ADD CONSTRAINT ozon_bid_changes_status_check
 CHECK(status IN ('pending','applied','failed','rolled_back','shadow'));
DROP TABLE ad_native_resource_holds;
DROP TABLE ad_native_commands;
