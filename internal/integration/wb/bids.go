package wb

import (
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

type minimumBidsResponse struct {
	Bids []struct {
		AdvertID int64 `json:"advertId"`
		NmID     int64 `json:"nmId"`
		MinBid   int64 `json:"minBid"`
		Bid      int64 `json:"bid"`
	} `json:"bids"`
	AdvertID int64 `json:"advertId"`
	NmID     int64 `json:"nmId"`
	MinBid   int64 `json:"minBid"`
	Bid      int64 `json:"bid"`
}

type WBMinimumBidDTO struct {
	NmID   int64 `json:"nmId"`
	MinBid int64 `json:"minBid"`
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
// WB API endpoint: GET /api/advert/v1/bids/min
func (c *Client) GetMinimumBids(ctx context.Context, token string, campaignID int, articles []int) ([]WBMinimumBidDTO, error) {
	values := url.Values{}
	values.Set("advertId", strconv.Itoa(campaignID))
	for _, article := range articles {
		values.Add("nmIds", strconv.Itoa(article))
	}

	_, body, err := c.doRequest(ctx, "GET", "/api/advert/v1/bids/min?"+values.Encode(), token, nil)
	if err != nil {
		return nil, err
	}

	var response minimumBidsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal minimum bids: %v", err))
	}

	result := make([]WBMinimumBidDTO, 0, len(response.Bids)+1)
	for _, bid := range response.Bids {
		minBid := bid.MinBid
		if minBid == 0 {
			minBid = bid.Bid
		}
		result = append(result, WBMinimumBidDTO{NmID: bid.NmID, MinBid: minBid})
	}
	if len(result) == 0 && response.NmID != 0 {
		minBid := response.MinBid
		if minBid == 0 {
			minBid = response.Bid
		}
		result = append(result, WBMinimumBidDTO{NmID: response.NmID, MinBid: minBid})
	}
	return result, nil
}
