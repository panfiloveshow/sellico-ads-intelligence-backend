package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Ozon meters the Performance API by hour and by day from 2026-08-25:
// campaign create/copy, adding products, budget and bid changes, and report
// generation each have their own allowance. The full table has not been
// published yet — the only figure Ozon named is «ставки в кампаниях можно
// будет менять до 6000 раз в день».
//
// The client already paces requests to 2 rps per client_id, which says nothing
// about an hourly or daily allowance. This budget closes that gap: every
// metered call is counted per cabinet, and AUTOMATED writes stop when a cabinet
// approaches its allowance. Manual actions are counted but never blocked — a
// person clicking a button has already decided, and silently refusing them
// would be worse than a 429.
//
// The defaults are deliberately below the announced figure. When Ozon
// publishes the full list these constants (or the per-cabinet overrides) are
// the only thing to change.
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

// ozonAPIDailyLimits maps a metered category to its daily allowance. Only the
// bid-change figure is published; the others are placeholders generous enough
// not to interfere today, present so that filling in the real numbers is a
// one-line change rather than a new feature.
var ozonAPIDailyLimits = map[string]int64{
	ozonAPICategoryBidWrite:      ozonBidWriteDailyLimit,
	ozonAPICategoryBudgetWrite:   6000,
	ozonAPICategoryCampaignWrite: 1000,
	ozonAPICategoryProductWrite:  1000,
	ozonAPICategoryReport:        1000,
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
	if b == nil {
		return 0, nil
	}
	return b.queries.CountOzonAPICallsSince(ctx, sqlcgen.CountOzonAPICallsSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Category:        category,
		Since:           pgtype.Timestamptz{Time: time.Now().UTC().Add(-24 * time.Hour), Valid: true},
	})
}

// AutomationBlockReason reports why automation must not spend `planned` more
// calls of a category right now, or "" when there is room.
//
// A failed read returns "" — an unavailable counter must not stop the sweep,
// the same way an unavailable ДРР measurement does not.
func (b *OzonAPIBudget) AutomationBlockReason(ctx context.Context, cabinetID uuid.UUID, category string, planned int) string {
	if b == nil || planned <= 0 {
		return ""
	}
	used, err := b.UsedToday(ctx, cabinetID, category)
	if err != nil {
		b.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).
			Str("category", category).Msg("ozon api budget read failed; not blocking")
		return ""
	}
	return ozonAPIBudgetBlockReason(used, int64(planned), ozonAPIDailyLimits[category], category)
}

// ozonAPIBudgetBlockReason is the pure decision: would `planned` more calls
// push automation past its share of the daily allowance?
func ozonAPIBudgetBlockReason(used, planned, dailyLimit int64, category string) string {
	if dailyLimit <= 0 {
		return ""
	}
	automationLimit := int64(float64(dailyLimit) * ozonAutomationDailyShare)
	if used+planned <= automationLimit {
		return ""
	}
	return fmt.Sprintf(
		"дневной лимит Ozon Performance API по %s почти исчерпан: использовано %d из %d, автоматике отведено %d",
		category, used, dailyLimit, automationLimit,
	)
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
