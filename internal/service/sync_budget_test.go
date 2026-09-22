package service

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
)

// Кабинет с >50 кампаниями: каждый 15-минутный слот начинает с другой пачки,
// иначе при Базовом токене (1 запрос в 15 мин) обновлялась бы только первая.
func TestRotateCampaignBudgetIDs(t *testing.T) {
	ids := make([]int64, 0, 2*wb.CampaignBudgetsBatch+10)
	for i := range 2*wb.CampaignBudgetsBatch + 10 {
		ids = append(ids, int64(i))
	}
	slot := time.Unix(0, 0).Add(wbBudgetReadBaseInterval)

	rotated := rotateCampaignBudgetIDs(ids, slot)
	assert.Len(t, rotated, len(ids))
	assert.Equal(t, int64(wb.CampaignBudgetsBatch), rotated[0])
	assert.Equal(t, int64(wb.CampaignBudgetsBatch-1), rotated[len(rotated)-1])
	assert.Equal(t, ids, rotateCampaignBudgetIDs(ids, time.Unix(0, 0)))
	assert.Equal(t, []int64{1, 2}, rotateCampaignBudgetIDs([]int64{1, 2}, slot))
}

// 429 без Retry-After даёт 60 с по умолчанию — для остатков бюджетов пауза не
// короче 15 минут (лимит Базового токена), для остальных методов как раньше.
func TestWBRateLimitWindowFromError_BudgetReadFloor(t *testing.T) {
	err := &wb.APIError{StatusCode: 429, RetryAfter: time.Minute}

	_, budgetSeconds := wbRateLimitWindowFromError(wbEndpointBudgetRead, err)
	assert.Equal(t, int(wbBudgetReadBaseInterval.Seconds()), budgetSeconds)

	_, otherSeconds := wbRateLimitWindowFromError(wbEndpointAdverts, err)
	assert.Equal(t, 60, otherSeconds)

	_, longSeconds := wbRateLimitWindowFromError(wbEndpointBudgetRead, &wb.APIError{StatusCode: 429, RetryAfter: time.Hour})
	assert.Equal(t, 3600, longSeconds)
}

// campaignBudgetAccessFailure distinguishes account-level access/limit problems
// (which stop the whole budget phase) from transient per-campaign failures like
// timeouts and one-off 4xx (which are skipped silently as best-effort noise).
func TestCampaignBudgetAccessFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "rate limited stops the phase", err: errors.New("rate limited (429) on /api/advert/v2/budget"), want: true},
		{name: "forbidden means cabinet cannot collect budgets", err: errors.New("client error (403) on /api/advert/v2/budget"), want: true},
		{name: "timeout is transient, not an access failure", err: errors.New("context deadline exceeded"), want: false},
		{name: "campaign-specific bad request is transient", err: errors.New("client error (400) on /api/advert/v2/budget"), want: false},
		{name: "campaign-specific missing budget is transient", err: errors.New("client error (404) on /api/advert/v2/budget"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := campaignBudgetAccessFailure(tt.err); got != tt.want {
				t.Fatalf("campaignBudgetAccessFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}
