package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// campaignStatsRequest is the request body for the campaign statistics endpoint.
type campaignStatsRequest struct {
	AdvertIDs []int  `json:"advertIds"`
	DateFrom  string `json:"dateFrom"`
	DateTo    string `json:"dateTo"`
}

// GetCampaignStats fetches daily campaign statistics from the WB Advertising API.
// WB API endpoint: POST /adv/v2/statistics
func (c *Client) GetCampaignStats(ctx context.Context, token string, campaignIDs []int, dateFrom, dateTo string) ([]WBCampaignStatDTO, error) {
	reqBody := campaignStatsRequest{
		AdvertIDs: campaignIDs,
		DateFrom:  dateFrom,
		DateTo:    dateTo,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal campaign stats request: %v", err))
	}

	_, body, err := c.doRequest(ctx, "POST", "/adv/v2/statistics", token, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	var result []WBCampaignStatDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal campaign stats: %v", err))
	}

	return result, nil
}
