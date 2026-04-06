-- 000014_competitors.up.sql
-- Competitor tracking and analysis.

-- 1. Competitors — tracked competitor products
CREATE TABLE competitors (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID         NOT NULL REFERENCES workspaces(id),
    product_id     UUID         NOT NULL REFERENCES products(id),
    competitor_nm_id BIGINT     NOT NULL,
    competitor_title TEXT       NOT NULL,
    competitor_brand VARCHAR(255),
    competitor_price BIGINT,
    competitor_rating FLOAT,
    competitor_reviews_count INT,
    competitor_image_url TEXT,
    query          TEXT         NOT NULL,
    region         VARCHAR(50)  DEFAULT '',
    first_seen_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_position  INT,
    our_position   INT,
    source         VARCHAR(20)  NOT NULL DEFAULT 'serp',
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, product_id, competitor_nm_id, query)
);

CREATE INDEX idx_competitors_product ON competitors (product_id, last_seen_at DESC);
CREATE INDEX idx_competitors_workspace ON competitors (workspace_id, last_seen_at DESC);

-- 2. Competitor history — tracks competitor changes over time
CREATE TABLE competitor_snapshots (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    competitor_id  UUID        NOT NULL REFERENCES competitors(id) ON DELETE CASCADE,
    price          BIGINT,
    rating         FLOAT,
    reviews_count  INT,
    position       INT,
    our_position   INT,
    captured_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_competitor_snapshots ON competitor_snapshots (competitor_id, captured_at DESC);

-- 3. Add paid/organic flag to SERP result items
ALTER TABLE serp_result_items ADD COLUMN IF NOT EXISTS is_promoted BOOLEAN DEFAULT false;
ALTER TABLE serp_result_items ADD COLUMN IF NOT EXISTS promo_type VARCHAR(50) DEFAULT '';
