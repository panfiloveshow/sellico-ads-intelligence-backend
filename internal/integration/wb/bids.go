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
