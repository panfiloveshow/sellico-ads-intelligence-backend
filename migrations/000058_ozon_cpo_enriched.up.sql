-- CPO (search promo) product enrichment. The search_promo/v2/products response
-- carries per-product display fields Ozon does not expose anywhere else
-- (sourceSku=offer_id, title, price, bidPrice, imageUrl, visibilityIndex).
-- Mirroring them keeps the CPO products list fully rendered even when a later
-- refresh call to Ozon fails and we fall back to the stored rows.
ALTER TABLE ozon_cpo_products
    ADD COLUMN offer_id         TEXT,
    ADD COLUMN name             TEXT,
    ADD COLUMN price_rub        NUMERIC(12,2),
    ADD COLUMN bid_price_rub    NUMERIC(12,2),
    ADD COLUMN image_url        TEXT,
    ADD COLUMN visibility_index TEXT;
