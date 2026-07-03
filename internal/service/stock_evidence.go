package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

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
	snapshot, err := queries.GetLatestProductSnapshot(ctx, productID)
	if err == nil && snapshot.StockTotal.Valid {
		return stockEvidence{
			Stock:         snapshot.StockTotal.Int32,
			Source:        "product_snapshot",
			CapturedAt:    snapshot.CapturedAt.Time,
			QuantityKnown: true,
			OK:            true,
		}
	}

	delivery, err := queries.GetLatestDeliveryData(ctx, productID)
	if err == nil {
		if delivery.InStock {
			// Presence confirmed but delivery_data has no quantity.
			return stockEvidence{
				Stock:         0,
				Source:        "delivery_data",
				CapturedAt:    delivery.CapturedAt.Time,
				QuantityKnown: false,
				OK:            true,
			}
		}
		// Confirmed out of stock.
		return stockEvidence{
			Stock:         0,
			Source:        "delivery_data",
			CapturedAt:    delivery.CapturedAt.Time,
			QuantityKnown: true,
			OK:            true,
		}
	}

	return stockEvidence{}
}
