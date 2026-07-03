ALTER TABLE product_sales_funnel_periods
    DROP COLUMN IF EXISTS order_count,
    DROP COLUMN IF EXISTS open_count;
