CREATE TABLE extension_context_events (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID         NOT NULL REFERENCES extension_sessions(id),
    workspace_id UUID         NOT NULL REFERENCES workspaces(id),
    user_id      UUID         NOT NULL REFERENCES users(id),
    url          TEXT         NOT NULL,
    page_type    VARCHAR(20)  NOT NULL,
    metadata     JSONB        NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_extension_context_events_session_id ON extension_context_events (session_id);
CREATE INDEX idx_extension_context_events_workspace_id ON extension_context_events (workspace_id);
CREATE INDEX idx_extension_context_events_user_id ON extension_context_events (user_id);
CREATE INDEX idx_extension_context_events_created_at ON extension_context_events (created_at);
