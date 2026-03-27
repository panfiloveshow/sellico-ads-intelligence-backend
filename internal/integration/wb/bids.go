package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// recommendedBidsRequest is the request body for the recommended bids endpoint.
type recommendedBidsRequest struct {
	CampaignID int   `json:"campaignId"`
	Articles   []int `json:"articles"`
}

// GetRecommendedBids fetches recommended bids (competitive_bid, leadership_bid) from WB API.
// WB API endpoint: POST /adv/v2/recommended-bids
func (c *Client) GetRecommendedBids(ctx context.Context, token string, campaignID int, articles []int) ([]WBBidDTO, error) {
	reqBody := recommendedBidsRequest{
		CampaignID: campaignID,
		Articles:   articles,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal recommended bids request: %v", err))
	}

	_, body, err := c.doRequest(ctx, "POST", "/adv/v2/recommended-bids", token, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	var result []WBBidDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal recommended bids: %v", err))
	}

	return result, nil
}
