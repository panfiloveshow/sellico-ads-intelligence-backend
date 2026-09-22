package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Ozon meters the Performance API by hour and by day from 2026-09-14 (раздел
// «Лимиты на запросы»): создание и копирование кампаний — 300 в час и 1000 в
// сутки, изменение бюджета и смена стратегии — 100 и 500, добавление товаров —
// 200 и 1200, изменение ставок — 500 и 6000 (до 10 000 товаров за раз),
// выгрузки статистики — 2000 за 24 часа.
//
// The client already paces requests to 2 rps per client_id and stops metered
// writes at Ozon's hard limit (ozon.QuotaError). This budget is the shared,
// cross-process part: every automated metered call is counted per cabinet,
// and AUTOMATED writes stop when a cabinet approaches its hourly or daily
// allowance. Manual actions are never blocked here — a person clicking a
// button has already decided.
const (
	ozonAPICategoryBidWrite      = "bid_write"
	ozonAPICategoryBudgetWrite   = "budget_write"
	ozonAPICategoryCampaignWrite = "campaign_write"
	ozonAPICategoryProductWrite  = "product_write"
	ozonAPICategoryReport        = "report"

	// ozonBidWriteDailyLimit is Ozon's announced 6000/day for bid changes.
	ozonBidWriteDailyLimit int64 = 6000
	// ozonAutomationDailyShare is the fraction of an allowance automation may
	// consume. The rest stays for the people using the cabinet — an autopilot
	// that burns the whole quota by noon leaves managers unable to work.
	ozonAutomationDailyShare = 0.8
	// ozonAPICounterRetention is how long buckets are kept. Two days covers
	// every rolling window with room to inspect yesterday.
	ozonAPICounterRetention = 48 * time.Hour
)

// ozonAPIDailyLimits / ozonAPIHourlyLimits — опубликованные лимиты по
// категориям (campaign_write — создание/копирование, report — выгрузки).
var ozonAPIDailyLimits = map[string]int64{
	ozonAPICategoryBidWrite:      ozonBidWriteDailyLimit,
	ozonAPICategoryBudgetWrite:   500,
	ozonAPICategoryCampaignWrite: 1000,
	ozonAPICategoryProductWrite:  1200,
	ozonAPICategoryReport:        2000,
}

var ozonAPIHourlyLimits = map[string]int64{
	ozonAPICategoryBidWrite:      500,
	ozonAPICategoryBudgetWrite:   100,
	ozonAPICategoryCampaignWrite: 300,
	ozonAPICategoryProductWrite:  200,
}

// OzonAPIBudget counts metered Performance API calls per cabinet and answers
// whether automation still has room.
type OzonAPIBudget struct {
	queries *sqlcgen.Queries
	logger  zerolog.Logger
}

func NewOzonAPIBudget(queries *sqlcgen.Queries, logger zerolog.Logger) *OzonAPIBudget {
	return &OzonAPIBudget{
		queries: queries,
		logger:  logger.With().Str("component", "ozon_api_budget").Logger(),
	}
}

// Record adds n calls to a cabinet's counter. Accounting must never break a
// write that already succeeded, so failures are logged and swallowed.
func (b *OzonAPIBudget) Record(ctx context.Context, cabinetID uuid.UUID, category string, n int) {
	if b == nil || n <= 0 {
		return
	}
	if err := b.queries.IncrementOzonAPICalls(ctx, sqlcgen.IncrementOzonAPICallsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Category:        category,
		Calls:           int32(n),
	}); err != nil {
		b.logger.Warn().Err(err).
			Str("cabinet_id", cabinetID.String()).
			Str("category", category).
			Msg("failed to record ozon api call")
	}
}

// UsedToday returns the cabinet's calls in a category over the last 24 hours.
func (b *OzonAPIBudget) UsedToday(ctx context.Context, cabinetID uuid.UUID, category string) (int64, error) {
	return b.usedSince(ctx, cabinetID, category, time.Now().UTC().Add(-24*time.Hour))
}

func (b *OzonAPIBudget) usedSince(ctx context.Context, cabinetID uuid.UUID, category string, since time.Time) (int64, error) {
	if b == nil {
		return 0, nil
	}
	return b.queries.CountOzonAPICallsSince(ctx, sqlcgen.CountOzonAPICallsSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Category:        category,
		Since:           pgtype.Timestamptz{Time: since, Valid: true},
	})
}

