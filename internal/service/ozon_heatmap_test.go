package service

import (
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// --- postings → dow/hour aggregation (timezone-sensitive) ---

func TestOzonMSKSlot_ConvertsUTCToMoscow(t *testing.T) {
	// 2026-08-03 is a Monday. 21:30 UTC = 00:30 MSK Tuesday → dow 1, hour 0.
	dow, hour := ozonMSKSlot(time.Date(2026, 8, 3, 21, 30, 0, 0, time.UTC))
	if dow != 1 || hour != 0 {
		t.Fatalf("slot = (%d, %d), want (1, 0)", dow, hour)
	}
}

func TestOzonMSKSlot_OffsetTimestampSameInstant(t *testing.T) {
	// The same instant sent with an explicit +03:00 offset must land in the
	// same slot as its UTC form.
	msk := time.FixedZone("MSK", 3*60*60)
	dow, hour := ozonMSKSlot(time.Date(2026, 8, 4, 0, 30, 0, 0, msk))
	if dow != 1 || hour != 0 {
		t.Fatalf("slot = (%d, %d), want (1, 0)", dow, hour)
	}
}

func TestOzonMSKSlot_SundayIsDow6(t *testing.T) {
	// 2026-08-02 is a Sunday; 10:00 UTC = 13:00 MSK Sunday → dow 6 (ISO 7 − 1).
	dow, hour := ozonMSKSlot(time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC))
	if dow != 6 || hour != 13 {
		t.Fatalf("slot = (%d, %d), want (6, 13)", dow, hour)
	}
}

func TestAggregateOzonPostings_BucketsByMSKSlot(t *testing.T) {
	// Two orders of the same SKU in the same MSK slot merge; a UTC timestamp
	// crossing midnight in MSK lands on the next day.
	postings := []ozon.PostingSale{
		{SKU: 111, CreatedAt: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC), Quantity: 2},   // Mon 12:00 MSK
		{SKU: 111, CreatedAt: time.Date(2026, 8, 3, 9, 45, 0, 0, time.UTC), Quantity: 1},  // Mon 12:45 MSK
		{SKU: 111, CreatedAt: time.Date(2026, 8, 3, 21, 30, 0, 0, time.UTC), Quantity: 1}, // Tue 00:30 MSK!
		{SKU: 222, CreatedAt: time.Date(2026, 8, 3, 9, 15, 0, 0, time.UTC), Quantity: 5},  // Mon 12:15 MSK
	}
	rows := aggregateOzonPostings(postings)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (%+v)", len(rows), rows)
	}
	find := func(sku int64, dow, hour int16) *sqlcgen.OzonOrdersHourlyRow {
		for i := range rows {
			if rows[i].Sku == sku && rows[i].Dow == dow && rows[i].Hour == hour {
				return &rows[i]
			}
		}
		return nil
	}
	monNoon := find(111, 0, 12)
	if monNoon == nil || monNoon.Orders != 2 || monNoon.Quantity != 3 {
		t.Fatalf("sku 111 Mon 12h = %+v, want orders=2 quantity=3", monNoon)
	}
	tueMidnight := find(111, 1, 0)
	if tueMidnight == nil || tueMidnight.Orders != 1 || tueMidnight.Quantity != 1 {
		t.Fatalf("sku 111 Tue 0h = %+v, want orders=1 quantity=1", tueMidnight)
	}
	other := find(222, 0, 12)
	if other == nil || other.Orders != 1 || other.Quantity != 5 {
		t.Fatalf("sku 222 Mon 12h = %+v, want orders=1 quantity=5", other)
	}
}

func TestAggregateOzonPostings_DropsInvalidRecords(t *testing.T) {
	postings := []ozon.PostingSale{
		{SKU: 0, CreatedAt: time.Now(), Quantity: 1},
		{SKU: 111, Quantity: 1}, // zero timestamp
	}
	if rows := aggregateOzonPostings(postings); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}

// --- peak-hours decision through the Ozon wrapper ---

func TestDecideOzonPeakHours_PeakStepsUp(t *testing.T) {
	// Intensity 1 → target 1000×1.08=1080, capped by the 3% default step to
	// 1030. dry_run default allows raises without max_price_rub.
	d := decideOzonPeakHours(1000, 500, 1, true, domain.StrategyParams{})
	if !d.ShouldChange || d.Direction != "up" {
		t.Fatalf("decision = %+v, want an up change", d)
	}
	if d.NewPriceRub != 1030 {
		t.Fatalf("new price = %v, want 1030", d.NewPriceRub)
	}
}

func TestDecideOzonPeakHours_DeadHourStepsDown(t *testing.T) {
	// Intensity 0 → target 1000×0.88=880, capped by the 3% step to 970.
	d := decideOzonPeakHours(1000, 500, 0, true, domain.StrategyParams{})
	if !d.ShouldChange || d.Direction != "down" {
		t.Fatalf("decision = %+v, want a down change", d)
	}
	if d.NewPriceRub != 970 {
		t.Fatalf("new price = %v, want 970", d.NewPriceRub)
	}
}

