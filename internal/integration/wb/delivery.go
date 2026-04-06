package wb

import (
	"context"
	"encoding/json"
	"fmt"
)

// DeliveryInfo represents delivery data for a product in a specific region.
type DeliveryInfo struct {
	ProductNMID  int64  `json:"product_nm_id"`
	Region       string `json:"region"`
	Warehouse    string `json:"warehouse"`
	DeliveryDays int    `json:"delivery_days"`
	InStock      bool   `json:"in_stock"`
	StockCount   int    `json:"stock_count"`
}

// GetProductDelivery fetches delivery info for a product via WB content card API.
// Uses the public card detail endpoint — no auth needed.
func (c *Client) GetProductDelivery(ctx context.Context, nmID int64) ([]DeliveryInfo, error) {
	path := fmt.Sprintf("/cards/v2/detail?appType=1&curr=rub&dest=-1257786&nm=%d", nmID)

	origBase := c.baseURL
	c.baseURL = c.contentURL
	defer func() { c.baseURL = origBase }()

	_, body, err := c.doRequestInner(ctx, "GET", path, "", nil)
	if err != nil {
		return nil, fmt.Errorf("product delivery request: %w", err)
	}

	var raw struct {
		Data struct {
			Products []struct {
				ID    int64 `json:"id"`
				Sizes []struct {
					Stocks []struct {
						Wh   int `json:"wh"`
						Qty  int `json:"qty"`
						Time int `json:"time"` // delivery time in days
					} `json:"stocks"`
				} `json:"sizes"`
			} `json:"products"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse product delivery: %w", err)
	}

	var results []DeliveryInfo
	for _, p := range raw.Data.Products {
		if p.ID != nmID {
			continue
		}
		totalStock := 0
		minDelivery := 999
		for _, size := range p.Sizes {
			for _, stock := range size.Stocks {
				totalStock += stock.Qty
				if stock.Time > 0 && stock.Time < minDelivery {
					minDelivery = stock.Time
				}
			}
		}
		if minDelivery == 999 {
			minDelivery = 0
		}

		results = append(results, DeliveryInfo{
			ProductNMID:  nmID,
			Region:       "default",
			Warehouse:    "wb",
			DeliveryDays: minDelivery,
			InStock:      totalStock > 0,
			StockCount:   totalStock,
		})
	}

	return results, nil
}
