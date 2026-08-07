-- Ozon module phase 3 (AI autopilot).

-- 1. One row per AI manager execution over a cabinet (cron or manual).
CREATE TABLE ai_runs (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    strategy_id       UUID        REFERENCES strategies(id) ON DELETE SET NULL,
    status            TEXT        NOT NULL DEFAULT 'running'
                      CHECK (status IN ('running', 'completed', 'failed', 'skipped')),
    trigger           TEXT        NOT NULL DEFAULT 'cron'
                      CHECK (trigger IN ('cron', 'manual')),
    summary           TEXT,
    error             TEXT,
    prompt_tokens     INT         DEFAULT 0,
    completion_tokens INT         DEFAULT 0,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ
);

CREATE INDEX idx_ai_runs_cabinet
    ON ai_runs (seller_cabinet_id, started_at DESC);

-- 2. Every proposal the model made, with the guardrail verdict and the
--    application outcome — the full AI audit trail. LLM never writes to Ozon
--    directly: applied decisions go through the same audited action paths as
--    manual writes (ozon_bid_changes rows carry source='ai').
CREATE TABLE ai_decisions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id            UUID        NOT NULL REFERENCES ai_runs(id) ON DELETE CASCADE,
    workspace_id      UUID        NOT NULL,
    seller_cabinet_id UUID        NOT NULL,
    action_type       TEXT        NOT NULL CHECK (action_type IN (
                          'bid_change', 'budget_change', 'campaign_pause',
                          'campaign_activate', 'cpo_bid', 'cpo_enable', 'cpo_disable')),
    target            JSONB       NOT NULL,
    proposal          JSONB       NOT NULL,
    rationale         TEXT,
    expected_effect   TEXT,
    guardrail_verdict TEXT        NOT NULL DEFAULT 'passed',
    status            TEXT        NOT NULL CHECK (status IN (
                          'shadow', 'proposed', 'approved', 'auto_applied', 'applied',
                          'failed', 'rejected_by_user', 'rejected_by_guardrail')),
    error             TEXT,
    created_at        TIMESTAMPTZ DEFAULT now(),
    applied_at        TIMESTAMPTZ,
    applied_by        UUID
);

CREATE INDEX idx_ai_decisions_cabinet
    ON ai_decisions (seller_cabinet_id, created_at DESC);
CREATE INDEX idx_ai_decisions_run
    ON ai_decisions (run_id);
CREATE INDEX idx_ai_decisions_proposed
    ON ai_decisions (seller_cabinet_id)
    WHERE status = 'proposed';

-- 3. strategies.type: recreate with the full 000049 list + ozon_ai_autopilot.
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr',
    'ozon_ai_autopilot'
));

-- 4. ozon_bid_changes.source: AI-applied writes are audited as source='ai'.
ALTER TABLE ozon_bid_changes DROP CONSTRAINT IF EXISTS ozon_bid_changes_source_check;
ALTER TABLE ozon_bid_changes ADD CONSTRAINT ozon_bid_changes_source_check
    CHECK (source IN ('strategy', 'manual', 'ai'));
