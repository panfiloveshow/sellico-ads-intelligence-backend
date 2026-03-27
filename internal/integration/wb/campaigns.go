package wb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// ListCampaigns fetches all campaigns from the WB Advertising API.
// WB API endpoint: GET /adv/v2/adverts
func (c *Client) ListCampaigns(ctx context.Context, token string) ([]WBCampaignDTO, error) {
	_, body, err := c.doRequest(ctx, "GET", "/adv/v2/adverts", token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBCampaignDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal campaigns: %v", err))
	}

	return result, nil
}
