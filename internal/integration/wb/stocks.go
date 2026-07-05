package wb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrStatsScopeMissing is returned when the WB token lacks the "Статистика"
// category — the statistics-api answers 401/403.
var ErrStatsScopeMissing = errors.New("wb token missing statistics scope")

type wbSupplierStock struct {
	NmID     int64 `json:"nmId"`
	Quantity int   `json:"quantity"` // available for sale (matches the seller cabinet "Остаток")
}

// ListSupplierStocks returns the seller's real FBW stock summed per nmID from
// the WB Statistics API. This is the true warehouse stock (like the seller
// cabinet shows), unlike the card.wb.ru storefront quantity. Needs the
// "Статистика" token category — 401/403 → ErrStatsScopeMissing.
func (c *Client) ListSupplierStocks(ctx context.Context, token string) (map[int64]int, error) {
	// dateFrom far in the past → full current stock snapshot.
	const path = "/api/v1/supplier/stocks?dateFrom=2020-01-01"
	resp, body, err := c.doStatisticsRequest(ctx, http.MethodGet, path, token, nil)
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
	var rows []wbSupplierStock
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("unmarshal supplier stocks: %w", err)
	}
	out := make(map[int64]int, len(rows))
	for _, r := range rows {
		if r.NmID != 0 {
			out[r.NmID] += r.Quantity
		}
	}
	return out, nil
}
