package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

const fullStatsBatchSize = 25

type wbFullStatsResponse []struct {
	AdvertID int64 `json:"advertId"`
	Days     []struct {
		Date         string   `json:"date"`
		Views        int64    `json:"views"`
		Clicks       int64    `json:"clicks"`
		Sum          float64  `json:"sum"`
		Orders       *int64   `json:"orders"`
		SHKs         *int64   `json:"shks"`
		SumPrice     *float64 `json:"sum_price"`
		SumPriceAlt  *float64 `json:"sumPrice"`
		OrdersSumAlt *float64 `json:"ordersSum"`
		Atbs         *int64   `json:"atbs"`     // Добавления в корзину
		Canceled     *int64   `json:"canceled"`  // Технические отмены
		CPC          *float64 `json:"cpc"`       // Стоимость клика
		CTR          *float64 `json:"ctr"`       // Кликабельность
		CR           *float64 `json:"cr"`        // Конверсия
	} `json:"days"`
}

// GetCampaignStats fetches daily campaign statistics from the WB Advertising API.
// Official contract as of 2026-03-28:
// - GET /adv/v3/fullstats?ids=...&beginDate=YYYY-MM-DD&endDate=YYYY-MM-DD
func (c *Client) GetCampaignStats(ctx context.Context, token string, campaignIDs []int, dateFrom, dateTo string) ([]WBCampaignStatDTO, error) {
	if len(campaignIDs) == 0 {
		return nil, nil
	}

	result := make([]WBCampaignStatDTO, 0, len(campaignIDs))
	for start := 0; start < len(campaignIDs); start += fullStatsBatchSize {
		end := start + fullStatsBatchSize
		if end > len(campaignIDs) {
			end = len(campaignIDs)
		}

		// Delay between batches to avoid WB 429 rate limit (max ~1 req/sec for fullstats)
		if start > 0 {
			if err := sleepWithContext(ctx, 2*time.Second); err != nil {
				return result, err
			}
		}

		batch, err := c.getCampaignStatsBatch(ctx, token, campaignIDs[start:end], dateFrom, dateTo)
		if err != nil {
			return nil, err
		}

		for _, campaign := range batch {
			for _, day := range campaign.Days {
				result = append(result, WBCampaignStatDTO{
					AdvertID:     int(campaign.AdvertID),
					Date:         day.Date,
					Views:        day.Views,
					Clicks:       day.Clicks,
					Sum:          day.Sum,
					Orders:       day.Orders,
					OrderedItems: firstInt64Ptr(day.SHKs, day.Orders),
					Revenue:      firstFloat64Ptr(day.SumPrice, day.SumPriceAlt, day.OrdersSumAlt),
					Atbs:         day.Atbs,
					Canceled:     day.Canceled,
					CPC:          day.CPC,
					CTR:          day.CTR,
					CR:           day.CR,
				})
			}
		}
	}

	return result, nil
}

func (c *Client) getCampaignStatsBatch(ctx context.Context, token string, campaignIDs []int, dateFrom, dateTo string) (wbFullStatsResponse, error) {
	path := fmt.Sprintf("/adv/v3/fullstats?%s", fullStatsQuery(campaignIDs, dateFrom, dateTo))
	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		if isClient400(err) && len(campaignIDs) > 1 {
			mid := len(campaignIDs) / 2
			left, leftErr := c.getCampaignStatsBatch(ctx, token, campaignIDs[:mid], dateFrom, dateTo)
			right, rightErr := c.getCampaignStatsBatch(ctx, token, campaignIDs[mid:], dateFrom, dateTo)
			if leftErr != nil && rightErr != nil {
				return nil, leftErr
			}
			return append(left, right...), firstNonNilWBError(leftErr, rightErr)
		}
		if isClient400(err) && len(campaignIDs) == 1 {
			c.logger.Warn().
				Int("campaign_id", campaignIDs[0]).
				Str("path", path).
				Msg("skipping campaign stats fetch because WB returned 400 for a single advert")
			return nil, nil
		}
		return nil, err
	}

	var batch wbFullStatsResponse
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal campaign fullstats: %v", err))
	}

	return batch, nil
}

func fullStatsQuery(campaignIDs []int, dateFrom, dateTo string) string {
	values := url.Values{}
	values.Set("beginDate", dateFrom)
	values.Set("endDate", dateTo)
	values.Set("ids", joinIntSlice(campaignIDs))
	return values.Encode()
}

func joinIntSlice(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func isClient400(err error) bool {
	return err != nil && strings.Contains(err.Error(), "client error (400)")
}

func firstNonNilWBError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func firstInt64Ptr(values ...*int64) *int64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstFloat64Ptr(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
