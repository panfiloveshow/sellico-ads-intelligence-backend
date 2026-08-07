package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// OrdersHeatmap builds the 7×24 (ISO day-of-week × MSK hour) order matrix for
// an Ozon cabinet (optionally one SKU) from ozon_orders_hourly. The response
// mirrors the WB /prices/heatmap DTO (domain.OrdersHeatmap) so the frontend
// reuses the same component; revenue is always 0 — postings carry no money.
// The stored window is the last ozonPostingsWindowDays days (rewritten by the
// postings sync), which is what date_from/date_to report.
//
// The sku filter accepts either the SALES sku (the native ozon_orders_hourly
// key) or the Seller API product_id (the /ozon/prices key space), bridged via
// the ozon_products mapping — a sales-SKU match wins.
func (s *OzonRepricerService) OrdersHeatmap(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku int64, metric string) (*domain.OrdersHeatmap, error) {
	if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	if sku != 0 {
		sku = s.resolveHeatmapSKU(ctx, cabinetID, sku)
	}
	cells, err := s.queries.OzonOrdersHeatmap(ctx, uuidToPgtype(cabinetID), sku)
	if err != nil {
		return nil, err
	}
	// Postings have no revenue — never present a heatmap of silent zeroes.
	if metric == domain.HeatmapMetricRevenue {
		metric = domain.HeatmapMetricUnits
	}
	to := time.Now()
	from := to.AddDate(0, 0, -ozonPostingsWindowDays)
	return buildOrdersHeatmap(ozonHeatmapCells(cells), from, to, metric), nil
}

// resolveHeatmapSKU maps the sku query param onto the ozon_orders_hourly key
// space: a known sales SKU passes through, a known product_id is bridged to
// its sales SKU, anything else passes through unchanged (empty matrix).
func (s *OzonRepricerService) resolveHeatmapSKU(ctx context.Context, cabinetID uuid.UUID, sku int64) int64 {
	rows, err := s.queries.ListOzonProductsByCabinet(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		s.logger.Warn().Err(err).Msg("heatmap sku bridge load failed")
		return sku
	}
	bridged := int64(0)
	for _, row := range rows {
		if row.Sku.Valid && row.Sku.Int64 == sku {
			return sku // already a sales SKU
		}
		if row.ProductID == sku && row.Sku.Valid && row.Sku.Int64 != 0 {
			bridged = row.Sku.Int64
		}
	}
	if bridged != 0 {
		return bridged
	}
	return sku
}

// ozonHeatmapCells adapts Ozon buckets (dow 0=Пн..6=Вс, quantity) to the WB
// heatmap cell shape (ISO day-of-week 1..7, units) so the pure
// buildOrdersHeatmap matrix math is shared. Pure (unit-tested).
func ozonHeatmapCells(cells []sqlcgen.OzonOrdersHeatmapCell) []sqlcgen.OrdersHeatmapCell {
	out := make([]sqlcgen.OrdersHeatmapCell, 0, len(cells))
	for _, c := range cells {
		out = append(out, sqlcgen.OrdersHeatmapCell{
			DayOfWeek: c.Dow + 1,
			Hour:      c.Hour,
			Orders:    c.Orders,
			Units:     c.Quantity,
		})
	}
	return out
}
