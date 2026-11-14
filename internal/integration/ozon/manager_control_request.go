package ozon

import (
	"errors"
	"fmt"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

func (c *PerfClient) AdvertisingResumeRequest(creds Credentials, id int64) (adguard.Request, error) {
	if c == nil || id <= 0 {
		return adguard.Request{}, errors.New("specific Ozon resume target required")
	}
	return adguard.NewOzonRequest("POST", c.baseURL+fmt.Sprintf("/api/client/campaign/%d/activate", id), creds.PerfClientID, []byte(`{}`))
}
