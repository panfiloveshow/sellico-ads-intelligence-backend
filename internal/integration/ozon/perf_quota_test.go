package ozon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// Бюджет: 100 в час и 500 в сутки на аккаунт — место освобождается, когда
// самая старая запись выходит из окна.
func TestPerfQuotaTracker_HourlyAndDailyWindows(t *testing.T) {
	var tr perfQuotaTracker
	start := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 100; i++ {
		require.NoError(t, tr.reserve("perf-1", QuotaBudgetWrite, start.Add(time.Duration(i)*time.Second)))
	}
	err := tr.reserve("perf-1", QuotaBudgetWrite, start.Add(10*time.Minute))
	var quotaErr *QuotaError
	require.ErrorAs(t, err, &quotaErr)
	assert.Equal(t, start.Add(time.Hour), quotaErr.ResetAt, "первая запись часа выходит из окна")
	assert.Contains(t, err.Error(), "исчерпан лимит Ozon Performance API на изменение бюджета")
	assert.Contains(t, err.Error(), "14:00 МСК")

	// Другой аккаунт и другая категория считаются отдельно.
	require.NoError(t, tr.reserve("perf-2", QuotaBudgetWrite, start.Add(10*time.Minute)))
	require.NoError(t, tr.reserve("perf-1", QuotaBidWrite, start.Add(10*time.Minute)))

	// Ещё 4 часа по 100 → 500 за сутки: следующий запрос ждёт выхода первой записи из суток.
	for h := 1; h <= 4; h++ {
		for i := 0; i < 100; i++ {
			require.NoError(t, tr.reserve("perf-1", QuotaBudgetWrite, start.Add(time.Duration(h)*time.Hour+time.Duration(i)*time.Second)))
		}
	}
	err = tr.reserve("perf-1", QuotaBudgetWrite, start.Add(6*time.Hour))
	require.ErrorAs(t, err, &quotaErr)
	assert.Equal(t, start.Add(24*time.Hour), quotaErr.ResetAt)
	require.NoError(t, tr.reserve("perf-1", QuotaBudgetWrite, start.Add(24*time.Hour+time.Second)))
}

// 429 на записи ставок: без повторов, категория закрыта до следующего часа,
// следующий вызов не идёт в Ozon, HTTP-слою уходит 429 с тем же текстом.
func TestSetCampaignProductBids_429ClosesBidQuota(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()
	c := newTestPerfClient(srv.URL)

	err := c.SetCampaignProductBids(context.Background(), testCreds, 7, []ProductBid{{SKU: 1, BidRub: 10}})
	var quotaErr *QuotaError
	require.ErrorAs(t, err, &quotaErr)
	assert.Equal(t, QuotaBidWrite, quotaErr.Category)
	assert.WithinDuration(t, time.Now().Add(30*time.Minute), quotaErr.ResetAt, 30*time.Minute)
	assert.Zero(t, quotaErr.ResetAt.Sub(quotaErr.ResetAt.Truncate(time.Hour)), "без Retry-After — до начала следующего часа")
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusTooManyRequests, appErr.Status)

	err = c.SetCampaignProductBids(context.Background(), testCreds, 8, []ProductBid{{SKU: 2, BidRub: 10}})
	require.ErrorAs(t, err, &quotaErr)
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, *reqs, 1, "429 не повторяется, а закрытая категория не ходит в Ozon")

	// Бюджеты — другая категория, не закрыты.
	assert.NoError(t, c.quota.reserve(testCreds.PerfClientID, QuotaBudgetWrite, time.Now()))
}
