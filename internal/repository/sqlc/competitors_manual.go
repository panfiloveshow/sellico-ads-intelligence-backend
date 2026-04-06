package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type CompetitorRow struct {
	ID                     pgtype.UUID        `json:"id"`
	WorkspaceID            pgtype.UUID        `json:"workspace_id"`
	ProductID              pgtype.UUID        `json:"product_id"`
	CompetitorNmID         int64              `json:"competitor_nm_id"`
	CompetitorTitle        string             `json:"competitor_title"`
	CompetitorBrand        pgtype.Text        `json:"competitor_brand"`
	CompetitorPrice        int64              `json:"competitor_price"`
	CompetitorRating       float64            `json:"competitor_rating"`
	CompetitorReviewsCount int32              `json:"competitor_reviews_count"`
	Query                  string             `json:"query"`
	Region                 string             `json:"region"`
	LastPosition           int32              `json:"last_position"`
	OurPosition            int32              `json:"our_position"`
	FirstSeenAt            pgtype.Timestamptz `json:"first_seen_at"`
	LastSeenAt             pgtype.Timestamptz `json:"last_seen_at"`
	Source                 string             `json:"source"`
	CreatedAt              pgtype.Timestamptz `json:"created_at"`
	UpdatedAt              pgtype.Timestamptz `json:"updated_at"`
}

type UpsertCompetitorParams struct {
	WorkspaceID            pgtype.UUID
	ProductID              pgtype.UUID
	CompetitorNmID         int64
	CompetitorTitle        string
	CompetitorBrand        pgtype.Text
	CompetitorPrice        int64
	CompetitorRating       float64
	CompetitorReviewsCount int32
	Query                  string
	Region                 string
	LastPosition           int32
	OurPosition            int32
}

func (q *Queries) UpsertCompetitor(ctx context.Context, arg UpsertCompetitorParams) (CompetitorRow, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO competitors (workspace_id, product_id, competitor_nm_id, competitor_title, competitor_brand, competitor_price, competitor_rating, competitor_reviews_count, query, region, last_position, our_position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (workspace_id, product_id, competitor_nm_id, query) DO UPDATE SET
			competitor_title = EXCLUDED.competitor_title,
			competitor_price = EXCLUDED.competitor_price,
			competitor_rating = EXCLUDED.competitor_rating,
			competitor_reviews_count = EXCLUDED.competitor_reviews_count,
			last_position = EXCLUDED.last_position,
			our_position = EXCLUDED.our_position,
			last_seen_at = now(),
			updated_at = now()
		RETURNING id, workspace_id, product_id, competitor_nm_id, competitor_title, competitor_brand, competitor_price, competitor_rating, competitor_reviews_count, query, region, last_position, our_position, first_seen_at, last_seen_at, source, created_at, updated_at`,
		arg.WorkspaceID, arg.ProductID, arg.CompetitorNmID, arg.CompetitorTitle, arg.CompetitorBrand,
		arg.CompetitorPrice, arg.CompetitorRating, arg.CompetitorReviewsCount, arg.Query, arg.Region,
		arg.LastPosition, arg.OurPosition)
	var i CompetitorRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.CompetitorNmID, &i.CompetitorTitle, &i.CompetitorBrand,
		&i.CompetitorPrice, &i.CompetitorRating, &i.CompetitorReviewsCount, &i.Query, &i.Region,
		&i.LastPosition, &i.OurPosition, &i.FirstSeenAt, &i.LastSeenAt, &i.Source, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type ListCompetitorsByProductParams struct {
	ProductID pgtype.UUID
	Limit     int32
	Offset    int32
}

func (q *Queries) GetCompetitorByID(ctx context.Context, id pgtype.UUID) (CompetitorRow, error) {
	row := q.db.QueryRow(ctx,
		`SELECT id, workspace_id, product_id, competitor_nm_id, competitor_title, competitor_brand, competitor_price, competitor_rating, competitor_reviews_count, query, region, last_position, our_position, first_seen_at, last_seen_at, source, created_at, updated_at
		FROM competitors WHERE id = $1`, id)
	var i CompetitorRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.CompetitorNmID, &i.CompetitorTitle, &i.CompetitorBrand, &i.CompetitorPrice, &i.CompetitorRating, &i.CompetitorReviewsCount, &i.Query, &i.Region, &i.LastPosition, &i.OurPosition, &i.FirstSeenAt, &i.LastSeenAt, &i.Source, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type CompetitorSnapshotRow struct {
	ID           pgtype.UUID        `json:"id"`
	CompetitorID pgtype.UUID        `json:"competitor_id"`
	Price        int64              `json:"price"`
	Rating       float64            `json:"rating"`
	ReviewsCount int32              `json:"reviews_count"`
	Position     int32              `json:"position"`
	OurPosition  int32              `json:"our_position"`
	CapturedAt   pgtype.Timestamptz `json:"captured_at"`
}

func (q *Queries) ListCompetitorSnapshots(ctx context.Context, competitorID pgtype.UUID, limit int32) ([]CompetitorSnapshotRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, competitor_id, price, rating, reviews_count, position, our_position, captured_at
		FROM competitor_snapshots WHERE competitor_id = $1 ORDER BY captured_at DESC LIMIT $2`,
		competitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CompetitorSnapshotRow
	for rows.Next() {
		var i CompetitorSnapshotRow
		if err := rows.Scan(&i.ID, &i.CompetitorID, &i.Price, &i.Rating, &i.ReviewsCount, &i.Position, &i.OurPosition, &i.CapturedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) ListCompetitorsByProduct(ctx context.Context, arg ListCompetitorsByProductParams) ([]CompetitorRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, workspace_id, product_id, competitor_nm_id, competitor_title, competitor_brand, competitor_price, competitor_rating, competitor_reviews_count, query, region, last_position, our_position, first_seen_at, last_seen_at, source, created_at, updated_at
		FROM competitors WHERE product_id = $1 ORDER BY last_seen_at DESC LIMIT $2 OFFSET $3`,
		arg.ProductID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CompetitorRow
	for rows.Next() {
		var i CompetitorRow
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.CompetitorNmID, &i.CompetitorTitle, &i.CompetitorBrand, &i.CompetitorPrice, &i.CompetitorRating, &i.CompetitorReviewsCount, &i.Query, &i.Region, &i.LastPosition, &i.OurPosition, &i.FirstSeenAt, &i.LastSeenAt, &i.Source, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type ListCompetitorsByWorkspaceParams struct {
	WorkspaceID pgtype.UUID
	Limit       int32
	Offset      int32
}

func (q *Queries) ListCompetitorsByWorkspace(ctx context.Context, arg ListCompetitorsByWorkspaceParams) ([]CompetitorRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, workspace_id, product_id, competitor_nm_id, competitor_title, competitor_brand, competitor_price, competitor_rating, competitor_reviews_count, query, region, last_position, our_position, first_seen_at, last_seen_at, source, created_at, updated_at
		FROM competitors WHERE workspace_id = $1 ORDER BY last_seen_at DESC LIMIT $2 OFFSET $3`,
		arg.WorkspaceID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CompetitorRow
	for rows.Next() {
		var i CompetitorRow
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.CompetitorNmID, &i.CompetitorTitle, &i.CompetitorBrand, &i.CompetitorPrice, &i.CompetitorRating, &i.CompetitorReviewsCount, &i.Query, &i.Region, &i.LastPosition, &i.OurPosition, &i.FirstSeenAt, &i.LastSeenAt, &i.Source, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
