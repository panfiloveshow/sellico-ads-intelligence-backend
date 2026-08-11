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



type WorkspaceWithSettings struct {
	ID        pgtype.UUID        `json:"id"`
	Name      string             `json:"name"`
	Slug      string             `json:"slug"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	UpdatedAt pgtype.Timestamptz `json:"updated_at"`
	DeletedAt pgtype.Timestamptz `json:"deleted_at"`
	Settings  []byte             `json:"settings"`
}



