package ozon

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrPhrasesUnsupported means Ozon refuses to generate a phrases report for the
// given campaigns (the report is forbidden for whole campaign types). It is a
// permanent, expected condition — callers skip the cabinet, they do not retry.
var ErrPhrasesUnsupported = errors.New("ozon phrases report unsupported for these campaigns")

// Phrases (search query) statistics via the async report flow of the
// Performance API:
//
//	POST /api/client/statistics/phrases            → {"UUID": "..."}
//	GET  /api/client/statistics/{UUID}             → {"state": "OK" | ...}
//	GET  /api/client/statistics/report?UUID=...    → CSV / JSON / ZIP of CSVs
//
// Constraints from the API docs: at most 10 campaigns per report and one
// report generation per account at a time — chunks run strictly sequentially.

const (
	// phrasesCampaignChunk is the documented campaign cap per statistics report.
	phrasesCampaignChunk = 10
	// phrasesPollTimeout bounds one report's generation wait.
	phrasesPollTimeout = 5 * time.Minute
	// rawSnippetLimit is how many raw bytes are logged on a decode failure.
	rawSnippetLimit = 300
)

// phrasesPollInterval is how often the report state is polled. It is a var
// (not a const) only so tests can shorten it; production never mutates it.
var phrasesPollInterval = 5 * time.Second

// PhraseStatRow is one parsed row of the phrases report: a search query with
// its performance counters, optionally attributed to a SKU (0 = unknown).
type PhraseStatRow struct {
	SKU         int64
	Query       string
	Date        time.Time
	Views       int64
	Clicks      int64
	Orders      int64
	SpendRub    float64
	AvgPosition *float64
}

