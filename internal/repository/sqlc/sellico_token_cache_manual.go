package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const cacheSellicoToken = `
UPDATE workspaces
SET encrypted_sellico_token = $2, sellico_token_updated_at = now(), updated_at = now()
WHERE id = $1
`

func (q *Queries) CacheSellicoToken(ctx context.Context, workspaceID pgtype.UUID, encryptedToken string) error {
	_, err := q.db.Exec(ctx, cacheSellicoToken, workspaceID, encryptedToken)
	return err
}

type SellicoTokenCache struct {
	WorkspaceID         pgtype.UUID        `json:"workspace_id"`
	EncryptedToken      pgtype.Text        `json:"encrypted_sellico_token"`
	TokenUpdatedAt      pgtype.Timestamptz `json:"sellico_token_updated_at"`
	ExternalWorkspaceID pgtype.Text        `json:"external_workspace_id"`
	Source              string             `json:"source"`
}

const listWorkspacesWithSellicoToken = `
SELECT id, encrypted_sellico_token, sellico_token_updated_at, external_workspace_id, source
FROM workspaces
WHERE deleted_at IS NULL
  AND source = 'sellico'
  AND encrypted_sellico_token IS NOT NULL
  AND encrypted_sellico_token != ''
ORDER BY sellico_token_updated_at DESC NULLS LAST
LIMIT $1
`

func (q *Queries) ListWorkspacesWithSellicoToken(ctx context.Context, limit int32) ([]SellicoTokenCache, error) {
	rows, err := q.db.Query(ctx, listWorkspacesWithSellicoToken, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SellicoTokenCache
	for rows.Next() {
		var i SellicoTokenCache
		if err := rows.Scan(&i.WorkspaceID, &i.EncryptedToken, &i.TokenUpdatedAt, &i.ExternalWorkspaceID, &i.Source); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// WorkspaceExternalID is the projection used by the service-account discovery
// path: just enough fields to map a local workspace to its Sellico
// work_space_id and call /api/get-integrations/{ws}.
type WorkspaceExternalID struct {
	WorkspaceID         pgtype.UUID `json:"workspace_id"`
	ExternalWorkspaceID pgtype.Text `json:"external_workspace_id"`
}

const listWorkspacesWithExternalID = `
SELECT id, external_workspace_id
FROM workspaces
WHERE deleted_at IS NULL
  AND external_workspace_id IS NOT NULL
  AND external_workspace_id != ''
ORDER BY updated_at DESC NULLS LAST
LIMIT $1
`

// ListWorkspacesWithExternalID returns every local workspace that knows its
// Sellico work_space_id. Used by IntegrationRefreshService.RefreshViaServiceAccount
// to ask the upstream "what integrations does this tenant have?" without
// needing the tenant's personal token.
func (q *Queries) ListWorkspacesWithExternalID(ctx context.Context, limit int32) ([]WorkspaceExternalID, error) {
	rows, err := q.db.Query(ctx, listWorkspacesWithExternalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []WorkspaceExternalID
	for rows.Next() {
		var i WorkspaceExternalID
		if err := rows.Scan(&i.WorkspaceID, &i.ExternalWorkspaceID); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
