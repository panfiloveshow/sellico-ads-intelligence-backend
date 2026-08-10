package ozon

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CPO («Оплата за заказ») promoted-orders report via the same async
// statistics flow as phrases (verified live 2026-08-10):
//
//	GET /api/client/statistics/all_sku_promo/orders/generate/json
//	    ?timeBounds.from=<RFC3339>&timeBounds.to=<RFC3339>   → {"UUID": "..."}
//	GET /api/client/statistics/{UUID}                        → {"state": "OK" | ...}
//	GET /api/client/statistics/report?UUID=...               → {"rows": [...]}
//
// The report speaks Russian formats on the wire: money/percent as
// «752,00» (comma decimals) and dates as DD.MM.YYYY. Both are parsed here at
// the boundary. The account-level limit of ONE report generation at a time is
// shared with the phrases report — callers must run cabinets sequentially and
// never interleave with a phrases pull for the same account.

// CPOOrderRow is one parsed row of the all_sku_promo orders report: one
// promoted order line with the bid and money the promotion charged for it.
type CPOOrderRow struct {
	Date         time.Time
	OrderID      string
	OrderNumber  string
	SKU          int64
	AdvSKU       int64
	VendorCode   string
	Name         string
	Quantity     int
	PriceRub     float64
	SalePriceRub float64
	BidPct       float64
	BidRub       float64 // abs_bid — the per-order charge the bid produced
	SpendRub     float64 // adv_money_spent
}

// GetAllSKUPromoOrders fetches the CPO promoted-orders report for [from, to]
// via the async statistics flow (submit → poll → download, helpers shared
// with the phrases report).
func (c *PerfClient) GetAllSKUPromoOrders(ctx context.Context, creds Credentials, from, to time.Time) ([]CPOOrderRow, error) {
	reportUUID, err := c.submitAllSKUPromoOrdersReport(ctx, creds, from, to)
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
	rows, err := parseCPOOrdersReport(body)
	if err != nil {
		c.logger.Warn().Err(err).Str("uuid", reportUUID).
			Str("raw_prefix", rawSnippet(body)).
			Msg("ozon cpo orders report parse failed")
		return nil, err
	}
	return rows, nil
}

// submitAllSKUPromoOrdersReport starts the async orders report (JSON variant)
// and returns its UUID.
func (c *PerfClient) submitAllSKUPromoOrdersReport(ctx context.Context, creds Credentials, from, to time.Time) (string, error) {
	query := url.Values{}
	query.Set("timeBounds.from", from.UTC().Format(time.RFC3339))
	query.Set("timeBounds.to", to.UTC().Format(time.RFC3339))
	body, err := c.doGet(ctx, creds, "/api/client/statistics/all_sku_promo/orders/generate/json", query)
	if err != nil {
		return "", fmt.Errorf("submit cpo orders report: %w", err)
	}
	var parsed struct {
		UUIDUpper string `json:"UUID"`
		UUIDLower string `json:"uuid"`
	}
	if err := decodeJSON(body, &parsed, "all_sku_promo orders submit"); err != nil {
		c.logger.Warn().Str("raw_prefix", rawSnippet(body)).Msg("ozon cpo orders submit: undecodable response")
		return "", err
	}
	reportUUID := parsed.UUIDUpper
	if reportUUID == "" {
		reportUUID = parsed.UUIDLower
	}
	if reportUUID == "" {
		return "", fmt.Errorf("ozon perf: all_sku_promo orders submit returned no UUID (raw: %s)", rawSnippet(body))
	}
	return reportUUID, nil
}

// wireCPOOrderRow mirrors the report row verbatim: every field is a string on
// the wire, numbers use Russian decimal commas, dates DD.MM.YYYY.
type wireCPOOrderRow struct {
	Date          string `json:"date"`
	OrderID       string `json:"order_id"`
	OrderNumber   string `json:"order_number"`
	SKU           string `json:"sku"`
	AdvSKU        string `json:"adv_sku"`
	VendorCode    string `json:"vendor_code"`
	Name          string `json:"name"`
	Quantity      string `json:"quantity"`
	Price         string `json:"price"`
	SalePrice     string `json:"sale_price"`
	Bid           string `json:"bid"`
	AbsBid        string `json:"abs_bid"`
	AdvMoneySpent string `json:"adv_money_spent"`
}

// parseCPOOrdersReport parses the downloaded {"rows":[...]} JSON payload.
// Rows without an order_id are skipped (summary/garbage lines); an
// unparseable date drops the row too — a promoted order without its date
// cannot be aggregated into any window.
func parseCPOOrdersReport(body []byte) ([]CPOOrderRow, error) {
	var envelope struct {
		Rows []wireCPOOrderRow `json:"rows"`
	}
	if err := decodeJSON(body, &envelope, "cpo orders report"); err != nil {
		return nil, err
	}
	out := make([]CPOOrderRow, 0, len(envelope.Rows))
	for _, wire := range envelope.Rows {
		orderID := strings.TrimSpace(wire.OrderID)
		if orderID == "" {
			continue
		}
		date, err := parseRuDate(wire.Date)
		if err != nil {
			continue
		}
		out = append(out, CPOOrderRow{
			Date:         date,
			OrderID:      orderID,
			OrderNumber:  strings.TrimSpace(wire.OrderNumber),
			SKU:          parseRuInt(wire.SKU),
			AdvSKU:       parseRuInt(wire.AdvSKU),
			VendorCode:   strings.TrimSpace(wire.VendorCode),
			Name:         strings.TrimSpace(wire.Name),
			Quantity:     int(parseRuInt(wire.Quantity)),
			PriceRub:     parseRuDecimal(wire.Price),
			SalePriceRub: parseRuDecimal(wire.SalePrice),
			BidPct:       parseRuDecimal(wire.Bid),
			BidRub:       parseRuDecimal(wire.AbsBid),
			SpendRub:     parseRuDecimal(wire.AdvMoneySpent),
		})
	}
	return out, nil
}

// parseRuDate parses the report's DD.MM.YYYY dates (with a YYYY-MM-DD
// fallback in case the format ever normalises).
func parseRuDate(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if parsed, err := time.Parse("02.01.2006", trimmed); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02", trimmed)
}

// stripRuSeparators removes regular and non-breaking thousand separators —
// the same tolerance the phrases CSV parser applies.
func stripRuSeparators(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(raw), " ", ""), "\u00a0", "")
}

// parseRuDecimal parses a Russian-formatted decimal («1 752,00») into a
// float64; malformed values yield 0 (defensive, matching the phrases parser).
func parseRuDecimal(raw string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(stripRuSeparators(raw), ",", "."), 64)
	return v
}

// parseRuInt parses an integer that may carry spaces or a decimal tail.
func parseRuInt(raw string) int64 {
	cleaned := stripRuSeparators(raw)
	if v, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
		return v
	}
	return int64(parseRuDecimal(cleaned))
}
