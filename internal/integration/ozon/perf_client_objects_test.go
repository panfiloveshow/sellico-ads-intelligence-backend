package ozon

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"
)

func TestParseCampaignObjectsCSV(t *testing.T) {
	csv := "Отчёт по кампании №12345678;;;;;;\n" +
		"День;sku;Название товара;Показы;Клики;Расход, ₽, с НДС;Заказы;Выручка, ₽\n" +
		"2026-08-20;1275443218;Товар;1 200;34;156,78;3;2 970,00\n" +
		"Всего;;;1 200;34;156,78;3;2 970,00\n"
	rows, err := parseCampaignObjectsCSV([]byte(csv), 12345678, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.CampaignID != 12345678 || r.SKU != 1275443218 || r.Views != 1200 ||
		r.Clicks != 34 || r.SpendRub != 156.78 || r.Orders != 3 || r.RevenueRub != 2970 {
		t.Fatalf("bad row: %+v", r)
	}
	if r.Date.Format("2006-01-02") != "2026-08-20" {
		t.Fatalf("bad date: %s", r.Date)
	}
}

func TestParseCampaignObjectsReportZip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("98765432.csv")
	f.Write([]byte("День;sku;Показы;Клики;Расход;Заказы;Выручка\n02.08.2026;711000001;10;1;5,50;1;990,00\n"))
	w.Close()
	rows, err := parseCampaignObjectsReport(buf.Bytes(), 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CampaignID != 98765432 || rows[0].SKU != 711000001 || rows[0].RevenueRub != 990 {
		t.Fatalf("bad rows: %+v", rows)
	}
}
