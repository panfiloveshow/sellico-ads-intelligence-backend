-- 000012_product_events.up.sql
-- Product change history and event tracking.

-- 1. Product events — tracks all changes to a product card
CREATE TABLE product_events (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    product_id    UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    event_type    VARCHAR(50) NOT NULL CHECK (event_type IN (
        'price_change', 'stock_change', 'content_change',
        'photo_change', 'rating_change', 'review_count_change',
        'category_change', 'brand_change', 'title_change',
        'created', 'synced'
    )),
    field_name    VARCHAR(100),
    old_value     TEXT,
    new_value     TEXT,
    metadata      JSONB DEFAULT '{}',
    detected_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    source        VARCHAR(20) NOT NULL DEFAULT 'sync' CHECK (source IN ('sync', 'extension', 'manual'))
);

CREATE INDEX idx_product_events_product ON product_events (product_id, detected_at DESC);
CREATE INDEX idx_product_events_workspace ON product_events (workspace_id, detected_at DESC);
CREATE INDEX idx_product_events_type ON product_events (event_type, detected_at DESC);

-- 2. Product snapshots — periodic full snapshots for diffing
CREATE TABLE product_snapshots (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id   UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    title        TEXT,
    brand        VARCHAR(255),
    category     VARCHAR(500),
    price        BIGINT,
    rating       FLOAT,
    reviews_count INT,
    stock_total  INT,
    image_url    TEXT,
    content_hash VARCHAR(64),
    captured_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_product_snapshots_product ON product_snapshots (product_id, captured_at DESC);

-- 3. Add tracking fields to products table
ALTER TABLE products ADD COLUMN IF NOT EXISTS rating FLOAT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS reviews_count INT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS stock_total INT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64) DEFAULT '';
ALTER TABLE products ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ;
