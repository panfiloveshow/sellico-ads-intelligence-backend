-- Ozon product catalog mapping: product_id (Seller API key) ↔ sales SKU
-- (Performance API key) + name/offer_id/image from POST /v3/product/info/list.
-- Bridges the two key spaces: ozon_product_prices rows are keyed by
-- product_id, campaign/CPO rows are keyed by the sales SKU.
CREATE TABLE ozon_products (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    product_id        BIGINT      NOT NULL,
    sku               BIGINT,
    offer_id          TEXT,
    name              TEXT,
    primary_image     TEXT,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, product_id)
);

-- Campaign / CPO / bid-change enrichment joins by the sales SKU.
CREATE INDEX idx_ozon_products_cabinet_sku ON ozon_products (seller_cabinet_id, sku);
