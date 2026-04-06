-- 000013_semantics_seo.up.sql
-- Semantics, keyword research, and SEO analysis.

-- 1. Keywords — collected search queries with frequency and metadata
CREATE TABLE keywords (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    query         TEXT        NOT NULL,
    normalized    TEXT        NOT NULL,
    frequency     INT         DEFAULT 0,
    frequency_trend VARCHAR(20) DEFAULT 'stable',
    cluster_id    UUID,
    source        VARCHAR(50) NOT NULL DEFAULT 'wb_api',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, normalized)
);

CREATE INDEX idx_keywords_workspace ON keywords (workspace_id);
CREATE INDEX idx_keywords_cluster ON keywords (cluster_id) WHERE cluster_id IS NOT NULL;
CREATE INDEX idx_keywords_frequency ON keywords (workspace_id, frequency DESC);

-- 2. Keyword frequency history — tracks frequency over time
CREATE TABLE keyword_frequency_history (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    keyword_id  UUID        NOT NULL REFERENCES keywords(id) ON DELETE CASCADE,
    frequency   INT         NOT NULL,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_keyword_freq_history ON keyword_frequency_history (keyword_id, checked_at DESC);

-- 3. Keyword clusters — groups of related queries
CREATE TABLE keyword_clusters (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID         NOT NULL REFERENCES workspaces(id),
    name         VARCHAR(255) NOT NULL,
    main_keyword TEXT         NOT NULL,
    keyword_count INT         DEFAULT 0,
    total_frequency INT       DEFAULT 0,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_keyword_clusters_workspace ON keyword_clusters (workspace_id);

-- 4. SEO analysis results per product
CREATE TABLE seo_analyses (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    product_id        UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    title_score       INT         DEFAULT 0,
    description_score INT         DEFAULT 0,
    keywords_score    INT         DEFAULT 0,
    overall_score     INT         DEFAULT 0,
    title_issues      JSONB       DEFAULT '[]',
    description_issues JSONB      DEFAULT '[]',
    keyword_coverage  JSONB       DEFAULT '{}',
    recommendations   JSONB       DEFAULT '[]',
    analyzed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_seo_analyses_product ON seo_analyses (product_id, analyzed_at DESC);
CREATE INDEX idx_seo_analyses_workspace ON seo_analyses (workspace_id, overall_score ASC);

-- 5. Related keywords mapping
CREATE TABLE keyword_relations (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    keyword_id    UUID        NOT NULL REFERENCES keywords(id) ON DELETE CASCADE,
    related_id    UUID        NOT NULL REFERENCES keywords(id) ON DELETE CASCADE,
    relation_type VARCHAR(30) NOT NULL DEFAULT 'related',
    strength      FLOAT       DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(keyword_id, related_id)
);
