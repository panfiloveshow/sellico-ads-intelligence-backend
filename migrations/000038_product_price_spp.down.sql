ALTER TABLE product_prices
    DROP COLUMN IF EXISTS customer_price_rub,
    DROP COLUMN IF EXISTS spp_percent;
