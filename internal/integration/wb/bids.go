package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

type recommendedBidsResponse struct {
	AdvertID int64 `json:"advertId"`
	NmID     int64 `json:"nmId"`
	Base     struct {
		CompetitiveBid struct {
			BidKopecks int64 `json:"bidKopecks"`
		} `json:"competitiveBid"`
		LeadersBid struct {
			BidKopecks int64 `json:"bidKopecks"`
		} `json:"leadersBid"`
	} `json:"base"`
}

type MinimumBidsRequest struct {
	AdvertID       int64    `json:"advert_id"`
	NMIDs          []int64  `json:"nm_ids"`
	PaymentType    string   `json:"payment_type"`
	PlacementTypes []string `json:"placement_types"`
}

type minimumBidsResponse struct {
	Bids []struct {
		NMID int64 `json:"nm_id"`
		Bids []struct {
			Type  string `json:"type"`
			Value int64  `json:"value"`
		} `json:"bids"`
	} `json:"bids"`
}

type WBMinimumBidDTO struct {
	NmID      int64  `json:"nmId"`
	Placement string `json:"placement"`
	MinBid    int64  `json:"minBid"`
}

// GetRecommendedBids fetches recommended bids (competitive_bid, leadership_bid) from WB API.
// WB API endpoint: GET /api/advert/v0/bids/recommendations
func (c *Client) GetRecommendedBids(ctx context.Context, token string, campaignID int, articles []int) ([]WBBidDTO, error) {
	result := make([]WBBidDTO, 0, len(articles))
	for _, article := range articles {
		values := url.Values{}
		values.Set("advertId", strconv.Itoa(campaignID))
		values.Set("nmId", strconv.Itoa(article))

		_, body, err := c.doRequest(ctx, "GET", "/api/advert/v0/bids/recommendations?"+values.Encode(), token, nil)
		if err != nil {
			return nil, err
		}

		var response recommendedBidsResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal recommended bids: %v", err))
		}

		result = append(result, WBBidDTO{
			NmID:           response.NmID,
			CompetitiveBid: response.Base.CompetitiveBid.BidKopecks,
			LeadershipBid:  response.Base.LeadersBid.BidKopecks,
		})
	}

	return result, nil
}

// GetMinimumBids fetches minimum allowed product bids.
// WB API endpoint: POST /api/advert/v1/bids/min
func (c *Client) GetMinimumBids(ctx context.Context, token string, request MinimumBidsRequest) ([]WBMinimumBidDTO, error) {
	if request.AdvertID <= 0 || len(request.NMIDs) == 0 || request.PaymentType == "" || len(request.PlacementTypes) == 0 {
		return nil, apperror.New(apperror.ErrValidation, "advert_id, nm_ids, payment_type and placement_types are required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal minimum bids request: %v", err))
	}

	_, responseBody, err := c.doRequest(ctx, "POST", "/api/advert/v1/bids/min", token, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var response minimumBidsResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal minimum bids: %v", err))
	}

	result := make([]WBMinimumBidDTO, 0, len(response.Bids)*len(request.PlacementTypes))
	for _, product := range response.Bids {
		for _, bid := range product.Bids {
			result = append(result, WBMinimumBidDTO{
				NmID:      product.NMID,
				Placement: bid.Type,
				MinBid:    bid.Value,
			})
		}
	}
	return result, nil
}