// GetPhrasesReport fetches phrase (search query) statistics for the campaigns
// over [from, to] via the async statistics flow, sequentially in chunks of at
// most 10 campaigns. fallbackDate (the report's dateTo) is used for rows that
// carry no parseable date of their own.
func (c *PerfClient) GetPhrasesReport(ctx context.Context, creds Credentials, campaignIDs []int64, from, to time.Time) ([]PhraseStatRow, error) {
	var out []PhraseStatRow
	for start := 0; start < len(campaignIDs); start += phrasesCampaignChunk {
		end := start + phrasesCampaignChunk
		if end > len(campaignIDs) {
			end = len(campaignIDs)
		}
		rows, err := c.phrasesReportChunk(ctx, creds, campaignIDs[start:end], from, to)
		if err != nil {
			return out, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (c *PerfClient) phrasesReportChunk(ctx context.Context, creds Credentials, campaignIDs []int64, from, to time.Time) ([]PhraseStatRow, error) {
	reportUUID, err := c.submitPhrasesReport(ctx, creds, campaignIDs, from, to)
	if err != nil {
		return nil, err
	}
	if err := c.waitStatisticsReport(ctx, creds, reportUUID); err != nil {
		return nil, err
	}
	body, err := c.downloadStatisticsReport(ctx, creds, reportUUID)
	if err != nil {
		return nil, err
	}
	rows, err := c.parsePhrasesReport(body, to)
	if err != nil {
		c.logger.Warn().Err(err).Str("uuid", reportUUID).
			Str("raw_prefix", rawSnippet(body)).
			Msg("ozon phrases report parse failed")
		return nil, err
	}
	return rows, nil
}

// submitPhrasesReport starts the async report and returns its UUID.
func (c *PerfClient) submitPhrasesReport(ctx context.Context, creds Credentials, campaignIDs []int64, from, to time.Time) (string, error) {
	campaigns := make([]string, 0, len(campaignIDs))
	for _, id := range campaignIDs {
		campaigns = append(campaigns, strconv.FormatInt(id, 10))
	}
	// Live API (verified 2026-08-10): the phrases report uses from/to as
	// RFC3339 timestamps (protobuf Timestamp), NOT dateFrom/dateTo dates —
	// dateFrom is rejected as an unknown parameter.
	payload := map[string]any{
		"campaigns": campaigns,
		"from":      from.UTC().Format(time.RFC3339),
		"to":        to.UTC().Format(time.RFC3339),
		"groupBy":   "DATE",
	}
	body, err := c.doJSON(ctx, creds, "POST", "/api/client/statistics/phrases", nil, payload)
	if err != nil {
		// Ozon forbids the phrases report for whole campaign types (verified
		// 2026-08-10: SKU/SEARCH_PROMO/ALL_SKU_PROMO all rejected). Surface
		// that as a typed "unsupported" so the sync skips quietly instead of
		// flooding logs and alarming operators with a hard error.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 400 {
			msg := strings.ToLower(apiErr.Message)
			if strings.Contains(msg, "forbidden for the transferred") || strings.Contains(msg, "bad request") {
				return "", fmt.Errorf("%w: %s", ErrPhrasesUnsupported, apiErr.Message)
			}
		}
		return "", fmt.Errorf("submit phrases report: %w", err)
	}
	var parsed struct {
		UUIDUpper string `json:"UUID"`
		UUIDLower string `json:"uuid"`
	}
	if err := decodeJSON(body, &parsed, "statistics/phrases submit"); err != nil {
		c.logger.Warn().Str("raw_prefix", rawSnippet(body)).Msg("ozon phrases submit: undecodable response")
		return "", err
	}
	reportUUID := parsed.UUIDUpper
	if reportUUID == "" {
		reportUUID = parsed.UUIDLower
	}
	if reportUUID == "" {
		return "", fmt.Errorf("ozon perf: statistics/phrases submit returned no UUID (raw: %s)", rawSnippet(body))
	}
	return reportUUID, nil
}

// waitStatisticsReport polls GET /api/client/statistics/{uuid} until the
// report state is OK, an error state arrives, or the timeout elapses.
func (c *PerfClient) waitStatisticsReport(ctx context.Context, creds Credentials, reportUUID string) error {
	return c.waitStatisticsReportUntil(ctx, creds, reportUUID, phrasesPollTimeout)
}

// waitStatisticsReportUntil polls a report's state with an explicit timeout —
// reports queue behind each other account-wide, so callers that submit many
// reports in a row need a longer wait than the phrases default.
func (c *PerfClient) waitStatisticsReportUntil(ctx context.Context, creds Credentials, reportUUID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		body, err := c.doGet(ctx, creds, "/api/client/statistics/"+reportUUID, nil)
		if err != nil {
			return fmt.Errorf("poll statistics report %s: %w", reportUUID, err)
		}
		var parsed struct {
			State string `json:"state"`
			Error string `json:"error"`
		}
		if err := decodeJSON(body, &parsed, "statistics report state"); err != nil {
			c.logger.Warn().Str("raw_prefix", rawSnippet(body)).Msg("ozon statistics poll: undecodable response")
			return err
		}
		switch strings.ToUpper(strings.TrimSpace(parsed.State)) {
		case "OK":
			return nil
		case "ERROR", "FAILED":
			return fmt.Errorf("ozon perf: statistics report %s failed: %s", reportUUID, parsed.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ozon perf: statistics report %s not ready after %s (state %q)", reportUUID, timeout, parsed.State)
		}
		if err := sleepWithContext(ctx, phrasesPollInterval); err != nil {
			return err
		}
	}
}

// downloadStatisticsReport fetches the finished report body as-is.
func (c *PerfClient) downloadStatisticsReport(ctx context.Context, creds Credentials, reportUUID string) ([]byte, error) {
	query := url.Values{}
	query.Set("UUID", reportUUID)
	body, err := c.doGet(ctx, creds, "/api/client/statistics/report", query)
	if err != nil {
		return nil, fmt.Errorf("download statistics report %s: %w", reportUUID, err)
	}
	return body, nil
}

// parsePhrasesReport dispatches on the payload shape: ZIP (multi-campaign
// reports arrive as an archive of CSVs), JSON, or bare CSV.
func (c *PerfClient) parsePhrasesReport(body []byte, fallbackDate time.Time) ([]PhraseStatRow, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch {
	case bytes.HasPrefix(trimmed, []byte("PK\x03\x04")):
		return parsePhrasesZip(body, fallbackDate)
	case trimmed[0] == '{' || trimmed[0] == '[':
		return parsePhrasesJSON(trimmed, fallbackDate)
	default:
		return parsePhrasesCSV(trimmed, fallbackDate)
	}
}

// parsePhrasesZip unpacks a multi-campaign report archive and parses each
// entry (CSV or JSON) independently.
func parsePhrasesZip(body []byte, fallbackDate time.Time) ([]PhraseStatRow, error) {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("ozon perf: open phrases report zip: %w", err)
	}
	var out []PhraseStatRow
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
		trimmed := bytes.TrimSpace(content)
		if len(trimmed) == 0 {
			continue
		}
		var rows []PhraseStatRow
		var parseErr error
		if trimmed[0] == '{' || trimmed[0] == '[' {
			rows, parseErr = parsePhrasesJSON(trimmed, fallbackDate)
		} else {
			rows, parseErr = parsePhrasesCSV(trimmed, fallbackDate)
		}
		if parseErr != nil {
			return out, fmt.Errorf("ozon perf: parse zip entry %s: %w", file.Name, parseErr)
		}
		out = append(out, rows...)
	}
	return out, nil
}

