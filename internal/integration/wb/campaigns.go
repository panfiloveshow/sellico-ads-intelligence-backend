package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

const advertsV2StatusesQuery = "/api/advert/v2/adverts?statuses=-1,4,7,8,9,11"

type wbAdvertsV2Response struct {
	Adverts []wbAdvertV2 `json:"adverts"`
}

type wbAdvertV2 struct {
	ID          int64  `json:"id"`
	AdvertID    int64  `json:"advertId"`
	Name        string `json:"name"`
	Type        int    `json:"type"`
	Status      int    `json:"status"`
	DailyBudget int64  `json:"dailyBudget"`
	BidType     string `json:"bid_type"`
	PaymentType string `json:"paymentType"`
	Settings    struct {
		Name        string `json:"name"`
		PaymentType string `json:"payment_type"`
	} `json:"settings"`
	AutoParams struct {
		NMs []int64 `json:"nms"`
	} `json:"autoParams"`
	NMSettings []struct {
		NMID int64 `json:"nm_id"`
	} `json:"nm_settings"`
}

// ListCampaigns fetches campaigns from the current WB advertising API.
// Official contract as of 2026-03-28:
// - GET /api/advert/v2/adverts?statuses=-1,4,7,8,9,11
func (c *Client) ListCampaigns(ctx context.Context, token string) ([]WBCampaignDTO, error) {
	_, body, err := c.doRequest(ctx, "GET", advertsV2StatusesQuery, token, nil)
	if err != nil {
		return nil, err
	}

	var response wbAdvertsV2Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal adverts v2 campaigns: %v", err))
	}

	result := make([]WBCampaignDTO, 0, len(response.Adverts))
	for _, advert := range response.Adverts {
		advertID := firstNonZeroInt64(advert.ID, advert.AdvertID)
		if advertID == 0 {
			continue
		}

		name := advert.Name
		if name == "" {
			name = advert.Settings.Name
		}

		paymentType := advert.PaymentType
		if paymentType == "" {
			paymentType = advert.Settings.PaymentType
		}

		dto := WBCampaignDTO{
			AdvertID:    int(advertID),
			Name:        name,
			Status:      advert.Status,
			Type:        advert.Type,
			DailyBudget: rawOptionalInt64(advert.DailyBudget),
			BidType:     mapRawBidType(advert.BidType),
			PaymentType: paymentType,
			NMIDs:       collectAdvertNMIDs(advert),
		}
		result = append(result, dto)
	}

	return result, nil
}

func collectAdvertNMIDs(advert wbAdvertV2) []int64 {
	if len(advert.AutoParams.NMs) == 0 && len(advert.NMSettings) == 0 {
		return nil
	}

	seen := make(map[int64]struct{}, len(advert.AutoParams.NMs)+len(advert.NMSettings))
	result := make([]int64, 0, len(advert.AutoParams.NMs)+len(advert.NMSettings))
	for _, nmID := range advert.AutoParams.NMs {
		if nmID == 0 {
			continue
		}
		if _, ok := seen[nmID]; ok {
			continue
		}
		seen[nmID] = struct{}{}
		result = append(result, nmID)
	}
	for _, item := range advert.NMSettings {
		if item.NMID == 0 {
			continue
		}
		if _, ok := seen[item.NMID]; ok {
			continue
		}
		seen[item.NMID] = struct{}{}
		result = append(result, item.NMID)
	}
	return result
}

func rawString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func rawInt(value interface{}) int {
	return int(rawInt64(value))
}

func rawInt64(value interface{}) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		v, _ := typed.Int64()
		return v
	case string:
		v, _ := strconv.ParseInt(typed, 10, 64)
		return v
	default:
		return 0
	}
}

func rawOptionalInt64(value interface{}) *int64 {
	parsed := rawInt64(value)
	if parsed == 0 {
		return nil
	}
	return &parsed
}

func mapRawBidType(value interface{}) int {
	switch rawString(value) {
	case "unified":
		return 1
	case "manual":
		return 0
	default:
		return rawInt(value)
	}
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
