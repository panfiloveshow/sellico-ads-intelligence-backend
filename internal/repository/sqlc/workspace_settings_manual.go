package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const updateWorkspaceSettings = `-- name: UpdateWorkspaceSettings :one
UPDATE workspaces SET settings = $2, updated_at = now() WHERE id = $1 RETURNING id, name, slug, created_at, updated_at, deleted_at, settings
`

type UpdateWorkspaceSettingsParams struct {
	ID       pgtype.UUID `json:"id"`
	Settings []byte      `json:"settings"`
}

type WorkspaceWithSettings struct {
	ID        pgtype.UUID        `json:"id"`
	Name      string             `json:"name"`
	Slug      string             `json:"slug"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	UpdatedAt pgtype.Timestamptz `json:"updated_at"`
	DeletedAt pgtype.Timestamptz `json:"deleted_at"`
	Settings  []byte             `json:"settings"`
}

func (q *Queries) UpdateWorkspaceSettings(ctx context.Context, arg UpdateWorkspaceSettingsParams) (WorkspaceWithSettings, error) {
	row := q.db.QueryRow(ctx, updateWorkspaceSettings, arg.ID, arg.Settings)
	var i WorkspaceWithSettings
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.Slug,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.DeletedAt,
		&i.Settings,
	)
	return i, err
}

const getWorkspaceSettings = `-- name: GetWorkspaceSettings :one
SELECT settings FROM workspaces WHERE id = $1 AND deleted_at IS NULL
`

func (q *Queries) GetWorkspaceSettings(ctx context.Context, id pgtype.UUID) ([]byte, error) {
	row := q.db.QueryRow(ctx, getWorkspaceSettings, id)
	var settings []byte
	err := row.Scan(&settings)
	return settings, err
}
