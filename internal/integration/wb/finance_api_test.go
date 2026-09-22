package wb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// POST /api/advert/v2/budget: advertIds пачками по 50, ответ adverts[]{advertId,currency,total};
// 429 не повторяется (у Базового токена 1 запрос в 15 мин), уже полученное возвращается.
func TestGetCampaignBudgets_BatchesByFiftyAndStopsOn429(t *testing.T) {
	withFastWBRetryTiming(t)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v2/budget", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		var body struct {
			AdvertIDs []int64 `json:"advertIds"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if atomic.AddInt32(&calls, 1) > 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		assert.Len(t, body.AdvertIDs, CampaignBudgetsBatch)
		adverts := make([]map[string]any, 0, len(body.AdvertIDs))
		for _, id := range body.AdvertIDs {
			adverts = append(adverts, map[string]any{"advertId": id, "currency": "RUB", "total": id * 10})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"adverts": adverts})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	client.budgetLimiters.Set("token", rate.NewLimiter(rate.Inf, 1))
	ids := make([]int64, 0, CampaignBudgetsBatch+1)
	for i := 1; i <= CampaignBudgetsBatch+1; i++ {
		ids = append(ids, int64(i))
	}

	budgets, err := client.GetCampaignBudgets(context.Background(), "token", ids)

	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.EqualValues(t, 2, atomic.LoadInt32(&calls), "429 must not be retried")
	require.Len(t, budgets, CampaignBudgetsBatch)
	assert.Equal(t, WBCampaignBudgetDTO{AdvertID: 7, Currency: "RUB", Total: 70}, budgets[6])
}
