package ozon

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"
)

func TestParseCampaignObjectsCSV(t *testing.T) {
	csv := "\ufeff;Кампания по продвижению товаров № 12345678, период 13.08.2026-27.08.2026\n" +
		"День;sku;Название товара;Цена товара, ₽;Показы;Клики;CTR, %;Добавления в корзину;Средняя стоимость клика, ₽;Расход, ₽, с НДС;Продано товаров;Продажи в продвижении, ₽;Продано товаров модели;Продажи в продвижении с заказов модели, ₽;ДРР в продвижении, %;Заказано на сумму, ₽;ДРР (общий), %;Дата добавления\n" +
		"2026-08-20;1275443218;Товар;212,00;1 200;34;2,83;5;1,10;156,78;3;2 970,00;1;500,00;5,3;4 885,00;0,8;30.07.2026\n" +
		"Всего;;;;1 200;34;;;;156,78;3;2 970,00;;;;;;\n"
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
