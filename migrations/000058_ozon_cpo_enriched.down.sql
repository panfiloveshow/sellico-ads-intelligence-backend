ALTER TABLE ozon_cpo_products
    DROP COLUMN IF EXISTS offer_id,
    DROP COLUMN IF EXISTS name,
    DROP COLUMN IF EXISTS price_rub,
    DROP COLUMN IF EXISTS bid_price_rub,
    DROP COLUMN IF EXISTS image_url,
    DROP COLUMN IF EXISTS visibility_index;
