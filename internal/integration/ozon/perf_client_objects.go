package ozon

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Campaign objects (per-SKU) statistics via the same async report flow as
// phrases (perf_client_phrases.go):
//
//	POST /api/client/statistics             → {"UUID": "..."}
//	GET  /api/client/statistics/{UUID}      → {"state": "OK" | ...}
//	GET  /api/client/statistics/report?UUID → CSV / ZIP of CSVs
//
// For SKU campaigns the report rows carry per-SKU (and per-day with
// groupBy DATE) counters — the only surface where «сколько заказов принесла
// именно эта кампания этому товару» exists.

// objectsPollTimeout: reports queue account-wide behind phrases/CPO reports
// kicked at the same worker boot — 5 minutes in NOT_STARTED is normal.
const objectsPollTimeout = 15 * time.Minute

// CampaignObjectStatRow is one parsed row: a campaign's SKU on one day.
type CampaignObjectStatRow struct {
	CampaignID int64 // filled by the caller per chunk when absent in the CSV
	SKU        int64
	Date       time.Time
	Views      int64
	Clicks     int64
	SpendRub   float64
	Orders     int64
	RevenueRub float64
}

// GetCampaignObjectsReport fetches per-SKU campaign statistics over [from, to]
// ONE campaign per report, sequentially. Multi-campaign requests return a ZIP
// whose entry names don't reliably carry the campaign id (rows parsed to
// campaign 0 on prod) — a single-campaign report is a plain CSV and the id is
// known from the request itself.
func (c *PerfClient) GetCampaignObjectsReport(ctx context.Context, creds Credentials, campaignIDs []int64, from, to time.Time) ([]CampaignObjectStatRow, error) {
	var out []CampaignObjectStatRow
	var errs []error
	for _, id := range campaignIDs {
		rows, err := c.campaignObjectsChunk(ctx, creds, []int64{id}, from, to)
		if err != nil {
			// Одна кампания не должна рушить остальные (таймаут очереди
			// отчётов, 400 на конкретный id) — best-effort с общей ошибкой.
			errs = append(errs, fmt.Errorf("campaign %d: %w", id, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		out = append(out, rows...)
	}
	return out, errors.Join(errs...)
}

func (c *PerfClient) campaignObjectsChunk(ctx context.Context, creds Credentials, campaignIDs []int64, from, to time.Time) ([]CampaignObjectStatRow, error) {
	campaigns := make([]string, 0, len(campaignIDs))
	for _, id := range campaignIDs {
		campaigns = append(campaigns, strconv.FormatInt(id, 10))
	}
	payload := map[string]any{
		"campaigns": campaigns,
		"from":      from.UTC().Format(time.RFC3339),
		"to":        to.UTC().Format(time.RFC3339),
		"groupBy":   "DATE",
	}
	body, err := c.doJSON(ctx, creds, "POST", "/api/client/statistics", nil, payload)
	if err != nil {
		return nil, fmt.Errorf("submit campaign objects report: %w", err)
	}
	var parsed struct {
		UUIDUpper string `json:"UUID"`
		UUIDLower string `json:"uuid"`
	}
	if err := decodeJSON(body, &parsed, "statistics submit"); err != nil {
		c.logger.Warn().Str("raw_prefix", rawSnippet(body)).Msg("ozon statistics submit: undecodable response")
		return nil, err
	}
	reportUUID := parsed.UUIDUpper
	if reportUUID == "" {
		reportUUID = parsed.UUIDLower
	}
	if reportUUID == "" {
		return nil, fmt.Errorf("ozon perf: statistics submit returned no UUID (raw: %s)", rawSnippet(body))
	}
	if err := c.waitStatisticsReportUntil(ctx, creds, reportUUID, objectsPollTimeout); err != nil {
		return nil, err
	}
	report, err := c.downloadStatisticsReport(ctx, creds, reportUUID)
	if err != nil {
		return nil, err
	}
	// Один UUID на чанк: CSV для одной кампании, ZIP для нескольких. Номер
	// кампании в CSV-строках отсутствует — но в ZIP он в имени файла, а для
	// одиночного отчёта чанк и есть кампания.
	fallbackCampaign := int64(0)
	if len(campaignIDs) == 1 {
		fallbackCampaign = campaignIDs[0]
	}
	rows, err := parseCampaignObjectsReport(report, fallbackCampaign, to)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		c.logger.Warn().Int64("campaign_id", fallbackCampaign).
			Str("raw_prefix", rawSnippet(report)).
			Msg("ozon campaign objects report parsed to 0 rows")
	}
	// Диагностика колонок: клики есть, заказы по всем строкам нулевые —
	// либо факт, либо колонка называется иначе; заголовок решает спор.
	var clicks, orders int64
	for _, row := range rows {
		clicks += row.Clicks
		orders += row.Orders
	}
	if clicks > 100 && orders == 0 {
		head := report
		if len(head) > 1200 {
			head = head[:1200] // кириллический заголовок не влезает в rawSnippet(300)
		}
		c.logger.Warn().Int64("campaign_id", fallbackCampaign).
			Str("report_head", string(head)).
			Msg("ozon campaign objects report: clicks without orders — check column mapping")
	}
	return rows, nil
}

// parseCampaignObjectsReport dispatches on payload shape (ZIP / CSV).
func parseCampaignObjectsReport(body []byte, fallbackCampaign int64, fallbackDate time.Time) ([]CampaignObjectStatRow, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if bytes.HasPrefix(trimmed, []byte("PK\x03\x04")) {
		reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, fmt.Errorf("ozon perf: open objects report zip: %w", err)
		}
		var out []CampaignObjectStatRow
		for _, file := range reader.File {
			fh, openErr := file.Open()
			if openErr != nil {
				return out, fmt.Errorf("ozon perf: open zip entry %s: %w", file.Name, openErr)
			}
			content, readErr := io.ReadAll(fh)
			fh.Close()
			if readErr != nil {
				return out, fmt.Errorf("ozon perf: read zip entry %s: %w", file.Name, readErr)
			}
			entryCampaign := campaignIDFromFilename(file.Name)
			if entryCampaign == 0 && len(reader.File) == 1 {
				entryCampaign = fallbackCampaign
			}
			rows, parseErr := parseCampaignObjectsCSV(bytes.TrimSpace(content), entryCampaign, fallbackDate)
			if parseErr != nil {
				return out, fmt.Errorf("ozon perf: parse zip entry %s: %w", file.Name, parseErr)
			}
			out = append(out, rows...)
		}
		return out, nil
	}
	return parseCampaignObjectsCSV(trimmed, fallbackCampaign, fallbackDate)
}

// campaignIDFromFilename extracts the campaign id from a ZIP entry name —
// the longest digit run in the basename ("12345678.csv", "report_12345678.csv").
func campaignIDFromFilename(name string) int64 {
	base := name
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	best := ""
	current := ""
	for _, r := range base {
		if r >= '0' && r <= '9' {
			current += string(r)
			continue
		}
		if len(current) > len(best) {
			best = current
		}
		current = ""
	}
	if len(current) > len(best) {
		best = current
	}
	id, _ := strconv.ParseInt(best, 10, 64)
	return id
}

// parseCampaignObjectsCSV parses one campaign's CSV by header names — the
// report prepends a title/period preamble, and column sets vary by campaign
// type, so positions are never trusted.
func parseCampaignObjectsCSV(body []byte, campaignID int64, fallbackDate time.Time) ([]CampaignObjectStatRow, error) {
	if len(body) == 0 {
		return nil, nil
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || (len(records) > 0 && len(records[0]) < 2) {
		reader = csv.NewReader(bytes.NewReader(body))
		reader.FieldsPerRecord = -1
		records, err = reader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("ozon perf: parse objects CSV: %w", err)
		}
	}
	if len(records) < 2 {
		return nil, nil
	}

	// Header = первая строка с колонкой sku.
	headerIdx := -1
	col := map[string]int{}
	for i, record := range records {
		candidate := map[string]int{}
		for j, name := range record {
			candidate[strings.ToLower(strings.TrimSpace(name))] = j
		}
		if _, ok := candidate["sku"]; ok {
			headerIdx = i
			col = candidate
			break
		}
	}
	if headerIdx < 0 {
		return nil, fmt.Errorf("ozon perf: objects CSV has no sku column (header: %s)", strings.Join(records[0], ";"))
	}

	pickIdx := func(exact []string, contains []string) int {
		for _, name := range exact {
			if idx, ok := col[name]; ok {
				return idx
			}
		}
		for header, idx := range col {
			for _, fragment := range contains {
				if strings.Contains(header, fragment) {
					return idx
				}
			}
		}
		return -1
	}
	skuIdx := col["sku"]
	dateIdx := pickIdx([]string{"день", "дата", "date"}, nil)
	viewsIdx := pickIdx([]string{"показы", "views"}, []string{"показы"})
	clicksIdx := pickIdx([]string{"клики", "clicks"}, []string{"клики"})
	spendIdx := pickIdx(nil, []string{"расход"})
	// Трафареты называют колонки «Продано товаров» / «Продажи в продвижении, ₽»
	// (не «Заказы»/«Выручка»); exact-ключи первыми, чтобы не зацепить
	// «... модели» через contains.
	ordersIdx := pickIdx([]string{"продано товаров", "заказы", "orders"}, []string{"заказы"})
	revenueIdx := pickIdx([]string{"продажи в продвижении, ₽", "выручка, ₽"}, []string{"выручка"})

	cell := func(row []string, idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}
	stripSpaces := func(raw string) string {
		return strings.ReplaceAll(strings.ReplaceAll(raw, " ", ""), " ", "")
	}
	parseI := func(raw string) int64 {
		v, _ := strconv.ParseInt(stripSpaces(raw), 10, 64)
		return v
	}
	parseF := func(raw string) float64 {
		v, _ := strconv.ParseFloat(strings.ReplaceAll(stripSpaces(raw), ",", "."), 64)
		return v
	}
	parseDate := func(raw string) time.Time {
		for _, layout := range []string{"2006-01-02", "02.01.2006"} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed
			}
		}
		return fallbackDate
	}

	out := make([]CampaignObjectStatRow, 0, len(records)-headerIdx-1)
	for _, record := range records[headerIdx+1:] {
		sku := parseI(cell(record, skuIdx))
		if sku == 0 {
			continue // итоговые строки «Всего»/«Корректировка» — без sku
		}
		out = append(out, CampaignObjectStatRow{
			CampaignID: campaignID,
			SKU:        sku,
			Date:       parseDate(cell(record, dateIdx)),
			Views:      parseI(cell(record, viewsIdx)),
			Clicks:     parseI(cell(record, clicksIdx)),
			SpendRub:   parseF(cell(record, spendIdx)),
			Orders:     parseI(cell(record, ordersIdx)),
			RevenueRub: parseF(cell(record, revenueIdx)),
		})
	}
	return out, nil
}
