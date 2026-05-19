package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// WBOrderReportDTO is a row from GET /api/v1/supplier/orders.
type WBOrderReportDTO struct {
	Date            string  `json:"date"`
	LastChangeDate  string  `json:"lastChangeDate"`
	SupplierArticle string  `json:"supplierArticle"`
	NmID            int64   `json:"nmId"`
	Category        string  `json:"category"`
	Subject         string  `json:"subject"`
	Brand           string  `json:"brand"`
	TotalPrice      float64 `json:"totalPrice"`
	FinishedPrice   float64 `json:"finishedPrice"`
	PriceWithDisc   float64 `json:"priceWithDisc"`
	IsCancel        bool    `json:"isCancel"`
	SRID            string  `json:"srid"`
}

// WBSaleReportDTO is a row from GET /api/v1/supplier/sales.
type WBSaleReportDTO struct {
	Date            string  `json:"date"`
	LastChangeDate  string  `json:"lastChangeDate"`
	SupplierArticle string  `json:"supplierArticle"`
	NmID            int64   `json:"nmId"`
	Category        string  `json:"category"`
	Subject         string  `json:"subject"`
	Brand           string  `json:"brand"`
	TotalPrice      float64 `json:"totalPrice"`
	FinishedPrice   float64 `json:"finishedPrice"`
	PriceWithDisc   float64 `json:"priceWithDisc"`
	ForPay          float64 `json:"forPay"`
	SaleID          string  `json:"saleID"`
	SRID            string  `json:"srid"`
}

func (c *Client) GetSupplierOrders(ctx context.Context, token, dateFrom string, flag int) ([]WBOrderReportDTO, error) {
	path := fmt.Sprintf("/api/v1/supplier/orders?dateFrom=%s&flag=%d", url.QueryEscape(dateFrom), flag)
	_, body, err := c.doStatisticsRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBOrderReportDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal supplier orders: %w", err)
	}
	return result, nil
}

func (c *Client) GetSupplierSales(ctx context.Context, token, dateFrom string, flag int) ([]WBSaleReportDTO, error) {
	path := fmt.Sprintf("/api/v1/supplier/sales?dateFrom=%s&flag=%d", url.QueryEscape(dateFrom), flag)
	_, body, err := c.doStatisticsRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBSaleReportDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal supplier sales: %w", err)
	}
	return result, nil
}