func TestDecideOzonPeakHours_DownClampedToFloor(t *testing.T) {
	// The dead-hour target undershoots the 985₽ floor → clamp to the floor.
	d := decideOzonPeakHours(1000, 985, 0, true, domain.StrategyParams{})
	if !d.ShouldChange || d.Direction != "down" {
		t.Fatalf("decision = %+v, want a down change", d)
	}
	if d.NewPriceRub != 985 {
		t.Fatalf("new price = %v, want floor 985", d.NewPriceRub)
	}
}

func TestDecideOzonPeakHours_AutoRaiseRequiresMaxPrice(t *testing.T) {
	params := domain.StrategyParams{PriceApplyMode: domain.PriceApplyModeAuto}
	d := decideOzonPeakHours(1000, 500, 1, true, params)
	if d.ShouldChange || d.SkipReason != "max_price_required_for_increase" {
		t.Fatalf("decision = %+v, want skip max_price_required_for_increase", d)
	}
}

func TestDecideOzonPeakHours_NoDemandDataSkips(t *testing.T) {
	d := decideOzonPeakHours(1000, 500, 0, false, domain.StrategyParams{})
	if d.ShouldChange || d.SkipReason != "no_demand_data" {
		t.Fatalf("decision = %+v, want skip no_demand_data", d)
	}
}

func TestDecideOzonPeakHours_MidIntensityDeadBand(t *testing.T) {
	// intensity 0.6 → factor 1 + 0.08×0.6 − 0.12×0.4 = 1.0 → no move.
	d := decideOzonPeakHours(1000, 500, 0.6, true, domain.StrategyParams{})
	if d.ShouldChange {
		t.Fatalf("decision = %+v, want no change", d)
	}
}

// --- per-SKU intensity with cabinet fallback ---

func TestPeakIntensityForProduct_SKUWithEnoughOrders(t *testing.T) {
	aux := ozonStrategyAux{
		salesSKUByProductID:     map[int64]int64{10: 111},
		peakIntensityBySalesSKU: map[int64]sqlcgen.OzonSlotIntensity{111: {Intensity: 0.75, TotalOrders: 25}},
		peakCabinetIntensity:    0.2,
		peakCabinetHasData:      true,
	}
	intensity, ok := aux.peakIntensityForProduct(10)
	if !ok || intensity != 0.75 {
		t.Fatalf("intensity = (%v, %v), want (0.75, true)", intensity, ok)
	}
}

func TestPeakIntensityForProduct_ThinSKUFallsBackToCabinet(t *testing.T) {
	aux := ozonStrategyAux{
		salesSKUByProductID:     map[int64]int64{10: 111},
		peakIntensityBySalesSKU: map[int64]sqlcgen.OzonSlotIntensity{111: {Intensity: 1, TotalOrders: ozonPeakHoursMinSKUOrders - 1}},
		peakCabinetIntensity:    0.4,
		peakCabinetHasData:      true,
	}
	intensity, ok := aux.peakIntensityForProduct(10)
	if !ok || intensity != 0.4 {
		t.Fatalf("intensity = (%v, %v), want cabinet fallback (0.4, true)", intensity, ok)
	}
}

func TestPeakIntensityForProduct_NoDataSkips(t *testing.T) {
	aux := ozonStrategyAux{
		salesSKUByProductID:     map[int64]int64{},
		peakIntensityBySalesSKU: map[int64]sqlcgen.OzonSlotIntensity{},
	}
	if _, ok := aux.peakIntensityForProduct(10); ok {
		t.Fatal("want ok=false when the cabinet has no heatmap data")
	}
}

// --- heatmap DTO mapping (mirrors the WB /prices/heatmap contract) ---

func TestOzonHeatmapCells_MapsDowAndQuantity(t *testing.T) {
	cells := ozonHeatmapCells([]sqlcgen.OzonOrdersHeatmapCell{
		{Dow: 0, Hour: 12, Orders: 3, Quantity: 5},
		{Dow: 6, Hour: 23, Orders: 1, Quantity: 1},
	})
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	hm := buildOrdersHeatmap(cells, from, to, domain.HeatmapMetricUnits)
	if len(hm.Days) != 7 || len(hm.Days[0].Hours) != 24 {
		t.Fatalf("matrix shape = %dx%d, want 7x24", len(hm.Days), len(hm.Days[0].Hours))
	}
	monday := hm.Days[0]
	if monday.DayOfWeek != 1 || monday.DayLabel != "Пн" {
		t.Fatalf("day[0] = %+v, want ISO Monday (Пн)", monday)
	}
	cell := monday.Hours[12]
	if cell.Orders != 3 || cell.Units != 5 || cell.Value != 5 || cell.Intensity != 1 {
		t.Fatalf("Mon 12h = %+v, want orders=3 units=5 value=5 intensity=1", cell)
	}
	sunday := hm.Days[6].Hours[23]
	if sunday.Units != 1 || sunday.Intensity != 0.2 {
		t.Fatalf("Sun 23h = %+v, want units=1 intensity=0.2", sunday)
	}
	if hm.Peak == nil || hm.Peak.DayOfWeek != 1 || hm.Peak.Hour != 12 {
		t.Fatalf("peak = %+v, want Mon 12h", hm.Peak)
	}
	if hm.Totals.Orders != 4 || hm.Totals.Units != 6 || hm.Totals.RevenueRub != 0 {
		t.Fatalf("totals = %+v, want orders=4 units=6 revenue=0", hm.Totals)
	}
}
