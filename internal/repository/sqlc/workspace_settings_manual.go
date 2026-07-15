package sqlcgen

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const workspaceSettingsUpdateAdvisoryLock = `SELECT pg_advisory_xact_lock(
	hashtextextended($1::text || ':workspace-daily-bid-actions', 0)
)`

type workspaceSettingsTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// BeginWorkspaceSettingsUpdateTx acquires the automation/settings lock before
// callers read the current JSON. This makes read-merge-write updates serial and
// prevents an unrelated PUT from restoring a stale cap or manual_hold value.
func (q *Queries) BeginWorkspaceSettingsUpdateTx(ctx context.Context, workspaceID pgtype.UUID) (*Queries, pgx.Tx, error) {
	beginner, ok := q.db.(workspaceSettingsTxBeginner)
	if !ok {
		return nil, nil, fmt.Errorf("database does not support workspace settings transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, workspaceSettingsUpdateAdvisoryLock, workspaceID); err != nil {
		_ = tx.Rollback(context.Background())
		return nil, nil, err
	}
	return q.WithTx(tx), tx, nil
}

const updateWorkspaceSettings = `-- name: UpdateWorkspaceSettings :one
WITH automation_lock AS MATERIALIZED (
	SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':workspace-daily-bid-actions', 0))
)
UPDATE workspaces w
SET settings = $2, updated_at = now()
FROM automation_lock
WHERE w.id = $1
RETURNING w.id, w.name, w.slug, w.created_at, w.updated_at, w.deleted_at, w.settings
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