// wirePhraseRow tolerates every field spelling the statistics reports have
// used across revisions (flex types absorb numbers-as-strings — prod lesson).
type wirePhraseRow struct {
	Date        string    `json:"date"`
	SKU         flexInt64 `json:"sku"`
	Phrase      string    `json:"phrase"`
	SearchQuery string    `json:"searchQuery"`
	Query       string    `json:"query"`
	Views       flexInt64 `json:"views"`
	Clicks      flexInt64 `json:"clicks"`
	MoneySpent  flexFloat `json:"moneySpent"`
	Spend       flexFloat `json:"spend"`
	Orders      flexInt64 `json:"orders"`
	AvgPosition flexFloat `json:"avgPosition"`
	Position    flexFloat `json:"position"`
}

func (w wirePhraseRow) toRow(fallbackDate time.Time) (PhraseStatRow, bool) {
	query := strings.TrimSpace(w.Phrase)
	if query == "" {
		query = strings.TrimSpace(w.SearchQuery)
	}
	if query == "" {
		query = strings.TrimSpace(w.Query)
	}
	if query == "" {
		return PhraseStatRow{}, false
	}
	date := fallbackDate
	if parsed, err := time.Parse("2006-01-02", strings.TrimSpace(w.Date)); err == nil {
		date = parsed
	}
	row := PhraseStatRow{
		SKU:      int64(w.SKU),
		Query:    strings.ToLower(query),
		Date:     date,
		Views:    int64(w.Views),
		Clicks:   int64(w.Clicks),
		Orders:   int64(w.Orders),
		SpendRub: float64(w.MoneySpent),
	}
	if row.SpendRub == 0 {
		row.SpendRub = float64(w.Spend)
	}
	position := float64(w.AvgPosition)
	if position == 0 {
		position = float64(w.Position)
	}
	if position > 0 {
		row.AvgPosition = &position
	}
	return row, true
}

// parsePhrasesJSON accepts the shapes the statistics endpoints are known to
// produce: {"rows":[...]}, {"report":{"rows":[...]}}, a bare array, or a
// per-campaign map {"<id>":{"report":{"rows":[...]}}}.
func parsePhrasesJSON(body []byte, fallbackDate time.Time) ([]PhraseStatRow, error) {
	convert := func(wires []wirePhraseRow) []PhraseStatRow {
		out := make([]PhraseStatRow, 0, len(wires))
		for _, wire := range wires {
			if row, ok := wire.toRow(fallbackDate); ok {
				out = append(out, row)
			}
		}
		return out
	}

	// Bare array.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		var wires []wirePhraseRow
		if err := decodeJSON(body, &wires, "phrases report array"); err != nil {
			return nil, err
		}
		return convert(wires), nil
	}

	// Object shapes.
	var envelope struct {
		Rows   []wirePhraseRow `json:"rows"`
		Report *struct {
			Rows []wirePhraseRow `json:"rows"`
		} `json:"report"`
	}
	if err := decodeJSON(body, &envelope, "phrases report"); err == nil {
		if len(envelope.Rows) > 0 {
			return convert(envelope.Rows), nil
		}
		if envelope.Report != nil && len(envelope.Report.Rows) > 0 {
			return convert(envelope.Report.Rows), nil
		}
	}

	// Per-campaign map: {"12345": {"report": {"rows": [...]}}, ...}.
	var perCampaign map[string]struct {
		Rows   []wirePhraseRow `json:"rows"`
		Report *struct {
			Rows []wirePhraseRow `json:"rows"`
		} `json:"report"`
	}
	if err := json.Unmarshal(body, &perCampaign); err != nil {
		return nil, fmt.Errorf("ozon perf: phrases report JSON has an unknown shape: %w", err)
	}
	var out []PhraseStatRow
	for _, entry := range perCampaign {
		rows := entry.Rows
		if len(rows) == 0 && entry.Report != nil {
			rows = entry.Report.Rows
		}
		out = append(out, convert(rows)...)
	}
	return out, nil
}

