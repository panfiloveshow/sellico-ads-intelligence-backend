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
