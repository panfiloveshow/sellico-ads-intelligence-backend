package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type stockEvidenceTestDB struct {
	snapshot *sqlcgen.ProductSnapshotRecord
	delivery *sqlcgen.DeliveryDataRow
}

func (db *stockEvidenceTestDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *stockEvidenceTestDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (db *stockEvidenceTestDB) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *stockEvidenceTestDB) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "FROM product_snapshots"):
		return stockEvidenceTestRow{scan: func(dest ...any) error {
			if db.snapshot == nil {
				return pgx.ErrNoRows
			}
			row := db.snapshot
			*dest[0].(*pgtype.UUID) = row.ID
			*dest[1].(*pgtype.UUID) = row.ProductID
			*dest[2].(*pgtype.Text) = row.Title
			*dest[3].(*pgtype.Text) = row.Brand
			*dest[4].(*pgtype.Text) = row.Category
			*dest[5].(*pgtype.Int8) = row.Price
			*dest[6].(*pgtype.Float8) = row.Rating
			*dest[7].(*pgtype.Int4) = row.ReviewsCount
			*dest[8].(*pgtype.Int4) = row.StockTotal
			*dest[9].(*pgtype.Text) = row.ImageURL
			*dest[10].(*pgtype.Text) = row.ContentHash
			*dest[11].(*pgtype.Timestamptz) = row.CapturedAt
			return nil
		}}
	case strings.Contains(query, "FROM delivery_data"):
		return stockEvidenceTestRow{scan: func(dest ...any) error {
			if db.delivery == nil {
				return pgx.ErrNoRows
			}
			row := db.delivery
			*dest[0].(*pgtype.UUID) = row.ID
			*dest[1].(*pgtype.UUID) = row.WorkspaceID
			*dest[2].(*pgtype.UUID) = row.ProductID
			*dest[3].(*string) = row.Region
			*dest[4].(*pgtype.Text) = row.Warehouse
			*dest[5].(*int32) = row.DeliveryDays
			*dest[6].(*int64) = row.DeliveryCost
			*dest[7].(*bool) = row.InStock
			*dest[8].(*pgtype.Timestamptz) = row.CapturedAt
			return nil
		}}
	default:
		return stockEvidenceTestRow{scan: func(...any) error { return pgx.ErrNoRows }}
	}
}

type stockEvidenceTestRow struct {
	scan func(...any) error
}

func (row stockEvidenceTestRow) Scan(dest ...any) error {
	return row.scan(dest...)
}

func TestLatestProductStockEvidenceRejectsStaleSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	db := &stockEvidenceTestDB{snapshot: productSnapshotEvidence(12, now.Add(-maxStockEvidenceAge-time.Second))}

	evidence := latestProductStockEvidenceAt(context.Background(), sqlcgen.New(db), pgtype.UUID{}, now)

	assert.False(t, evidence.OK)
	assert.Empty(t, evidence.Source)
}

func TestLatestProductStockEvidenceRejectsStaleDelivery(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	db := &stockEvidenceTestDB{delivery: deliveryStockEvidence(true, now.Add(-maxStockEvidenceAge-time.Second))}

	evidence := latestProductStockEvidenceAt(context.Background(), sqlcgen.New(db), pgtype.UUID{}, now)

	assert.False(t, evidence.OK)
}

func TestLatestProductStockEvidenceUsesFreshDeliveryWhenSnapshotIsStale(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	db := &stockEvidenceTestDB{
		snapshot: productSnapshotEvidence(12, now.Add(-maxStockEvidenceAge-time.Second)),
		delivery: deliveryStockEvidence(true, now.Add(-time.Hour)),
	}

	evidence := latestProductStockEvidenceAt(context.Background(), sqlcgen.New(db), pgtype.UUID{}, now)

	require.True(t, evidence.OK)
	assert.Equal(t, "delivery_data", evidence.Source)
	assert.False(t, evidence.QuantityKnown)
}

func TestLatestProductStockEvidenceUsesFreshestSignal(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	db := &stockEvidenceTestDB{
		snapshot: productSnapshotEvidence(12, now.Add(-time.Hour)),
		delivery: deliveryStockEvidence(false, now.Add(-10*time.Minute)),
	}

	evidence := latestProductStockEvidenceAt(context.Background(), sqlcgen.New(db), pgtype.UUID{}, now)

	require.True(t, evidence.OK)
	assert.Equal(t, "delivery_data", evidence.Source)
	assert.True(t, evidence.QuantityKnown)
	assert.Zero(t, evidence.Stock)
}

func TestStockEvidenceFreshnessBoundaryAndClockSkew(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	assert.True(t, stockEvidenceIsFresh(now.Add(-maxStockEvidenceAge), now))
	assert.False(t, stockEvidenceIsFresh(now.Add(-maxStockEvidenceAge-time.Nanosecond), now))
	assert.True(t, stockEvidenceIsFresh(now.Add(maxStockEvidenceFutureSkew), now))
	assert.False(t, stockEvidenceIsFresh(now.Add(maxStockEvidenceFutureSkew+time.Nanosecond), now))
	assert.False(t, stockEvidenceIsFresh(time.Time{}, now))
}

func productSnapshotEvidence(stock int32, capturedAt time.Time) *sqlcgen.ProductSnapshotRecord {
	return &sqlcgen.ProductSnapshotRecord{
		StockTotal: pgtype.Int4{Int32: stock, Valid: true},
		CapturedAt: pgtype.Timestamptz{Time: capturedAt, Valid: true},
	}
}

func deliveryStockEvidence(inStock bool, capturedAt time.Time) *sqlcgen.DeliveryDataRow {
	return &sqlcgen.DeliveryDataRow{
		InStock:    inStock,
		CapturedAt: pgtype.Timestamptz{Time: capturedAt, Valid: true},
	}
}
