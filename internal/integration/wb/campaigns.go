package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

const advertsV2StatusesQuery = "/api/advert/v2/adverts?statuses=-1,4,7,8,9,11"
const advertsV2DetailStatuses = "-1,4,7,8,9,11"
const advertsV2DetailBatchSize = 50

type WBPromotionCountDTO struct {
	AdvertID int64  `json:"advertId"`
	Type     int    `json:"type"`
	Status   int    `json:"status"`
	Count    int    `json:"count"`
	Name     string `json:"name,omitempty"`
}

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
		Placements  struct {
			Search          *bool `json:"search"`
			Recommendations *bool `json:"recommendations"`
		} `json:"placements"`
	} `json:"settings"`
	Timestamps struct {
		Created string `json:"created"`
		Started string `json:"started"`
		Updated string `json:"updated"`
		Deleted string `json:"deleted"`
	} `json:"timestamps"`
	AutoParams struct {
		NMs []int64 `json:"nms"`
	} `json:"autoParams"`
	NMSettings []struct {
		NMID        int64           `json:"nm_id"`
		NMIDCamel   int64           `json:"nmId"`
		Subject     wbAdvertSubject `json:"subject"`
		SubjectID   int64           `json:"subject_id"`
		SubjectName string          `json:"subject_name"`
		BidsKopecks json.RawMessage `json:"bids_kopecks"`
	} `json:"nm_settings"`
}

type wbAdvertSubject struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListPromotionCounts fetches the WB campaign inventory grouped by promotion status.
// WB API endpoint: GET /adv/v1/promotion/count
func (c *Client) ListPromotionCounts(ctx context.Context, token string) ([]WBPromotionCountDTO, error) {
	_, body, err := c.doRequest(ctx, "GET", "/adv/v1/promotion/count", token, nil)
	if err != nil {
		return nil, err
	}

	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal promotion count: %v", err))
	}

	items := make([]WBPromotionCountDTO, 0)
	collectPromotionCountItems(raw, &items)
	return items, nil
}

// ListCampaigns fetches campaigns from the current WB advertising API.
// Official contract as of 2026-03-28:
// - GET /api/advert/v2/adverts?statuses=-1,4,7,8,9,11
func (c *Client) ListCampaigns(ctx context.Context, token string) ([]WBCampaignDTO, error) {
	adverts, err := c.fetchAdvertsV2(ctx, token, advertsV2StatusesQuery)
	if err != nil {
		return nil, err
	}
	if len(adverts) > 0 {
		adverts = c.enrichAdvertsWithoutNMIDs(ctx, token, adverts)
	}

	result := make([]WBCampaignDTO, 0, len(adverts))
	for _, advert := range adverts {
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
			AdvertID:                 int(advertID),
			Name:                     name,
			Status:                   advert.Status,
			Type:                     advert.Type,
			DailyBudget:              rawOptionalInt64(advert.DailyBudget),
			BidType:                  mapRawBidType(advert.BidType),
			PaymentType:              paymentType,
			NMIDs:                    collectAdvertNMIDs(advert),
			Products:                 collectAdvertProductSettings(advert),
			PlacementSearch:          advert.Settings.Placements.Search,
			PlacementRecommendations: advert.Settings.Placements.Recommendations,
			WBCreatedAt:              parseOptionalWBTime(advert.Timestamps.Created),
			WBStartedAt:              parseOptionalWBTime(advert.Timestamps.Started),
			WBUpdatedAt:              parseOptionalWBTime(advert.Timestamps.Updated),
			WBDeletedAt:              parseOptionalWBTime(advert.Timestamps.Deleted),
		}
		result = append(result, dto)
	}

	return result, nil
}

func collectPromotionCountItems(value interface{}, out *[]WBPromotionCountDTO) {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			collectPromotionCountItems(item, out)
		}
	case map[string]interface{}:
		advertID := firstNonZeroInt64(
			rawInt64(typed["advertId"]),
			rawInt64(typed["advert_id"]),
			rawInt64(typed["id"]),
		)
		if advertID != 0 {
			*out = append(*out, WBPromotionCountDTO{
				AdvertID: advertID,
				Type:     rawInt(typed["type"]),
				Status:   rawInt(typed["status"]),
				Count:    rawInt(typed["count"]),
				Name:     rawString(typed["name"]),
			})
		}
		for _, nested := range typed {
			switch nested.(type) {
			case []interface{}, map[string]interface{}:
				collectPromotionCountItems(nested, out)
			}
		}
	}
}

func (c *Client) fetchAdvertsV2(ctx context.Context, token, path string) ([]wbAdvertV2, error) {
	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var response wbAdvertsV2Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal adverts v2 campaigns: %v", err))
	}
	return response.Adverts, nil
}

