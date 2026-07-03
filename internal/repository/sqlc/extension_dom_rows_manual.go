package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type ExtensionDomRowSnapshot struct {
	ID              pgtype.UUID        `json:"id"`
	SessionID       pgtype.UUID        `json:"session_id"`
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	UserID          pgtype.UUID        `json:"user_id"`
	SellerCabinetID pgtype.UUID        `json:"seller_cabinet_id"`
	CampaignID      pgtype.UUID        `json:"campaign_id"`
	PhraseID        pgtype.UUID        `json:"phrase_id"`
	ProductID       pgtype.UUID        `json:"product_id"`
	PageType        string             `json:"page_type"`
	TableRole       string             `json:"table_role"`
	RowKey          string             `json:"row_key"`
	Query           pgtype.Text        `json:"query"`
	Region          pgtype.Text        `json:"region"`
	VisibleText     string             `json:"visible_text"`
	Cells           []byte             `json:"cells"`
	Metadata        []byte             `json:"metadata"`
	Source          string             `json:"source"`
	Confidence      pgtype.Numeric     `json:"confidence"`
	DedupeKey       string             `json:"dedupe_key"`
	CapturedAt      pgtype.Timestamptz `json:"captured_at"`
	CreatedAt       pgtype.Timestamptz `json:"created_at"`
}

const createExtensionDOMRowSnapshot = `
INSERT INTO extension_dom_row_snapshots (
    session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id, product_id,
    page_type, table_role, row_key, query, region, visible_text, cells, metadata,
    source, confidence, dedupe_key, captured_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14, $15,
    $16, $17, $18, $19
)
ON CONFLICT (workspace_id, dedupe_key) DO UPDATE
SET visible_text = EXCLUDED.visible_text,
    cells = COALESCE(EXCLUDED.cells, extension_dom_row_snapshots.cells),
    metadata = COALESCE(EXCLUDED.metadata, extension_dom_row_snapshots.metadata),
    confidence = EXCLUDED.confidence,
    captured_at = GREATEST(extension_dom_row_snapshots.captured_at, EXCLUDED.captured_at)
RETURNING id, session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id, product_id,
    page_type, table_role, row_key, query, region, visible_text, cells, metadata,
    source, confidence, dedupe_key, captured_at, created_at
`

type CreateExtensionDOMRowSnapshotParams struct {
	SessionID       pgtype.UUID        `json:"session_id"`
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	UserID          pgtype.UUID        `json:"user_id"`
	SellerCabinetID pgtype.UUID        `json:"seller_cabinet_id"`
	CampaignID      pgtype.UUID        `json:"campaign_id"`
	PhraseID        pgtype.UUID        `json:"phrase_id"`
	ProductID       pgtype.UUID        `json:"product_id"`
	PageType        string             `json:"page_type"`
	TableRole       string             `json:"table_role"`
	RowKey          string             `json:"row_key"`
	Query           pgtype.Text        `json:"query"`
	Region          pgtype.Text        `json:"region"`
	VisibleText     string             `json:"visible_text"`
	Cells           []byte             `json:"cells"`
	Metadata        []byte             `json:"metadata"`
	Source          string             `json:"source"`
	Confidence      pgtype.Numeric     `json:"confidence"`
	DedupeKey       string             `json:"dedupe_key"`
	CapturedAt      pgtype.Timestamptz `json:"captured_at"`
}

func (q *Queries) CreateExtensionDOMRowSnapshot(ctx context.Context, arg CreateExtensionDOMRowSnapshotParams) (ExtensionDomRowSnapshot, error) {
	row := q.db.QueryRow(ctx, createExtensionDOMRowSnapshot,
		arg.SessionID,
		arg.WorkspaceID,
		arg.UserID,
		arg.SellerCabinetID,
		arg.CampaignID,
		arg.PhraseID,
		arg.ProductID,
		arg.PageType,
		arg.TableRole,
		arg.RowKey,
		arg.Query,
		arg.Region,
		arg.VisibleText,
		arg.Cells,
		arg.Metadata,
		arg.Source,
		arg.Confidence,
		arg.DedupeKey,
		arg.CapturedAt,
	)
	var i ExtensionDomRowSnapshot
	err := row.Scan(
		&i.ID,
		&i.SessionID,
		&i.WorkspaceID,
		&i.UserID,
		&i.SellerCabinetID,
		&i.CampaignID,
		&i.PhraseID,
		&i.ProductID,
		&i.PageType,
		&i.TableRole,
		&i.RowKey,
		&i.Query,
		&i.Region,
		&i.VisibleText,
		&i.Cells,
		&i.Metadata,
		&i.Source,
		&i.Confidence,
		&i.DedupeKey,
		&i.CapturedAt,
		&i.CreatedAt,
	)
	return i, err
}

const listExtensionDOMRowSnapshotsFiltered = `
SELECT id, session_id, workspace_id, user_id, seller_cabinet_id, campaign_id, phrase_id, product_id,
    page_type, table_role, row_key, query, region, visible_text, cells, metadata,
    source, confidence, dedupe_key, captured_at, created_at
FROM extension_dom_row_snapshots
WHERE workspace_id = $1
  AND ($4::text IS NULL OR page_type = $4::text)
  AND ($5::text IS NULL OR table_role = $5::text)
  AND ($6::uuid IS NULL OR campaign_id = $6::uuid)
  AND ($7::uuid IS NULL OR phrase_id = $7::uuid)
  AND ($8::uuid IS NULL OR product_id = $8::uuid)
  AND ($9::text IS NULL OR query = $9::text)
  AND ($10::text IS NULL OR region = $10::text)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3
`

type ListExtensionDOMRowSnapshotsFilteredParams struct {
	WorkspaceID      pgtype.UUID `json:"workspace_id"`
	Limit            int32       `json:"limit"`
	Offset           int32       `json:"offset"`
	PageTypeFilter   pgtype.Text `json:"page_type_filter"`
	TableRoleFilter  pgtype.Text `json:"table_role_filter"`
	CampaignIDFilter pgtype.UUID `json:"campaign_id_filter"`
	PhraseIDFilter   pgtype.UUID `json:"phrase_id_filter"`
	ProductIDFilter  pgtype.UUID `json:"product_id_filter"`
	QueryFilter      pgtype.Text `json:"query_filter"`
	RegionFilter     pgtype.Text `json:"region_filter"`
}

func (q *Queries) ListExtensionDOMRowSnapshotsFiltered(ctx context.Context, arg ListExtensionDOMRowSnapshotsFilteredParams) ([]ExtensionDomRowSnapshot, error) {
	rows, err := q.db.Query(ctx, listExtensionDOMRowSnapshotsFiltered,
		arg.WorkspaceID,
		arg.Limit,
		arg.Offset,
		arg.PageTypeFilter,
		arg.TableRoleFilter,
		arg.CampaignIDFilter,
		arg.PhraseIDFilter,
		arg.ProductIDFilter,
		arg.QueryFilter,
		arg.RegionFilter,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ExtensionDomRowSnapshot{}
	for rows.Next() {
		var i ExtensionDomRowSnapshot
		if err := rows.Scan(
			&i.ID,
			&i.SessionID,
			&i.WorkspaceID,
			&i.UserID,
			&i.SellerCabinetID,
			&i.CampaignID,
			&i.PhraseID,
			&i.ProductID,
			&i.PageType,
			&i.TableRole,
			&i.RowKey,
			&i.Query,
			&i.Region,
			&i.VisibleText,
			&i.Cells,
			&i.Metadata,
			&i.Source,
			&i.Confidence,
			&i.DedupeKey,
			&i.CapturedAt,
			&i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
