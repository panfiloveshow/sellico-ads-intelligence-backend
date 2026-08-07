-- Sellico unit-economics mirror for Ozon cabinets, keyed by offer_id (артикул).
-- Feeds the ozon_price_margin_floor cost fallback when Ozon itself does not
-- report net_price in /v5/product/info/prices.
CREATE TABLE ozon_product_economics (
    id                 UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id  UUID          NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    offer_id           TEXT          NOT NULL,
    sku                BIGINT,
    cost_price_rub     NUMERIC(12,2),
    logistics_cost_rub NUMERIC(12,2),
    other_costs_rub    NUMERIC(12,2),
    tax_percent        NUMERIC(6,2),
    commission_percent NUMERIC(6,2),
    max_allowed_drr    NUMERIC(6,2),
    source             TEXT          DEFAULT 'sellico',
    synced_at          TIMESTAMPTZ   DEFAULT now(),
    UNIQUE (seller_cabinet_id, offer_id)
);