// parsePhrasesCSV parses the CSV report variant. Headers are matched by a
// lowercase alias table (both English and Russian spellings); the separator
// is ';' with a ',' fallback, mirroring parseDailyStatsCSV. Summary rows
// («всего», «корректировка») and rows without a query are skipped.
func parsePhrasesCSV(body []byte, fallbackDate time.Time) ([]PhraseStatRow, error) {
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
			return nil, fmt.Errorf("ozon perf: parse phrases CSV: %w", err)
		}
	}
	if len(records) < 2 {
		return nil, nil
	}

	// The header row is the first row that contains a known query column —
	// some report variants prepend a title/period preamble line.
	headerIdx := -1
	queryAliases := []string{"поисковый запрос", "условие показа", "фраза", "запрос", "phrase", "search query", "searchquery", "query"}
	col := map[string]int{}
	for i, record := range records {
		candidate := map[string]int{}
		for j, name := range record {
			candidate[strings.ToLower(strings.TrimSpace(name))] = j
		}
		for _, alias := range queryAliases {
			if _, ok := candidate[alias]; ok {
				headerIdx = i
				col = candidate
				break
			}
		}
		if headerIdx >= 0 {
			break
		}
	}
	if headerIdx < 0 {
		return nil, fmt.Errorf("ozon perf: phrases CSV has no recognizable query column (header: %s)", strings.Join(records[0], ";"))
	}

	pick := func(row []string, names ...string) string {
		for _, name := range names {
			if idx, ok := col[name]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
		}
		return ""
	}
	// Numbers may arrive with regular or non-breaking thousand separators and
	// a comma decimal point.
	stripSpaces := func(raw string) string {
		return strings.ReplaceAll(strings.ReplaceAll(raw, " ", ""), "\u00a0", "")
	}
	parseI := func(raw string) int64 {
		v, _ := strconv.ParseInt(stripSpaces(raw), 10, 64)
		return v
	}
	parseF := func(raw string) float64 {
		v, _ := strconv.ParseFloat(strings.ReplaceAll(stripSpaces(raw), ",", "."), 64)
		return v
	}

	out := make([]PhraseStatRow, 0, len(records)-headerIdx-1)
	for _, record := range records[headerIdx+1:] {
		query := strings.ToLower(pick(record, queryAliases...))
		if query == "" || query == "всего" || query == "итого" || strings.HasPrefix(query, "корректировка") {
			continue
		}
		date := fallbackDate
		if parsed, dErr := time.Parse("2006-01-02", pick(record, "date", "день", "дата")); dErr == nil {
			date = parsed
		} else if parsed, dErr := time.Parse("02.01.2006", pick(record, "date", "день", "дата")); dErr == nil {
			date = parsed
		}
		row := PhraseStatRow{
			SKU:      parseI(pick(record, "sku", "sku продвигаемого товара")),
			Query:    query,
			Date:     date,
			Views:    parseI(pick(record, "показы", "views", "количество показов")),
			Clicks:   parseI(pick(record, "клики", "clicks", "количество кликов")),
			Orders:   parseI(pick(record, "заказы", "orders", "количество заказов")),
			SpendRub: parseF(pick(record, "расход", "расход, ₽", "расход, ₽, с ндс", "moneyspent", "затраты")),
		}
		if position := parseF(pick(record, "средняя позиция", "ср. позиция", "позиция", "avgposition", "position")); position > 0 {
			row.AvgPosition = &position
		}
		out = append(out, row)
	}
	return out, nil
}

// rawSnippet returns the first rawSnippetLimit bytes of a payload for logs.
func rawSnippet(body []byte) string {
	if len(body) > rawSnippetLimit {
		body = body[:rawSnippetLimit]
	}
	return string(body)
}
