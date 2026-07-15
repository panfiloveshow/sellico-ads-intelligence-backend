package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Stock collection is scheduled hourly. Three hours allows two missed sync
// cycles without letting an old snapshot authorize an advertising scale-up.
const maxStockEvidenceAge = 3 * time.Hour

// Database and worker clocks can differ slightly. Larger future timestamps are
// treated as invalid evidence rather than remaining "fresh" indefinitely.
const maxStockEvidenceFutureSkew = 5 * time.Minute

// stockEvidence describes the freshest real stock signal we have for a product.
//
// Quantity is only meaningful when QuantityKnown is true. The delivery_data
// fallback carries an in_stock boolean but no count, so:
//   - in stock  -> Stock=0, QuantityKnown=false (presence confirmed, count unknown)
//   - out of stock -> Stock=0, QuantityKnown=true (confirmed zero)
//
// Callers that gate on a numeric threshold (e.g. MinStockForIncrease,
// stockAlertThreshold) MUST check QuantityKnown before comparing Stock, otherwise
// an "in stock, count unknown" product would be wrongly treated as zero stock.
type stockEvidence struct {
	Stock         int32
	Source        string
	CapturedAt    time.Time
	QuantityKnown bool
	OK            bool
}

func latestProductStockEvidence(ctx context.Context, queries *sqlcgen.Queries, productID pgtype.UUID) stockEvidence {
	return latestProductStockEvidenceAt(ctx, queries, productID, time.Now().UTC())
}

func latestProductStockEvidenceAt(ctx context.Context, queries *sqlcgen.Queries, productID pgtype.UUID, now time.Time) stockEvidence {
	var latest stockEvidence

	snapshot, err := queries.GetLatestProductSnapshot(ctx, productID)
	if err == nil && snapshot.StockTotal.Valid && snapshot.CapturedAt.Valid {
		latest = stockEvidence{
			Stock:         snapshot.StockTotal.Int32,
			Source:        "product_snapshot",
			CapturedAt:    snapshot.CapturedAt.Time,
			QuantityKnown: true,
		}
	}

	delivery, err := queries.GetLatestDeliveryData(ctx, productID)
	if err == nil && delivery.CapturedAt.Valid && (latest.CapturedAt.IsZero() || delivery.CapturedAt.Time.After(latest.CapturedAt)) {
		if delivery.InStock {
			// Presence confirmed but delivery_data has no quantity.
			latest = stockEvidence{
				Stock:         0,
				Source:        "delivery_data",
				CapturedAt:    delivery.CapturedAt.Time,
				QuantityKnown: false,
			}
		} else {
			// Confirmed out of stock.
			latest = stockEvidence{
				Stock:         0,
				Source:        "delivery_data",
				CapturedAt:    delivery.CapturedAt.Time,
				QuantityKnown: true,
			}
		}
	}

	if !stockEvidenceIsFresh(latest.CapturedAt, now) {
		return stockEvidence{}
	}
	latest.OK = true
	return latest
}

func stockEvidenceIsFresh(capturedAt, now time.Time) bool {
	if capturedAt.IsZero() || now.IsZero() {
		return false
	}
	age := now.Sub(capturedAt)
	return age >= -maxStockEvidenceFutureSkew && age <= maxStockEvidenceAge
}
