ALTER TABLE products DROP COLUMN IF EXISTS competitive_bid;
ALTER TABLE products DROP COLUMN IF EXISTS min_bid_recommend;
ALTER TABLE products DROP COLUMN IF EXISTS min_bid_search;
ALTER TABLE products DROP COLUMN IF EXISTS current_bid_recommend;
ALTER TABLE products DROP COLUMN IF EXISTS current_bid_search;

DROP TABLE IF EXISTS campaign_plus_phrases;
DROP TABLE IF EXISTS campaign_minus_phrases;
DROP TABLE IF EXISTS bid_changes;
DROP TABLE IF EXISTS strategy_bindings;
DROP TABLE IF EXISTS strategies;
