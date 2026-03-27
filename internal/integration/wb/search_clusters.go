package wb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// ListSearchClusters fetches Search Clusters (bid list) for a campaign.
// WB API endpoint: GET /adv/v2/search-clusters/{campaignID}/bids
func (c *Client) ListSearchClusters(ctx context.Context, token string, campaignID int) ([]WBSearchClusterDTO, error) {
	path := fmt.Sprintf("/adv/v2/search-clusters/%d/bids", campaignID)

	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBSearchClusterDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal search clusters: %v", err))
	}

	return result, nil
}

// GetSearchClusterStats fetches statistics for Search Clusters of a campaign.
// WB API endpoint: GET /adv/v2/search-clusters/{campaignID}/statistics
func (c *Client) GetSearchClusterStats(ctx context.Context, token string, campaignID int) ([]WBSearchClusterStatDTO, error) {
	path := fmt.Sprintf("/adv/v2/search-clusters/%d/statistics", campaignID)

	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBSearchClusterStatDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal search cluster stats: %v", err))
	}

	return result, nil
}