// AutomationBlockReason reports why automation must not spend `planned` more
// calls of a category right now, or "" when there is room. Сначала часовой
// лимит (текущий UTC-час), затем суточный (скользящие 24 часа); в причине —
// время, когда место освободится.
//
// A failed read returns "" — an unavailable counter must not stop the sweep,
// the same way an unavailable ДРР measurement does not.
func (b *OzonAPIBudget) AutomationBlockReason(ctx context.Context, cabinetID uuid.UUID, category string, planned int) string {
	if b == nil || planned <= 0 {
		return ""
	}
	now := time.Now().UTC()
	hourStart := now.Truncate(time.Hour)
	if hourly := ozonAPIHourlyLimits[category]; hourly > 0 {
		used, err := b.usedSince(ctx, cabinetID, category, hourStart)
		if err != nil {
			b.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).
				Str("category", category).Msg("ozon api budget read failed; not blocking")
			return ""
		}
		if reason := ozonAPIWindowBlockReason(used, int64(planned), hourly, category, "часовой"); reason != "" {
			return reason + ozonAPIResetSuffix(hourStart.Add(time.Hour))
		}
	}
	used, err := b.UsedToday(ctx, cabinetID, category)
	if err != nil {
		b.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).
			Str("category", category).Msg("ozon api budget read failed; not blocking")
		return ""
	}
	reason := ozonAPIBudgetBlockReason(used, int64(planned), ozonAPIDailyLimits[category], category)
	if reason == "" {
		return ""
	}
	return reason + ozonAPIResetSuffix(b.dailyResetAt(ctx, cabinetID, category, int64(planned), now))
}

// dailyResetAt находит, когда скользящие 24 часа освободят место: счётчик
// хранится по часам, и час b выпадает из окна в b+24h. Ищется первый час, после
// выхода которого остатка хватает (бинарный поиск, ~5 запросов, только когда
// автоматика уже упёрлась).
func (b *OzonAPIBudget) dailyResetAt(ctx context.Context, cabinetID uuid.UUID, category string, planned int64, now time.Time) time.Time {
	automationLimit := int64(float64(ozonAPIDailyLimits[category]) * ozonAutomationDailyShare)
	first := now.Add(-24 * time.Hour).Truncate(time.Hour)
	hours := int(now.Truncate(time.Hour).Sub(first) / time.Hour)
	k := sort.Search(hours+1, func(k int) bool {
		used, err := b.usedSince(ctx, cabinetID, category, first.Add(time.Duration(k+1)*time.Hour))
		return err != nil || used+planned <= automationLimit
	})
	return first.Add(time.Duration(k)*time.Hour + 24*time.Hour)
}

func ozonAPIResetSuffix(resetAt time.Time) string {
	return " — до " + resetAt.In(mskLocation).Format("02.01 15:04") + " МСК"
}

// ozonAPIBudgetBlockReason is the pure decision: would `planned` more calls
// push automation past its share of the daily allowance?
func ozonAPIBudgetBlockReason(used, planned, dailyLimit int64, category string) string {
	return ozonAPIWindowBlockReason(used, planned, dailyLimit, category, "дневной")
}

func ozonAPIWindowBlockReason(used, planned, limit int64, category, window string) string {
	if limit <= 0 {
		return ""
	}
	automationLimit := int64(float64(limit) * ozonAutomationDailyShare)
	if used+planned <= automationLimit {
		return ""
	}
	return fmt.Sprintf(
		"%s лимит Ozon Performance API по %s почти исчерпан: использовано %d из %d, автоматике отведено %d",
		window, category, used, limit, automationLimit,
	)
}

// ozonQuotaVerdict: исчерпанный лимит Ozon (ozon.QuotaError) — не провал
// действия, а отказ проверки со временем сброса: действие откладывается до
// следующего запуска. Остальные ошибки возвращаются как есть.
func ozonQuotaVerdict(err error) (string, error) {
	var quotaErr *ozon.QuotaError
	if errors.As(err, &quotaErr) {
		return quotaErr.Error(), nil
	}
	return "", err
}

// isOzonQuotaVerdict — причина отказа временная (лимит Ozon или своя доля
// автоматики): решение не отклоняется навсегда.
func isOzonQuotaVerdict(verdict string) bool {
	return strings.Contains(verdict, "лимит Ozon Performance API")
}

// CleanupCounters drops buckets past every rolling window.
func (b *OzonAPIBudget) CleanupCounters(ctx context.Context) error {
	if b == nil {
		return nil
	}
	return b.queries.DeleteOzonAPICallCountersBefore(ctx, pgtype.Timestamptz{
		Time: time.Now().UTC().Add(-ozonAPICounterRetention), Valid: true,
	})
}
