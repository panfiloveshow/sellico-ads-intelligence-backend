package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

var heatmapDayLabels = [8]string{"", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"}

// OrdersHeatmap builds the 7×24 (ISO day-of-week × MSK hour) order matrix for a
// cabinet (optionally one product) from product_orders_hourly. Mirrors the
// финсводка salesHeatmap contract: value per metric, intensity = value/max.
func (s *RepricerService) OrdersHeatmap(ctx context.Context, workspaceID uuid.UUID, cabinetID uuid.UUID, wbProductID int64, from, to time.Time, metric string) (*domain.OrdersHeatmap, error) {
	cells, err := s.queries.OrdersHeatmap(ctx,
		uuidToPgtype(workspaceID), uuidToPgtype(cabinetID), wbProductID,
		pgtype.Date{Time: from, Valid: true}, pgtype.Date{Time: to, Valid: true})
	if err != nil {
		return nil, err
	}
	return buildOrdersHeatmap(cells, from, to, metric), nil
}

// buildOrdersHeatmap is pure so the matrix math is unit-testable.
func buildOrdersHeatmap(cells []sqlcgen.OrdersHeatmapCell, from, to time.Time, metric string) *domain.OrdersHeatmap {
	if metric != domain.HeatmapMetricOrders && metric != domain.HeatmapMetricRevenue {
		metric = domain.HeatmapMetricUnits
	}

	out := &domain.OrdersHeatmap{
		DateFrom: from.Format("2006-01-02"),
		DateTo:   to.Format("2006-01-02"),
		Metric:   metric,
		Days:     make([]domain.HeatmapDay, 7),
	}
	for d := 0; d < 7; d++ {
		out.Days[d] = domain.HeatmapDay{
			DayOfWeek: d + 1,
			DayLabel:  heatmapDayLabels[d+1],
			Hours:     make([]domain.HeatmapCell, 24),
		}
		for h := 0; h < 24; h++ {
			out.Days[d].Hours[h] = domain.HeatmapCell{Hour: h}
		}
	}

	var maxValue int64
	for _, c := range cells {
		if c.DayOfWeek < 1 || c.DayOfWeek > 7 || c.Hour < 0 || c.Hour > 23 {
			continue
		}
		cell := &out.Days[c.DayOfWeek-1].Hours[c.Hour]
		cell.Orders += c.Orders
		cell.Units += c.Units
		cell.RevenueRub += (c.RevenueK + 50) / 100

		out.Totals.Orders += c.Orders
		out.Totals.Units += c.Units
		out.Totals.RevenueRub += (c.RevenueK + 50) / 100
	}
	for d := range out.Days {
		for h := range out.Days[d].Hours {
			cell := &out.Days[d].Hours[h]
			cell.Value = heatmapMetricValue(cell, metric)
			if cell.Value > maxValue {
				maxValue = cell.Value
				out.Peak = &domain.HeatmapPeak{DayOfWeek: d + 1, DayLabel: out.Days[d].DayLabel, Hour: h, Value: cell.Value}
			}
		}
	}
	if maxValue > 0 {
		for d := range out.Days {
			for h := range out.Days[d].Hours {
				cell := &out.Days[d].Hours[h]
				cell.Intensity = float64(cell.Value) / float64(maxValue)
			}
		}
	}
	return out
}

func heatmapMetricValue(c *domain.HeatmapCell, metric string) int64 {
	switch metric {
	case domain.HeatmapMetricOrders:
		return c.Orders
	case domain.HeatmapMetricRevenue:
		return c.RevenueRub
	default:
		return c.Units
	}
}
