package wb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// ListProducts fetches product cards from the WB API.
// WB API endpoint: GET /adv/v2/products
func (c *Client) ListProducts(ctx context.Context, token string) ([]WBProductDTO, error) {
	_, body, err := c.doRequest(ctx, "GET", "/adv/v2/products", token, nil)
	if err != nil {
		return nil, err
	}

	var result []WBProductDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal products: %v", err))
	}

	return result, nil
}
