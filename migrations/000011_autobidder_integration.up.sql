-- 000011_autobidder_integration.up.sql
-- Integrates autobidder functionality: strategies, bid changes, campaign phrases.

-- 1. Bid strategies (automation rules)
CREATE TABLE strategies (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID      NOT NULL REFERENCES seller_cabinets(id),
    name            VARCHAR(255) NOT NULL,
    type            VARCHAR(50) NOT NULL CHECK (type IN ('acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation')),
    params          JSONB       NOT NULL DEFAULT '{}',
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_strategies_workspace ON strategies (workspace_id) WHERE is_active = true;
CREATE INDEX idx_strategies_cabinet ON strategies (seller_cabinet_id) WHERE is_active = true;

-- 2. Strategy bindings (link strategy to campaign/product)
CREATE TABLE strategy_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    campaign_id UUID REFERENCES campaigns(id) ON DELETE CASCADE,
    product_id  UUID REFERENCES products(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_binding_target CHECK (campaign_id IS NOT NULL OR product_id IS NOT NULL)
);

CREATE INDEX idx_strategy_bindings_strategy ON strategy_bindings (strategy_id);
CREATE INDEX idx_strategy_bindings_campaign ON strategy_bindings (campaign_id) WHERE campaign_id IS NOT NULL;
CREATE INDEX idx_strategy_bindings_product ON strategy_bindings (product_id) WHERE product_id IS NOT NULL;

-- 3. Bid change history (audit trail for all bid modifications)
CREATE TABLE bid_changes (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id),
    campaign_id       UUID        NOT NULL REFERENCES campaigns(id),
    product_id        UUID        REFERENCES products(id),
    phrase_id         UUID        REFERENCES phrases(id),
    strategy_id       UUID        REFERENCES strategies(id),
    recommendation_id UUID        REFERENCES recommendations(id),
    placement         VARCHAR(20) NOT NULL CHECK (placement IN ('search', 'recommendations', 'carousel')),
    old_bid           INT         NOT NULL,
    new_bid           INT         NOT NULL,
    reason            TEXT        NOT NULL,
    source            VARCHAR(50) NOT NULL CHECK (source IN ('strategy', 'recommendation', 'manual')),
    acos              FLOAT,
    roas              FLOAT,
    wb_status         VARCHAR(20) DEFAULT 'pending',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bid_changes_workspace ON bid_changes (workspace_id, created_at DESC);
CREATE INDEX idx_bid_changes_campaign ON bid_changes (campaign_id, created_at DESC);
CREATE INDEX idx_bid_changes_strategy ON bid_changes (strategy_id, created_at DESC) WHERE strategy_id IS NOT NULL;

-- 4. Campaign minus phrases (negative keywords)
CREATE TABLE campaign_minus_phrases (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    phrase      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(campaign_id, phrase)
);

-- 5. Campaign plus phrases (target keywords)
CREATE TABLE campaign_plus_phrases (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    phrase      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(campaign_id, phrase)
);

-- 6. Add bid-related fields to existing products table
ALTER TABLE products ADD COLUMN IF NOT EXISTS current_bid_search INT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS current_bid_recommend INT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS min_bid_search INT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS min_bid_recommend INT DEFAULT 0;
ALTER TABLE products ADD COLUMN IF NOT EXISTS competitive_bid INT DEFAULT 0;
