ALTER TABLE product_prices
    ADD COLUMN spp_percent DOUBLE PRECISION NULL,
    ADD COLUMN customer_price_rub BIGINT NULL;

ALTER TABLE product_prices
    ADD CONSTRAINT product_prices_spp_percent_bounds CHECK (
        spp_percent IS NULL OR (spp_percent >= 0 AND spp_percent <= 100)
    ),
    ADD CONSTRAINT product_prices_customer_price_non_negative CHECK (
        customer_price_rub IS NULL OR customer_price_rub >= 0
    );

COMMENT ON COLUMN product_prices.spp_percent IS
    'Actual WB SPP from Sellico unit economics; public card is fallback only';
COMMENT ON COLUMN product_prices.customer_price_rub IS
    'Buyer-facing price after SPP from Sellico unit economics';
