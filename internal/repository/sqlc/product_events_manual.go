package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type ProductEvent struct {
	ID          pgtype.UUID        `json:"id"`
	WorkspaceID pgtype.UUID        `json:"workspace_id"`
	ProductID   pgtype.UUID        `json:"product_id"`
	EventType   string             `json:"event_type"`
	FieldName   pgtype.Text        `json:"field_name"`
	OldValue    pgtype.Text        `json:"old_value"`
	NewValue    pgtype.Text        `json:"new_value"`
	Metadata    []byte             `json:"metadata"`
	DetectedAt  pgtype.Timestamptz `json:"detected_at"`
	Source      string             `json:"source"`
}

type CreateProductEventParams struct {
	WorkspaceID pgtype.UUID
	ProductID   pgtype.UUID
	EventType   string
	FieldName   pgtype.Text
	OldValue    pgtype.Text
	NewValue    pgtype.Text
	Metadata    []byte
	Source      string
}

func (q *Queries) CreateProductEvent(ctx context.Context, arg CreateProductEventParams) (ProductEvent, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO product_events (workspace_id, product_id, event_type, field_name, old_value, new_value, metadata, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, workspace_id, product_id, event_type, field_name, old_value, new_value, metadata, detected_at, source`,
		arg.WorkspaceID, arg.ProductID, arg.EventType, arg.FieldName, arg.OldValue, arg.NewValue, arg.Metadata, arg.Source)
	var i ProductEvent
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.EventType, &i.FieldName, &i.OldValue, &i.NewValue, &i.Metadata, &i.DetectedAt, &i.Source)
	return i, err
}

type ListProductEventsParams struct {
	ProductID pgtype.UUID
	Limit     int32
	Offset    int32
}

func (q *Queries) ListProductEvents(ctx context.Context, arg ListProductEventsParams) ([]ProductEvent, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, workspace_id, product_id, event_type, field_name, old_value, new_value, metadata, detected_at, source
		FROM product_events WHERE product_id = $1 ORDER BY detected_at DESC LIMIT $2 OFFSET $3`,
		arg.ProductID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ProductEvent
	for rows.Next() {
		var i ProductEvent
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.EventType, &i.FieldName, &i.OldValue, &i.NewValue, &i.Metadata, &i.DetectedAt, &i.Source); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type ListProductEventsByWorkspaceParams struct {
	WorkspaceID pgtype.UUID
	EventType   pgtype.Text
	Limit       int32
	Offset      int32
}

func (q *Queries) ListProductEventsByWorkspace(ctx context.Context, arg ListProductEventsByWorkspaceParams) ([]ProductEvent, error) {
	query := `SELECT id, workspace_id, product_id, event_type, field_name, old_value, new_value, metadata, detected_at, source
		FROM product_events WHERE workspace_id = $1`
	args := []any{arg.WorkspaceID}

	if arg.EventType.Valid && arg.EventType.String != "" {
		query += ` AND event_type = $4`
		args = append(args, arg.EventType)
	}

	query += ` ORDER BY detected_at DESC LIMIT $2 OFFSET $3`
	args = append(args, arg.Limit, arg.Offset)

	// Fix arg positions — build cleanly
	var finalArgs []any
	finalArgs = append(finalArgs, arg.WorkspaceID)
	baseQuery := `SELECT id, workspace_id, product_id, event_type, field_name, old_value, new_value, metadata, detected_at, source FROM product_events WHERE workspace_id = $1`

	if arg.EventType.Valid && arg.EventType.String != "" {
		baseQuery += ` AND event_type = $2 ORDER BY detected_at DESC LIMIT $3 OFFSET $4`
		finalArgs = append(finalArgs, arg.EventType, arg.Limit, arg.Offset)
	} else {
		baseQuery += ` ORDER BY detected_at DESC LIMIT $2 OFFSET $3`
		finalArgs = append(finalArgs, arg.Limit, arg.Offset)
	}

	rows, err := q.db.Query(ctx, baseQuery, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ProductEvent
	for rows.Next() {
		var i ProductEvent
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.EventType, &i.FieldName, &i.OldValue, &i.NewValue, &i.Metadata, &i.DetectedAt, &i.Source); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type ProductSnapshotRecord struct {
	ID           pgtype.UUID        `json:"id"`
	ProductID    pgtype.UUID        `json:"product_id"`
	Title        pgtype.Text        `json:"title"`
	Brand        pgtype.Text        `json:"brand"`
	Category     pgtype.Text        `json:"category"`
	Price        pgtype.Int8        `json:"price"`
	Rating       pgtype.Float8      `json:"rating"`
	ReviewsCount pgtype.Int4        `json:"reviews_count"`
	StockTotal   pgtype.Int4        `json:"stock_total"`
	ImageURL     pgtype.Text        `json:"image_url"`
	ContentHash  pgtype.Text        `json:"content_hash"`
	CapturedAt   pgtype.Timestamptz `json:"captured_at"`
}

func (q *Queries) GetLatestProductSnapshot(ctx context.Context, productID pgtype.UUID) (ProductSnapshotRecord, error) {
	row := q.db.QueryRow(ctx,
		`SELECT id, product_id, title, brand, category, price, rating, reviews_count, stock_total, image_url, content_hash, captured_at
		FROM product_snapshots WHERE product_id = $1 ORDER BY captured_at DESC LIMIT 1`, productID)
	var i ProductSnapshotRecord
	err := row.Scan(&i.ID, &i.ProductID, &i.Title, &i.Brand, &i.Category, &i.Price, &i.Rating, &i.ReviewsCount, &i.StockTotal, &i.ImageURL, &i.ContentHash, &i.CapturedAt)
	return i, err
}

type ProductStockEvidenceRow struct {
	ProductID  pgtype.UUID        `json:"product_id"`
	StockTotal int32              `json:"stock_total"`
	Source     string             `json:"source"`
	CapturedAt pgtype.Timestamptz `json:"captured_at"`
}

func (q *Queries) ListLatestProductStockEvidenceByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]ProductStockEvidenceRow, error) {
	rows, err := q.db.Query(ctx, `
		WITH latest_snapshots AS (
			SELECT DISTINCT ON (ps.product_id)
				ps.product_id,
				ps.stock_total::int AS stock_total,
				ps.captured_at
			FROM product_snapshots ps
			JOIN products p ON p.id = ps.product_id
			WHERE p.workspace_id = $1
				AND ps.stock_total IS NOT NULL
			ORDER BY ps.product_id, ps.captured_at DESC
		),
		latest_delivery AS (
			SELECT DISTINCT ON (dd.product_id)
				dd.product_id,
				0::int AS stock_total,
				dd.captured_at
			FROM delivery_data dd
			WHERE dd.workspace_id = $1
				AND dd.in_stock = false
			ORDER BY dd.product_id, dd.captured_at DESC
		)
		SELECT product_id, stock_total, 'product_snapshot' AS source, captured_at
		FROM latest_snapshots
		UNION ALL
		SELECT ld.product_id, ld.stock_total, 'delivery_data' AS source, ld.captured_at
		FROM latest_delivery ld
		WHERE NOT EXISTS (
			SELECT 1 FROM latest_snapshots ls WHERE ls.product_id = ld.product_id
		)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProductStockEvidenceRow
	for rows.Next() {
		var i ProductStockEvidenceRow
		if err := rows.Scan(&i.ProductID, &i.StockTotal, &i.Source, &i.CapturedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type CreateProductSnapshotParams struct {
	ProductID    pgtype.UUID
	Title        pgtype.Text
	Brand        pgtype.Text
	Category     pgtype.Text
	Price        pgtype.Int8
	Rating       pgtype.Float8
	ReviewsCount pgtype.Int4
	StockTotal   pgtype.Int4
	ImageURL     pgtype.Text
	ContentHash  pgtype.Text
}

func (q *Queries) CreateProductSnapshot(ctx context.Context, arg CreateProductSnapshotParams) error {
	_, err := q.db.Exec(ctx,
		`INSERT INTO product_snapshots (product_id, title, brand, category, price, rating, reviews_count, stock_total, image_url, content_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		arg.ProductID, arg.Title, arg.Brand, arg.Category, arg.Price, arg.Rating, arg.ReviewsCount, arg.StockTotal, arg.ImageURL, arg.ContentHash)
	return err
}
