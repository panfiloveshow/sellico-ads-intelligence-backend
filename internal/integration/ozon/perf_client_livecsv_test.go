package ozon

import "testing"

// TestParseDailyStatsCSVLiveHeader pins the exact header the live export
// sends (captured from production 2026-08-07): unit suffixes in column
// names previously failed to match, silently zeroing spend/orders/revenue
// for every cabinet while views/clicks looked fine.
func TestParseDailyStatsCSVLiveHeader(t *testing.T) {
	body := []byte("ID;Название;Дата;Показы;Клики;Расход, ₽;Заказы, шт.;Заказы, ₽\n" +
		"33747459;Общ;2026-08-01;16018;304;1541,76;7;3930,00\n" +
		"25952364;Четверки+;2026-08-01;0;0;0,00;4;2215,00\n")
	rows, err := parseDailyStatsCSV(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	first := rows[0]
	if first.CampaignID != 33747459 || first.Views != 16018 || first.Clicks != 304 {
		t.Fatalf("unexpected id/views/clicks: %+v", first)
	}
	if first.SpendRub != 1541.76 {
		t.Fatalf("spend = %v, want 1541.76", first.SpendRub)
	}
	if first.Orders != 7 {
		t.Fatalf("orders = %v, want 7", first.Orders)
	}
	if first.RevenueRub != 3930 {
		t.Fatalf("revenue = %v, want 3930", first.RevenueRub)
	}
	if rows[1].Orders != 4 || rows[1].RevenueRub != 2215 {
		t.Fatalf("cpo row lost orders/revenue: %+v", rows[1])
	}
}
