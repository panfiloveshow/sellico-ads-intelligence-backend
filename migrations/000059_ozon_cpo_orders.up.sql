-- CPO («Оплата за заказ») promoted orders, mirrored from the async
-- Performance report GET /api/client/statistics/all_sku_promo/orders/generate
-- (verified live 2026-08-10). Each row is one promoted order line: the order,
-- the promoted SKU, quantity/price and what the promotion charged for it.
-- Money arrives as Russian decimals («752,00») and dates as DD.MM.YYYY —
-- parsed at the client boundary, stored as proper types here.
CREATE TABLE ozon_cpo_orders (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    date              DATE        NOT NULL,
    order_id          TEXT        NOT NULL,
    order_number      TEXT,
    sku               BIGINT,
    adv_sku           BIGINT,
    vendor_code       TEXT,
    name              TEXT,
    quantity          INT,
    price_rub         NUMERIC(12,2),
    sale_price_rub    NUMERIC(12,2),
    bid_pct           NUMERIC(5,2),
    bid_rub           NUMERIC(10,2),
    spend_rub         NUMERIC(12,2),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, order_id, sku)
);

CREATE INDEX idx_ozon_cpo_orders_cabinet_date
    ON ozon_cpo_orders (seller_cabinet_id, date DESC);
CREATE INDEX idx_ozon_cpo_orders_cabinet_sku
    ON ozon_cpo_orders (seller_cabinet_id, sku);

-- search_promo/v2/products enrichment part 2: previous bid and weekly view
-- counters (previousBid.bid, views.thisWeek/previousWeek). COALESCE-preserved
-- in the upsert like the 000058 display fields.
ALTER TABLE ozon_cpo_products
    ADD COLUMN prev_bid_pct    NUMERIC(5,2),
    ADD COLUMN views_this_week BIGINT,
    ADD COLUMN views_prev_week BIGINT;
