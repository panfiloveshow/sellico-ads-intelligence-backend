package wb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// GetCategoryConfig fetches category configuration (cpm_min) from WB API.
// WB API endpoint: GET /adv/v2/config/categories
func (c *Client) GetCategoryConfig(ctx context.Context, token string) ([]WBCategoryConfigDTO, error) {
	_, body, err := c.doRequest(ctx, "GET", "/adv/v2/config/categories", token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBCategoryConfigDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal category config: %v", err))
	}

	return result, nil
}
