CREATE TABLE seller_cabinet_sync_states (
    seller_cabinet_id UUID PRIMARY KEY REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('running', 'ready', 'partial', 'failed')),
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    data_through_date DATE,
    issue_count INTEGER NOT NULL DEFAULT 0,
    wb_error_count INTEGER NOT NULL DEFAULT 0,
    rate_limited BOOLEAN NOT NULL DEFAULT FALSE,
    retry_after_seconds INTEGER,
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX seller_cabinet_sync_states_workspace_idx
    ON seller_cabinet_sync_states (workspace_id, status, completed_at DESC);
