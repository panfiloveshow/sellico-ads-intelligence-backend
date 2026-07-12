package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrStatsScopeMissing is returned when the WB token lacks the required
// category or token type — the seller-analytics-api answers 401/403.
var ErrStatsScopeMissing = errors.New("wb token missing statistics scope")

type wbStocksReportItem struct {
	NmID     int64 `json:"nmId"`
	Quantity int   `json:"quantity"`
}

type wbStocksReportResponse struct {
	Data struct {
		Items []wbStocksReportItem `json:"items"`
	} `json:"data"`
}

// ListSupplierStocks returns the seller's real FBW stock summed per nmID from
// the WB Seller Analytics API. This is the true warehouse stock (like the
// seller cabinet shows), unlike the card.wb.ru storefront quantity.
//
// Needs the "Аналитика" token category, AND a Personal token, Service token,
// or a Basic token with a registered service secret — a plain self-issued
// Basic token gets 401/403 regardless of category. 401/403 → ErrStatsScopeMissing.
//
// WB retired the legacy GET /api/v1/supplier/stocks (Statistics API) on
// 2026-07-14; this is its replacement.
func (c *Client) ListSupplierStocks(ctx context.Context, token string) (map[int64]int, error) {
	const path = "/api/analytics/v1/stocks-report/wb-warehouses"

	// No nmIds filter → full snapshot. 250000 is the endpoint's max page size;
	// a seller with a larger catalog would need offset pagination, but the
	// repricer's 12s stock-sync budget (see syncCabinetStocks) only allows one page.
	reqBody, err := json.Marshal(map[string]int{"limit": 250000, "offset": 0})
	if err != nil {
		return nil, fmt.Errorf("marshal stocks report request: %w", err)
	}

	resp, body, err := c.doAnalyticsRequest(ctx, http.MethodPost, path, token, bytes.NewReader(reqBody))
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, ErrStatsScopeMissing
		}
		return nil, err
	}
	if resp != nil && resp.StatusCode >= http.StatusBadRequest {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ErrStatsScopeMissing
		}
		return nil, fmt.Errorf("wb stocks: status %d", resp.StatusCode)
	}

	var parsed wbStocksReportResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal supplier stocks: %w", err)
	}
	out := make(map[int64]int, len(parsed.Data.Items))
	for _, r := range parsed.Data.Items {
		if r.NmID != 0 {
			out[r.NmID] += r.Quantity
		}
	}
	return out, nil
}
