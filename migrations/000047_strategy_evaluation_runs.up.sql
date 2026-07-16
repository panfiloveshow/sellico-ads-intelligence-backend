-- Durable, per-binding autopilot evaluations. Live strategies are allowed to
-- explain every hold/skip without fabricating a WB action.
CREATE TABLE strategy_binding_rollouts (
    binding_id           UUID PRIMARY KEY REFERENCES strategy_bindings(id) ON DELETE CASCADE,
    workspace_id         UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    strategy_id          UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    desired_mode         TEXT NOT NULL DEFAULT 'shadow'
                         CHECK (desired_mode IN ('live', 'shadow')),
    state                TEXT NOT NULL DEFAULT 'shadow_validating'
                         CHECK (state IN ('shadow_validating', 'live', 'blocked', 'manual_hold')),
    hold_reason          TEXT NULL,
    last_block_code      TEXT NULL,
    last_block_detail    TEXT NULL,
    validation_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    live_enabled_at      TIMESTAMPTZ NULL,
    updated_by           UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_strategy_binding_rollouts_workspace
    ON strategy_binding_rollouts (workspace_id, strategy_id, state);

-- Preserve explicit existing live intent during rollout of this schema. New
-- bindings are initialized by StrategyService as shadow_validating.
INSERT INTO strategy_binding_rollouts (binding_id, workspace_id, strategy_id, desired_mode, state, live_enabled_at)
SELECT b.id, s.workspace_id, s.id,
       CASE WHEN s.is_active AND COALESCE(NULLIF((s.params->>'automation_level')::int, 0), 1) >= 3
            THEN 'live' ELSE 'shadow' END,
       'shadow_validating', NULL
FROM strategy_bindings b
JOIN strategies s ON s.id = b.strategy_id
ON CONFLICT (binding_id) DO NOTHING;

CREATE TABLE strategy_evaluation_runs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    seller_cabinet_id  UUID NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    strategy_id        UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    strategy_binding_id UUID NOT NULL REFERENCES strategy_bindings(id) ON DELETE CASCADE,
    campaign_id        UUID NULL REFERENCES campaigns(id) ON DELETE SET NULL,
    product_id         UUID NULL REFERENCES products(id) ON DELETE SET NULL,
    automation_level   INT NOT NULL CHECK (automation_level BETWEEN 1 AND 4),
    rollout_state      TEXT NOT NULL CHECK (rollout_state IN ('shadow_validating', 'live', 'blocked', 'manual_hold')),
    apply_requested    BOOLEAN NOT NULL DEFAULT false,
    outcome            TEXT NOT NULL DEFAULT 'evaluating'
                       CHECK (outcome IN ('evaluating', 'no_decision', 'blocked', 'shadow_decision', 'claimed', 'applied', 'failed', 'unknown')),
    reason_code        TEXT NOT NULL DEFAULT 'evaluation_started',
    reason_detail      TEXT NULL,
    proposed_actions   INT NOT NULL DEFAULT 0,
    applied_actions    INT NOT NULL DEFAULT 0,
    started_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at        TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_strategy_evaluation_runs_strategy
    ON strategy_evaluation_runs (workspace_id, strategy_id, started_at DESC);
CREATE INDEX idx_strategy_evaluation_runs_binding
    ON strategy_evaluation_runs (strategy_binding_id, started_at DESC);

CREATE TABLE strategy_evaluation_facts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id         UUID NOT NULL REFERENCES strategy_evaluation_runs(id) ON DELETE CASCADE,
    code           TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('observed', 'passed', 'blocked', 'error')),
    outcome        TEXT NOT NULL CHECK (outcome IN ('blocked', 'ready_no_change', 'would_apply', 'claimed', 'applied', 'failed', 'unknown')),
    source         TEXT NOT NULL,
    value          JSONB NOT NULL DEFAULT '{}',
    observed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_strategy_evaluation_facts_run
    ON strategy_evaluation_facts (run_id, observed_at, id);

-- A live-level strategy can be evaluated in per-binding shadow validation.
ALTER TABLE bid_decision_observations
    DROP CONSTRAINT IF EXISTS bid_decision_observations_automation_level_check;
ALTER TABLE bid_decision_observations
    ADD CONSTRAINT bid_decision_observations_automation_level_check
    CHECK (automation_level BETWEEN 1 AND 4);