func (c *Client) enrichAdvertsWithoutNMIDs(ctx context.Context, token string, adverts []wbAdvertV2) []wbAdvertV2 {
	missingIDs := make([]int64, 0)
	for _, advert := range adverts {
		advertID := firstNonZeroInt64(advert.ID, advert.AdvertID)
		if advertID == 0 || len(collectAdvertNMIDs(advert)) > 0 {
			continue
		}
		missingIDs = append(missingIDs, advertID)
	}
	if len(missingIDs) == 0 {
		return adverts
	}

	detailsByID := make(map[int64]wbAdvertV2, len(missingIDs))
	for start := 0; start < len(missingIDs); start += advertsV2DetailBatchSize {
		end := start + advertsV2DetailBatchSize
		if end > len(missingIDs) {
			end = len(missingIDs)
		}
		path := fmt.Sprintf("/api/advert/v2/adverts?ids=%s&statuses=%s", joinInt64s(missingIDs[start:end]), advertsV2DetailStatuses)
		details, err := c.fetchAdvertsV2(ctx, token, path)
		if err != nil {
			c.logger.Warn().Err(err).Ints64("advert_ids", missingIDs[start:end]).Msg("failed to enrich WB adverts with nm_settings")
			continue
		}
		for _, detail := range details {
			advertID := firstNonZeroInt64(detail.ID, detail.AdvertID)
			if advertID == 0 {
				continue
			}
			detailsByID[advertID] = detail
		}
	}

	for i := range adverts {
		advertID := firstNonZeroInt64(adverts[i].ID, adverts[i].AdvertID)
		detail, ok := detailsByID[advertID]
		if !ok || len(collectAdvertNMIDs(detail)) == 0 {
			continue
		}
		adverts[i] = mergeAdvertV2(adverts[i], detail)
	}
	return adverts
}

func mergeAdvertV2(base, detail wbAdvertV2) wbAdvertV2 {
	if base.ID == 0 {
		base.ID = detail.ID
	}
	if base.AdvertID == 0 {
		base.AdvertID = detail.AdvertID
	}
	if base.Name == "" {
		base.Name = detail.Name
	}
	if base.Settings.Name == "" {
		base.Settings.Name = detail.Settings.Name
	}
	if base.BidType == "" {
		base.BidType = detail.BidType
	}
	if base.PaymentType == "" {
		base.PaymentType = detail.PaymentType
	}
	if base.Settings.PaymentType == "" {
		base.Settings.PaymentType = detail.Settings.PaymentType
	}
	if len(base.AutoParams.NMs) == 0 {
		base.AutoParams.NMs = detail.AutoParams.NMs
	}
	if len(base.NMSettings) == 0 {
		base.NMSettings = detail.NMSettings
	}
	return base
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
		nmID := firstNonZeroInt64(item.NMID, item.NMIDCamel)
		if nmID == 0 {
			continue
		}
		if _, ok := seen[nmID]; ok {
			continue
		}
		seen[nmID] = struct{}{}
		result = append(result, nmID)
	}
	return result
}

func collectAdvertProductSettings(advert wbAdvertV2) []WBCampaignProductDTO {
	if len(advert.NMSettings) == 0 {
		return nil
	}
	result := make([]WBCampaignProductDTO, 0, len(advert.NMSettings))
	seen := make(map[int64]struct{}, len(advert.NMSettings))
	for _, item := range advert.NMSettings {
		nmID := firstNonZeroInt64(item.NMID, item.NMIDCamel)
		if nmID == 0 {
			continue
		}
		if _, ok := seen[nmID]; ok {
			continue
		}
		seen[nmID] = struct{}{}

		subjectName := strings.TrimSpace(item.SubjectName)
		if subjectName == "" {
			subjectName = strings.TrimSpace(item.Subject.Name)
		}
		var subjectNamePtr *string
		if subjectName != "" {
			subjectNamePtr = &subjectName
		}
		searchBid, recommendationBid := parseAdvertBidsKopecks(item.BidsKopecks)
		result = append(result, WBCampaignProductDTO{
			NmID:               nmID,
			SubjectName:        subjectNamePtr,
			BidSearch:          searchBid,
			BidRecommendations: recommendationBid,
		})
	}
	return result
}

func joinInt64s(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return strings.Join(parts, ",")
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

func parseAdvertBidsKopecks(raw json.RawMessage) (*int64, *int64) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj) > 0 {
		search := firstOptionalInt64(
			obj["search"],
			obj["bid_search"],
			obj["search_bid"],
			obj["searchBid"],
		)
		recommendations := firstOptionalInt64(
			obj["recommendations"],
			obj["recommendation"],
			obj["bid_recommendations"],
			obj["recommendations_bid"],
			obj["recommendationsBid"],
		)
		return search, recommendations
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		value := rawInt64(number)
		if value != 0 {
			return &value, nil
		}
	}
	return nil, nil
}

func firstOptionalInt64(values ...interface{}) *int64 {
	for _, value := range values {
		parsed := rawInt64(value)
		if parsed != 0 {
			return &parsed
		}
	}
	return nil
}

func parseOptionalWBTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			if parsed.Year() >= 2100 {
				return nil
			}
			return &parsed
		}
	}
	return nil
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
