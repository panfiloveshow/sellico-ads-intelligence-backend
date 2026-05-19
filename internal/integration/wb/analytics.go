package wb

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// GetSalesFunnel fetches Sales Funnel v3 data from the WB Analytics API.
// WB API endpoint: POST /adv/v2/analytics/sales-funnel
func (c *Client) GetSalesFunnel(ctx context.Context, token string, params SalesFunnelParams) ([]WBSalesFunnelDTO, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal sales funnel request: %v", err))
	}

	_, body, err := c.doRequest(ctx, "POST", "/adv/v2/analytics/sales-funnel", token, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	var result []WBSalesFunnelDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal sales funnel: %v", err))
	}

	return result, nil
}

type salesFunnelProductsV3Request struct {
	SelectedPeriod struct {
		Begin string `json:"begin"`
		End   string `json:"end"`
	} `json:"selectedPeriod"`
	NmIDs []int64 `json:"nmIDs,omitempty"`
	Limit int     `json:"limit,omitempty"`
}

type salesFunnelProductsV3Response struct {
	Data struct {
		Products []salesFunnelProductV3Item `json:"products"`
	} `json:"data"`
	Products []salesFunnelProductV3Item `json:"products"`
}

type salesFunnelProductV3Item struct {
	Product struct {
		NmID int64 `json:"nmID"`
		NMID int64 `json:"nmId"`
	} `json:"product"`
	Statistic struct {
		Selected struct {
			CartCount int64 `json:"cartCount"`
		} `json:"selected"`
	} `json:"statistic"`
	NmID      int64 `json:"nmID"`
	NMID      int64 `json:"nmId"`
	CartCount int64 `json:"cartCount"`
}

type WBSalesFunnelProductDTO struct {
	NmID      int64  `json:"nmId"`
	DateFrom  string `json:"dateFrom"`
	DateTo    string `json:"dateTo"`
	CartCount int64  `json:"cartCount"`
}

// GetSellerAnalytics fetches Seller Analytics data from the WB Analytics API.
// WB API endpoint: GET /adv/v2/analytics/seller
// Returns parsed CSV data as DTOs.
func (c *Client) GetSellerAnalytics(ctx context.Context, token string, dateFrom, dateTo string) ([]WBSellerAnalyticsDTO, error) {
	path := fmt.Sprintf("/adv/v2/analytics/seller?dateFrom=%s&dateTo=%s", dateFrom, dateTo)

	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	return parseSellerAnalyticsCSV(body)
}

// GetSalesFunnelProductsV3 fetches period-level product funnel data.
// WB API endpoint: POST /api/analytics/v3/sales-funnel/products
func (c *Client) GetSalesFunnelProductsV3(ctx context.Context, token string, params SalesFunnelParams) ([]WBSalesFunnelProductDTO, error) {
	var req salesFunnelProductsV3Request
	req.SelectedPeriod.Begin = params.DateFrom
	req.SelectedPeriod.End = params.DateTo
	req.NmIDs = params.NmIDs
	req.Limit = 1000

	data, err := json.Marshal(req)
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal sales funnel products v3 request: %v", err))
	}

	_, body, err := c.doAnalyticsRequest(ctx, "POST", "/api/analytics/v3/sales-funnel/products", token, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	var response salesFunnelProductsV3Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal sales funnel products v3: %v", err))
	}

	items := response.Data.Products
	if len(items) == 0 {
		items = response.Products
	}
	result := make([]WBSalesFunnelProductDTO, 0, len(items))
	for _, item := range items {
		nmID := item.Product.NmID
		if nmID == 0 {
			nmID = item.Product.NMID
		}
		if nmID == 0 {
			nmID = item.NmID
		}
		if nmID == 0 {
			nmID = item.NMID
		}
		if nmID == 0 {
			continue
		}
		cartCount := item.Statistic.Selected.CartCount
		if cartCount == 0 {
			cartCount = item.CartCount
		}
		result = append(result, WBSalesFunnelProductDTO{
			NmID:      nmID,
			DateFrom:  params.DateFrom,
			DateTo:    params.DateTo,
			CartCount: cartCount,
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

// parseSellerAnalyticsCSV parses CSV response into WBSellerAnalyticsDTO structs.
// Expected CSV columns: query, medianPosition, frequency, date
func parseSellerAnalyticsCSV(data []byte) ([]WBSellerAnalyticsDTO, error) {
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.TrimLeadingSpace = true

	// Read header row
	header, err := reader.Read()
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("read CSV header: %v", err))
	}

	colIndex := make(map[string]int, len(header))
	for i, col := range header {
		colIndex[strings.TrimSpace(col)] = i
	}

	// Validate required columns
	requiredCols := []string{"query", "medianPosition", "frequency", "date"}
	for _, col := range requiredCols {
		if _, ok := colIndex[col]; !ok {
			return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("missing CSV column: %s", col))
		}
	}

	var result []WBSellerAnalyticsDTO
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("read CSV row: %v", err))
		}

		medianPos, err := strconv.ParseFloat(record[colIndex["medianPosition"]], 64)
		if err != nil {
			return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("parse medianPosition %q: %v", record[colIndex["medianPosition"]], err))
		}

		freq, err := strconv.ParseInt(record[colIndex["frequency"]], 10, 64)
		if err != nil {
			return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("parse frequency %q: %v", record[colIndex["frequency"]], err))
		}

		result = append(result, WBSellerAnalyticsDTO{
			Query:          record[colIndex["query"]],
			MedianPosition: medianPos,
			Frequency:      freq,
			Date:           record[colIndex["date"]],
		})
	}

	return result, nil
}
