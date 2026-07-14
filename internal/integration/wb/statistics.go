package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// WB /adv/v3/fullstats accepts up to 50 advert IDs per request.
const fullStatsBatchSize = 50

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
		Canceled     *int64   `json:"canceled"` // Технические отмены
		CPC          *float64 `json:"cpc"`      // Стоимость клика
		CTR          *float64 `json:"ctr"`      // Кликабельность
		CR           *float64 `json:"cr"`       // Конверсия
		Apps         []struct {
			NMS []struct {
				NmID      int64    `json:"nmId"`
				Name      string   `json:"name"`
				Views     int64    `json:"views"`
				Clicks    int64    `json:"clicks"`
				Sum       float64  `json:"sum"`
				Orders    *int64   `json:"orders"`
				SHKs      *int64   `json:"shks"`
				SumPrice  *float64 `json:"sum_price"`
				SumPrice2 *float64 `json:"sumPrice"`
				Atbs      *int64   `json:"atbs"`
				Canceled  *int64   `json:"canceled"`
			} `json:"nms"`
		} `json:"apps"`
		NMS []struct {
			NmID      int64    `json:"nmId"`
			Name      string   `json:"name"`
			Views     int64    `json:"views"`
			Clicks    int64    `json:"clicks"`
			Sum       float64  `json:"sum"`
			Orders    *int64   `json:"orders"`
			SHKs      *int64   `json:"shks"`
			SumPrice  *float64 `json:"sum_price"`
			SumPrice2 *float64 `json:"sumPrice"`
			Atbs      *int64   `json:"atbs"`
			Canceled  *int64   `json:"canceled"`
		} `json:"nms"`
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
	var firstErr error
	consecutiveRateLimits := 0
	for start := 0; start < len(campaignIDs); start += fullStatsBatchSize {
		end := start + fullStatsBatchSize
		if end > len(campaignIDs) {
			end = len(campaignIDs)
		}

		if start > 0 && c.fullStatsInterBatchDelay > 0 {
			if err := sleepWithContext(ctx, c.fullStatsInterBatchDelay); err != nil {
				return result, err
			}
		}

		batch, err := c.getCampaignStatsBatch(ctx, token, campaignIDs[start:end], dateFrom, dateTo)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if isRateLimitError(err) {
				consecutiveRateLimits++
			} else {
				consecutiveRateLimits = 0
			}
			c.logger.Warn().
				Err(err).
				Ints("campaign_ids", campaignIDs[start:end]).
				Msg("skipping fullstats batch after WB error")
			if consecutiveRateLimits >= 3 {
				c.logger.Warn().
					Int("consecutive_rate_limits", consecutiveRateLimits).
					Msg("aborting fullstats sync after repeated WB 429 responses")
				break
			}
			continue
		}
		consecutiveRateLimits = 0

		for _, campaign := range batch {
			for _, day := range campaign.Days {
				productStats := make([]WBProductStatDTO, 0, len(day.NMS))
				for _, nm := range day.NMS {
					if nm.NmID == 0 {
						continue
					}
					productStats = append(productStats, WBProductStatDTO{
						NmID:     nm.NmID,
						Name:     nm.Name,
						Date:     day.Date,
						Views:    nm.Views,
						Clicks:   nm.Clicks,
						Sum:      nm.Sum,
						Orders:   nm.Orders,
						SHKs:     nm.SHKs,
						Revenue:  firstFloat64Ptr(nm.SumPrice, nm.SumPrice2),
						SumPrice: firstFloat64Ptr(nm.SumPrice, nm.SumPrice2),
						Atbs:     nm.Atbs,
						Canceled: nm.Canceled,
					})
				}
				for _, app := range day.Apps {
					for _, nm := range app.NMS {
						if nm.NmID == 0 {
							continue
						}
						productStats = append(productStats, WBProductStatDTO{
							NmID:     nm.NmID,
							Name:     nm.Name,
							Date:     day.Date,
							Views:    nm.Views,
							Clicks:   nm.Clicks,
							Sum:      nm.Sum,
							Orders:   nm.Orders,
							SHKs:     nm.SHKs,
							Revenue:  firstFloat64Ptr(nm.SumPrice, nm.SumPrice2),
							SumPrice: firstFloat64Ptr(nm.SumPrice, nm.SumPrice2),
							Atbs:     nm.Atbs,
							Canceled: nm.Canceled,
						})
					}
				}
				atbs := day.Atbs
				if atbs == nil || *atbs == 0 {
					if productAtbs := sumProductAtbs(productStats); productAtbs > 0 {
						atbs = &productAtbs
					}
				}
				result = append(result, WBCampaignStatDTO{
					AdvertID:     int(campaign.AdvertID),
					Date:         day.Date,
					Views:        day.Views,
					Clicks:       day.Clicks,
					Sum:          day.Sum,
					Orders:       day.Orders,
					OrderedItems: firstInt64Ptr(day.SHKs, day.Orders),
					SHKs:         day.SHKs,
					Revenue:      firstFloat64Ptr(day.SumPrice, day.SumPriceAlt, day.OrdersSumAlt),
					Atbs:         atbs,
					Canceled:     day.Canceled,
					CPC:          day.CPC,
					CTR:          day.CTR,
					CR:           day.CR,
					Products:     aggregateProductStats(productStats),
				})
			}
		}
	}

	return result, firstErr
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
			// Preserve the per-campaign failure so the caller marks the sync
			// partial. Treating this as a successful empty response makes stale
			// analytics indistinguishable from a genuine zero-activity period.
			return nil, err
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

func isRateLimitError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limited"))
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

func sumProductAtbs(products []WBProductStatDTO) int64 {
	var total int64
	for _, product := range products {
		if product.Atbs != nil {
			total += *product.Atbs
		}
	}
	return total
}

func aggregateProductStats(items []WBProductStatDTO) []WBProductStatDTO {
	if len(items) <= 1 {
		return items
	}
	type key struct {
		nmID int64
		date string
	}
	byKey := make(map[key]WBProductStatDTO, len(items))
	order := make([]key, 0, len(items))
	for _, item := range items {
		k := key{nmID: item.NmID, date: item.Date}
		existing, ok := byKey[k]
		if !ok {
			byKey[k] = item
			order = append(order, k)
			continue
		}
		if existing.Name == "" {
			existing.Name = item.Name
		}
		existing.Views += item.Views
		existing.Clicks += item.Clicks
		existing.Sum += item.Sum
		existing.Orders = addInt64Ptrs(existing.Orders, item.Orders)
		existing.SHKs = addInt64Ptrs(existing.SHKs, item.SHKs)
		existing.Revenue = addFloat64Ptrs(existing.Revenue, item.Revenue)
		existing.SumPrice = addFloat64Ptrs(existing.SumPrice, item.SumPrice)
		existing.Atbs = addInt64Ptrs(existing.Atbs, item.Atbs)
		existing.Canceled = addInt64Ptrs(existing.Canceled, item.Canceled)
		byKey[k] = existing
	}
	result := make([]WBProductStatDTO, 0, len(byKey))
	for _, k := range order {
		result = append(result, byKey[k])
	}
	return result
}

func addInt64Ptrs(a, b *int64) *int64 {
	if a == nil && b == nil {
		return nil
	}
	var total int64
	if a != nil {
		total += *a
	}
	if b != nil {
		total += *b
	}
	return &total
}

func addFloat64Ptrs(a, b *float64) *float64 {
	if a == nil && b == nil {
		return nil
	}
	var total float64
	if a != nil {
		total += *a
	}
	if b != nil {
		total += *b
	}
	return &total
}
