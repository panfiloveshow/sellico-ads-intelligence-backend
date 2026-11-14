package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

func wbExactBidRub(kopecks int64) string {
	value := fmt.Sprintf("%d.%02d", kopecks/100, kopecks%100)
	return strings.TrimSuffix(strings.TrimRight(value, "0"), ".")
}

var wbExactBidPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,16})(?:\.([0-9]{1,2}))?$`)

func wbExactBidKopecks(value string) (int64, error) {
	match := wbExactBidPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, errors.New("WB bid requires an exact positive RUB decimal with at most two fractional digits")
	}
	amount, ok := new(big.Int).SetString(match[1]+match[2]+strings.Repeat("0", 2-len(match[2])), 10)
	if !ok || !amount.IsInt64() || amount.Sign() <= 0 || int64(int(amount.Int64())) != amount.Int64() {
		return 0, errors.New("WB bid amount outside exact native range")
	}
	return amount.Int64(), nil
}
func wbExactProductBidBody(id, product int64, placement, value string) ([]byte, error) {
	if id <= 0 || product <= 0 || (placement != "search" && placement != "recommendations") {
		return nil, errors.New("specific WB product and manual placement required")
	}
	amount, err := wbExactBidKopecks(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(UpdateCampaignBidsRequest{Bids: []CampaignBidGroup{{AdvertID: id, NMBids: []NMBidItem{{NMID: product, BidKopecks: int(amount), Placement: placement}}}}})
}

// AdvertisingProductBidRequest and SetExactProductBid share the exact body.
// An absent product never falls back to a campaign-wide bid update.
func (c *Client) AdvertisingProductBidRequest(token string, id, product int64, placement, value string) (adguard.Request, error) {
	if c == nil {
		return adguard.Request{}, errors.New("WB transport unavailable")
	}
	body, err := wbExactProductBidBody(id, product, placement, value)
	if err != nil {
		return adguard.Request{}, err
	}
	return adguard.NewWBRequest("PATCH", c.baseURL+"/api/advert/v1/bids", token, body)
}
func (c *Client) SetExactProductBid(ctx context.Context, token string, id, product int64, placement, value string) error {
	body, err := wbExactProductBidBody(id, product, placement, value)
	if err != nil {
		return adguard.BeforeSendError(err)
	}
	_, _, err = c.doRequest(withoutRateLimitRetry(ctx), "PATCH", "/api/advert/v1/bids", token, bytes.NewReader(body))
	return err
}

// ReadManagerProductBid reads one exact manual product/placement and its native
// minimum. It does not use the lossy or campaign-wide mirror bid as a baseline.
func (c *Client) ReadManagerProductBid(ctx context.Context, token string, id, product int64, placement string) (*domain.NativeAdBidContext, error) {
	if id <= 0 || product <= 0 || (placement != "search" && placement != "recommendations") {
		return nil, errors.New("specific WB manual bid target required")
	}
	path := fmt.Sprintf("/api/advert/v2/adverts?ids=%d&statuses=%s", id, advertsV2DetailStatuses)
	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Adverts []struct {
			ID          int64  `json:"id"`
			AdvertID    int64  `json:"advertId"`
			Name        string `json:"name"`
			Status      *int   `json:"status"`
			Type        *int   `json:"type"`
			BidType     string `json:"bid_type"`
			PaymentType string `json:"paymentType"`
			DailyBudget *int64 `json:"dailyBudget"`
			Settings    struct {
				Name        string           `json:"name"`
				PaymentType string           `json:"payment_type"`
				Placements  map[string]*bool `json:"placements"`
			} `json:"settings"`
			Products []struct {
				ID   int64             `json:"nm_id"`
				Bids map[string]*int64 `json:"bids_kopecks"`
			} `json:"nm_settings"`
		} `json:"adverts"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, errors.New("unreadable WB bid context")
	}
	var result *domain.NativeAdBidContext
	for _, row := range response.Adverts {
		if row.ID != 0 && row.AdvertID != 0 && row.ID != row.AdvertID {
			return nil, errors.New("conflicting WB campaign identity")
		}
		actual := firstNonZeroInt64(row.ID, row.AdvertID)
		if actual != id {
			continue
		}
		if result != nil || row.Status == nil || (*row.Status != 9 && *row.Status != 11) || row.BidType != "manual" {
			return nil, errors.New("exact active/paused manual WB campaign required")
		}
		enabled := row.Settings.Placements[placement]
		if enabled == nil || !*enabled {
			return nil, errors.New("WB placement not explicitly enabled")
		}
		payment := row.PaymentType
		if payment == "" {
			payment = row.Settings.PaymentType
		}
		if payment != "cpm" && payment != "cpc" {
			return nil, errors.New("WB payment model unavailable")
		}
		name := row.Name
		if name == "" {
			name = row.Settings.Name
		}
		var selected *int64
		peer := map[string]string{}
		found := false
		for _, item := range row.Products {
			if item.ID != product {
				continue
			}
			if found {
				return nil, errors.New("duplicate WB product bid")
			}
			found = true
			selected = item.Bids[placement]
			for _, key := range []string{"search", "recommendations"} {
				if value := item.Bids[key]; value != nil {
					if *value < 0 {
						return nil, errors.New("invalid WB peer bid")
					}
					peer[key] = wbExactBidRub(*value)
				}
			}
		}
		if !found || selected == nil || *selected <= 0 {
			return nil, errors.New("current WB product bid not explicitly confirmed")
		}
		campaign := domain.NativeAdCampaign{ExternalID: strconv.FormatInt(id, 10), Name: name, Status: strconv.Itoa(*row.Status), BidType: &row.BidType, PaymentType: &payment, Warnings: []string{}}
		if row.Type != nil {
			v := strconv.Itoa(*row.Type)
			campaign.CampaignType = &v
		}
		if row.DailyBudget != nil && *row.DailyBudget >= 0 {
			v := strconv.FormatInt(*row.DailyBudget, 10)
			campaign.DailyBudgetRub = &v
		}
		result = &domain.NativeAdBidContext{Campaign: campaign, Bid: domain.NativeAdProductBid{ProductID: strconv.FormatInt(product, 10), Placement: placement, CurrentBidRub: wbExactBidRub(*selected), Strategy: row.BidType, PaymentType: payment, PeerBidsRub: peer, Warnings: []string{"Верхняя граница ставки и полные расходы не подтверждены. Нужны явные личные ограничения; снимок не разрешает отправку."}}}
	}
	if result == nil {
		return nil, errors.New("WB campaign missing from native response")
	}
	minPlacement := placement
	if minPlacement == "recommendations" {
		minPlacement = "recommendation"
	}
	payload, _ := json.Marshal(MinimumBidsRequest{AdvertID: id, NMIDs: []int64{product}, PaymentType: result.Bid.PaymentType, PlacementTypes: []string{minPlacement}})
	_, body, err = c.doRequest(ctx, "POST", "/api/advert/v1/bids/min", token, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	var minima struct {
		Bids []struct {
			ID   int64 `json:"nm_id"`
			Bids []struct {
				Type  string `json:"type"`
				Value *int64 `json:"value"`
			} `json:"bids"`
		} `json:"bids"`
	}
	if json.Unmarshal(body, &minima) != nil {
		return nil, errors.New("unreadable WB minimum bid")
	}
	for _, row := range minima.Bids {
		if row.ID != product {
			continue
		}
		for _, bid := range row.Bids {
			if bid.Type != minPlacement {
				continue
			}
			if result.Bid.MinimumBidRub != nil || bid.Value == nil || *bid.Value <= 0 {
				return nil, errors.New("ambiguous WB minimum bid")
			}
			v := wbExactBidRub(*bid.Value)
			result.Bid.MinimumBidRub = &v
		}
	}
	if result.Bid.MinimumBidRub == nil {
		return nil, errors.New("exact WB minimum bid missing")
	}
	result.Bid.MinimumSource = "native_kopecks"
	return result, nil
}
