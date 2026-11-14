package ozon

import (
	"errors"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

func (c *PerfClient) AdvertisingPauseRequest(creds Credentials, id int64) (adguard.Request, error) {
	if c == nil || id <= 0 {
		return adguard.Request{}, errors.New("specific Ozon pause target required")
	}
	return adguard.NewOzonRequest("POST", c.baseURL+fmt.Sprintf("/api/client/campaign/%d/deactivate", id), creds.PerfClientID, []byte(`{}`))
}
