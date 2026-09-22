package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// dailyBudget устарел 22.05.2026: дневная сумма уходит недельной ×7; кампания,
// созданная с дневным бюджетом, тип сменить не может — ей запрос как есть.
func TestOzonBudgetPatch(t *testing.T) {
	daily, weekly := int64(1000), int64(9000)
	weeklyCampaign := sqlcgen.OzonCampaign{WeeklyBudgetRub: pgtype.Int8{Int64: 7000, Valid: true}}
	legacyDaily := sqlcgen.OzonCampaign{DailyBudgetRub: pgtype.Int8{Int64: 500, Valid: true}}

	patch := ozonBudgetPatch(weeklyCampaign, &daily, nil)
	assert.Nil(t, patch.DailyBudgetRub)
	require.NotNil(t, patch.WeeklyBudgetRub)
	assert.EqualValues(t, 7000, *patch.WeeklyBudgetRub)

	patch = ozonBudgetPatch(sqlcgen.OzonCampaign{}, &daily, &weekly)
	assert.Nil(t, patch.DailyBudgetRub, "недельная сумма главнее, dailyBudget не шлём")
	assert.Equal(t, &weekly, patch.WeeklyBudgetRub)

	patch = ozonBudgetPatch(legacyDaily, &daily, nil)
	assert.Equal(t, &daily, patch.DailyBudgetRub)
	assert.Nil(t, patch.WeeklyBudgetRub)
}

// Лимит Ozon — отложенное действие (вердикт), не ошибка; и он временный для
// ручного одобрения, как и своя доля автоматики.
func TestOzonQuotaVerdict(t *testing.T) {
	quotaErr := &ozon.QuotaError{Category: ozon.QuotaBidWrite, ResetAt: time.Now().Add(time.Hour)}
	verdict, err := ozonQuotaVerdict(fmt.Errorf("ozon set product bids: %w", quotaErr))
	require.NoError(t, err)
	assert.Contains(t, verdict, "исчерпан лимит Ozon Performance API")
	assert.True(t, isOzonQuotaVerdict(verdict))

	other := errors.New("ozon perf: client error (400)")
	verdict, err = ozonQuotaVerdict(other)
	assert.Empty(t, verdict)
	assert.Equal(t, other, err)

	hourly := ozonAPIWindowBlockReason(400, 1, ozonAPIHourlyLimits[ozonAPICategoryBidWrite], ozonAPICategoryBidWrite, "часовой")
	assert.Contains(t, hourly, "часовой лимит Ozon Performance API по bid_write почти исчерпан: использовано 400 из 500, автоматике отведено 400")
	assert.True(t, isOzonQuotaVerdict(hourly))
	assert.Empty(t, ozonAPIWindowBlockReason(79, 1, ozonAPIHourlyLimits[ozonAPICategoryBudgetWrite], ozonAPICategoryBudgetWrite, "часовой"))
	assert.Contains(t, ozonAPIResetSuffix(time.Date(2026, 9, 22, 11, 0, 0, 0, time.UTC)), "до 22.09 14:00 МСК")
}
