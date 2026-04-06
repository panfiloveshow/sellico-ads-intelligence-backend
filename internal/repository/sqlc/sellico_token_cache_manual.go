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
