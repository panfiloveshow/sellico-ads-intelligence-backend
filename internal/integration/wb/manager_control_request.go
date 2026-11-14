package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

// AdvertisingResumeRequest constructs the same request that StartCampaign sends.
// Initial launches (status 4) are deliberately not authorized by this seam.
func (c *Client) AdvertisingResumeRequest(token string, id int64) (adguard.Request, error) {
	if c == nil || id <= 0 {
		return adguard.Request{}, errors.New("specific WB resume target required")
	}
	return adguard.NewWBRequest(http.MethodGet, c.baseURL+fmt.Sprintf("/adv/v0/start?id=%d", id), token, nil)
}

// ReadCampaignControlContext adds an exact, explicitly present RUB balance to
// the native campaign observation. The legacy finance DTO cannot distinguish
// a missing total from zero and cannot authorize resumed spending.
func (c *Client) ReadCampaignControlContext(ctx context.Context, token string, id int64) (*domain.NativeAdCampaign, error) {
	snapshot, err := c.ReadCampaignContext(ctx, token, id)
	if err != nil {
		return nil, err
	}
	if err = c.budgetLimiterForToken(token).Wait(ctx); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string][]int64{"advertIds": {id}})
	_, body, err := c.doRequest(withoutRateLimitRetry(ctx), http.MethodPost, "/api/advert/v2/budget", token, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	var response struct {
		Adverts []struct {
			ID       int64  `json:"advertId"`
			Currency string `json:"currency"`
			Total    *int64 `json:"total"`
		} `json:"adverts"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Adverts) != 1 {
		return nil, errors.New("exact WB campaign budget unavailable")
	}
	row := response.Adverts[0]
	if row.ID != id || row.Currency != "RUB" || row.Total == nil || *row.Total < 0 {
		return nil, errors.New("explicit RUB campaign budget required")
	}
	total := strconv.FormatInt(*row.Total, 10)
	snapshot.RemainingBudgetRub = &total
	return snapshot, nil
}
