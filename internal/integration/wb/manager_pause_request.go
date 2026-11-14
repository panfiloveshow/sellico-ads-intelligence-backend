package wb

import (
	"errors"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

// AdvertisingPauseRequest binds a proposal to this client's exact native target.
// It only constructs an identity and never sends an HTTP request.
func (c *Client) AdvertisingPauseRequest(token string, id int64) (adguard.Request, error) {
	if c == nil || id <= 0 {
		return adguard.Request{}, errors.New("specific WB pause target required")
	}
	return adguard.NewWBRequest("GET", c.baseURL+fmt.Sprintf("/adv/v0/pause?id=%d", id), token, nil)
}
