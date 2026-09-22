package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// SalesFunnelProductsMaxNmIDs — максимум nmIds в одном запросе и максимум limit
// (спека 11-analytics, ItemsRequest).
const SalesFunnelProductsMaxNmIDs = 1000

// salesFunnelProductsV3Request — ItemsRequest из спеки: selectedPeriod.{start,end}
// обязательны; формат begin/end был у v2, отключённой 09.12.2025.
type salesFunnelProductsV3Request struct {
	SelectedPeriod struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"selectedPeriod"`
	NmIDs []int64 `json:"nmIds,omitempty"`
	Limit int     `json:"limit,omitempty"`
}

type salesFunnelProductsV3Response struct {
	Data struct {
		Products []salesFunnelProductV3Item `json:"products"`
	} `json:"data"`
}

type salesFunnelProductV3Item struct {
	Product struct {
		NmID int64 `json:"nmId"`
	} `json:"product"`
	Statistic struct {
		Selected struct {
			OpenCount  int64 `json:"openCount"`
			CartCount  int64 `json:"cartCount"`
			OrderCount int64 `json:"orderCount"`
		} `json:"selected"`
	} `json:"statistic"`
}

type WBSalesFunnelProductDTO struct {
	NmID       int64  `json:"nmId"`
	DateFrom   string `json:"dateFrom"`
	DateTo     string `json:"dateTo"`
	OpenCount  int64  `json:"openCount"`
	CartCount  int64  `json:"cartCount"`
	OrderCount int64  `json:"orderCount"`
}

// GetSalesFunnelProductsV3 fetches period-level product funnel data.
// WB API endpoint: POST /api/analytics/v3/sales-funnel/products.
// Не больше SalesFunnelProductsMaxNmIDs артикулов за вызов: тогда одна
// страница (limit=1000) покрывает все переданные nmIds.
func (c *Client) GetSalesFunnelProductsV3(ctx context.Context, token string, params SalesFunnelParams) ([]WBSalesFunnelProductDTO, error) {
	if len(params.NmIDs) > SalesFunnelProductsMaxNmIDs {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("sales funnel products v3: at most %d nmIds per request, got %d", SalesFunnelProductsMaxNmIDs, len(params.NmIDs)))
	}
	var req salesFunnelProductsV3Request
	req.SelectedPeriod.Start = params.DateFrom
	req.SelectedPeriod.End = params.DateTo
	req.NmIDs = params.NmIDs
	req.Limit = SalesFunnelProductsMaxNmIDs

	data, err := json.Marshal(req)
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal sales funnel products v3 request: %v", err))
	}

	// 429 не повторяем: у Базового токена 2 запроса в час, повтор через минуту
	// только тратит лимит — паузу по ошибке ставит синк.
	_, body, err := c.doAnalyticsRequest(withoutRateLimitRetry(ctx), "POST", "/api/analytics/v3/sales-funnel/products", token, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	var response salesFunnelProductsV3Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal sales funnel products v3: %v", err))
	}

	items := response.Data.Products
	result := make([]WBSalesFunnelProductDTO, 0, len(items))
	for _, item := range items {
		if item.Product.NmID == 0 {
			continue
		}
		result = append(result, WBSalesFunnelProductDTO{
			NmID:       item.Product.NmID,
			DateFrom:   params.DateFrom,
			DateTo:     params.DateTo,
			OpenCount:  item.Statistic.Selected.OpenCount,
			CartCount:  item.Statistic.Selected.CartCount,
			OrderCount: item.Statistic.Selected.OrderCount,
		})
	}

	return result, nil
}

func SalesFunnelDefaultDateRange(now time.Time) (string, string) {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("MSK", 3*60*60)
	}
	nowMSK := now.In(location)
	yesterday := time.Date(nowMSK.Year(), nowMSK.Month(), nowMSK.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -1)
	return yesterday.AddDate(0, 0, -30).Format("2006-01-02"), yesterday.Format("2006-01-02")
}
