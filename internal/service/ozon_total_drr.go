package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// «ДРР от общего оборота» — the second ДРР.
//
// The existing campaign ДРР (ozon_strategy.go) divides ad spend by the revenue
// Ozon attributes to the campaign. This one divides ad spend by the seller's
// WHOLE turnover (ozon_sales_daily), so it answers a different question: не
// «окупается ли кампания», а «сколько всего оборота съедает реклама». The two
// diverge exactly when advertising buys traffic that would have converted
// organically anyway.
//
// It is measured at two scopes:
//
//   - cabinet — every campaign's spend over the seller's entire turnover. This
//     one cannot double-count anything, so the hard ceiling
//     (MaxTotalDRRPercent) uses it.
//   - campaign — the campaign's own spend over its attributed share of that
//     turnover, where a SKU advertised by several campaigns is split between
//     them in proportion to spend (see OzonCampaignAttributedTurnoverByCabinet).
//     The second bid target (TargetTotalDRRPercent) uses this one.
//
// Both are recorded on every decision regardless of whether a ceiling or
// target is configured, so the numbers can be reviewed before anyone switches
// the guardrails on.

const (
	// totalDRRStatusOK means the number is usable.
	totalDRRStatusOK = "ok"
	// totalDRRStatusNoData means there is no turnover to divide by. NOT the
	// same as ДРР 0 — a zero denominator makes the ratio undefined, and
	// reporting it as 0 would read as "всё отлично, можно разгоняться".
	totalDRRStatusNoData = "no_data"
	// totalDRRStatusStale means turnover exists but the newest day is older
	// than the strategy's freshness limit — ozon:sync_analytics is behind.
	totalDRRStatusStale = "stale"

	// Scope is recorded alongside every value so cabinet-wide and
	// per-campaign measurements can be told apart in stored decision contexts.
	totalDRRScopeCabinet  = "cabinet"
	totalDRRScopeCampaign = "campaign"

	// defaultTotalDRRMaxAgeHours is the freshness limit when the strategy
	// carries none. Mirrors StrategyParams.MaxDataAgeHours' own default.
	defaultTotalDRRMaxAgeHours = 36
)

// totalDRR is one measurement of the total-turnover ДРР.
type totalDRR struct {
	Value      float64 // percent; meaningful only when Status == ok
	Status     string  // ok | no_data | stale
	SpendRub   float64
	RevenueRub float64
	Scope      string
	// AgeHours is how long ago the newest turnover day ended. Zero when the
	// data covers today.
	AgeHours float64
}

// computeTotalDRR turns raw window sums into a measurement with an explicit
// freshness verdict. lastData is the newest DAY that has turnover rows (zero
// time = none at all).
//
// Freshness is measured from the END of that day: sales analytics is daily, so
// yesterday's numbers seen at noon today are 12 hours old, not 36.
func computeTotalDRR(spend, revenue float64, lastData, now time.Time, maxAgeHours int) totalDRR {
	result := totalDRR{
		SpendRub:   roundRub(spend),
		RevenueRub: roundRub(revenue),
		Scope:      totalDRRScopeCabinet,
		Status:     totalDRRStatusNoData,
	}
	if revenue <= 0 || lastData.IsZero() {
		return result
	}

	dataEnd := lastData.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	if age := now.UTC().Sub(dataEnd); age > 0 {
		result.AgeHours = age.Hours()
	}

	limit := maxAgeHours
	if limit <= 0 {
		limit = defaultTotalDRRMaxAgeHours
	}
	if result.AgeHours > float64(limit) {
		result.Status = totalDRRStatusStale
		return result
	}

	result.Status = totalDRRStatusOK
	result.Value = roundRub(spend / revenue * 100)
	return result
}

// loadCabinetTotalDRR measures a cabinet's total-turnover ДРР over [since,
// now]. Shared by the deterministic strategy and the AI autopilot so both
// branches judge the same number.
//
// Read failures are never fatal: they degrade to "no_data", which the ceiling
// treats as "do not block". Losing the guardrail for one run is strictly
// better than stopping the sweep.
func loadCabinetTotalDRR(
	ctx context.Context,
	queries *sqlcgen.Queries,
	logger zerolog.Logger,
	cabinetID uuid.UUID,
	since, now time.Time,
	maxAgeHours int,
) totalDRR {
	sinceDate := pgtype.Date{Time: since, Valid: true}
	cabinet := uuidToPgtype(cabinetID)

	sales, err := queries.AggregateOzonCabinetTotalSalesSince(ctx, sqlcgen.AggregateOzonCabinetTotalSalesSinceParams{
		SellerCabinetID: cabinet,
		Since:           sinceDate,
	})
	if err != nil {
		logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("total drr: turnover read failed")
		return computeTotalDRR(0, 0, time.Time{}, now, maxAgeHours)
	}
	spend, err := queries.AggregateOzonCabinetAdSpendSince(ctx, sqlcgen.AggregateOzonCabinetAdSpendSinceParams{
		SellerCabinetID: cabinet,
		Since:           sinceDate,
	})
	if err != nil {
		logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("total drr: ad spend read failed")
		return computeTotalDRR(0, 0, time.Time{}, now, maxAgeHours)
	}

	var lastData time.Time
	if sales.LastDate.Valid {
		lastData = sales.LastDate.Time
	}
	return computeTotalDRR(
		pgNumericToFloat(spend),
		pgNumericToFloat(sales.RevenueRub),
		lastData, now, maxAgeHours,
	)
}

// campaignTotalDRR is the per-campaign variant of the measurement: the
// campaign's own ad spend over the share of the cabinet's turnover attributed
// to it. Shared is true when at least one of the campaign's SKUs is also
// advertised elsewhere, so the denominator had to be split.
type campaignTotalDRR struct {
	totalDRR
	Shared bool
}

// loadCampaignTotalDRR measures the attributed total ДРР for every campaign of
// a cabinet in one round trip; callers look up by campaign id.
//
// The revenue side is attributed (see the SQL for the split rule); the spend
// side is the campaign's own, which the caller supplies per campaign since it
// already has it.
func loadCampaignAttributedTurnover(
	ctx context.Context,
	queries *sqlcgen.Queries,
	logger zerolog.Logger,
	cabinetID uuid.UUID,
	since time.Time,
) map[uuid.UUID]sqlcgen.OzonCampaignAttributedTurnoverByCabinetRow {
	rows, err := queries.OzonCampaignAttributedTurnoverByCabinet(ctx, sqlcgen.OzonCampaignAttributedTurnoverByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Since:           pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).
			Msg("total drr: attributed turnover read failed")
		return nil
	}
	out := make(map[uuid.UUID]sqlcgen.OzonCampaignAttributedTurnoverByCabinetRow, len(rows))
	for _, row := range rows {
		out[uuidFromPgtype(row.CampaignID)] = row
	}
	return out
}

// campaignTotalDRRFrom turns one attributed-turnover row plus the campaign's
// own spend into a measurement. A missing row means the campaign has no SKUs
// with turnover in the window — no_data, not zero.
func campaignTotalDRRFrom(
	row sqlcgen.OzonCampaignAttributedTurnoverByCabinetRow,
	found bool,
	campaignSpend float64,
	now time.Time,
	maxAgeHours int,
) campaignTotalDRR {
	if !found {
		return campaignTotalDRR{totalDRR: computeTotalDRR(campaignSpend, 0, time.Time{}, now, maxAgeHours)}
	}
	var lastData time.Time
	if row.LastDate.Valid {
		lastData = row.LastDate.Time
	}
	measured := computeTotalDRR(campaignSpend, pgNumericToFloat(row.RevenueRub), lastData, now, maxAgeHours)
	measured.Scope = totalDRRScopeCampaign
	return campaignTotalDRR{totalDRR: measured, Shared: row.RevenueShared}
}

// --- Инкрементальный ДРР ---
//
// The ratio of the CHANGE in ad spend to the CHANGE in total turnover between
// two adjacent windows:
//
//	incremental ДРР = Δspend / Δturnover × 100
//
// It answers the question neither level ДРР can: did the extra advertising
// rouble add turnover, or did it buy orders the shop was already getting?
//
// Deliberately observational — recorded and shown, never wired to a bid.
// On small numbers it is pure noise (a tiny Δturnover sends it to infinity),
// so it is only reported when both windows carry real evidence.
const (
	// incrementalDRRMinOrders is the per-window order floor. Below it the
	// windows are not comparable and no value is reported.
	incrementalDRRMinOrders int64 = 5
	// incrementalDRRMinSpendDelta is the smallest spend change worth judging,
	// in rubles: below it the denominator noise dominates.
	incrementalDRRMinSpendDelta = 100.0
)

// incrementalDRRVerdict classifies what the two windows say.
const (
	// incrementalDRRNotEnoughData — the windows fail the evidence floors.
	incrementalDRRNotEnoughData = "not_enough_data"
	// incrementalDRRAccretive — more spend brought more turnover.
	incrementalDRRAccretive = "accretive"
	// incrementalDRRCannibalizing — spend went up, turnover did not follow.
	// This is the case the whole feature exists to surface.
	incrementalDRRCannibalizing = "cannibalizing"
	// incrementalDRRFreed — spend went down without losing turnover.
	incrementalDRRFreed = "freed"
	// incrementalDRRCostly — spend went down and turnover fell with it.
	incrementalDRRCostly = "costly_cut"
)

// incrementalDRR is one two-window comparison.
type incrementalDRR struct {
	// Value is Δspend / Δturnover × 100, meaningful only when Verdict is
	// accretive or costly_cut (spend and turnover moved the same way).
	Value            float64 `json:"value,omitempty"`
	Verdict          string  `json:"verdict"`
	SpendDeltaRub    float64 `json:"spend_delta_rub"`
	TurnoverDeltaRub float64 `json:"turnover_delta_rub"`
}

// computeIncrementalDRR compares a previous and a current window. Orders are
// the evidence floor; the ratio is only computed when both deltas point the
// same way, because a negative ratio is not a ДРР — it is a direction.
func computeIncrementalDRR(prevSpend, prevTurnover float64, prevOrders int64, curSpend, curTurnover float64, curOrders int64) incrementalDRR {
	result := incrementalDRR{
		Verdict:          incrementalDRRNotEnoughData,
		SpendDeltaRub:    roundRub(curSpend - prevSpend),
		TurnoverDeltaRub: roundRub(curTurnover - prevTurnover),
	}
	if prevOrders < incrementalDRRMinOrders || curOrders < incrementalDRRMinOrders {
		return result
	}
	if prevTurnover <= 0 || curTurnover <= 0 {
		return result
	}
	spendDelta := result.SpendDeltaRub
	if spendDelta > -incrementalDRRMinSpendDelta && spendDelta < incrementalDRRMinSpendDelta {
		return result
	}

	turnoverDelta := result.TurnoverDeltaRub
	switch {
	case spendDelta > 0 && turnoverDelta > 0:
		result.Verdict = incrementalDRRAccretive
		result.Value = roundRub(spendDelta / turnoverDelta * 100)
	case spendDelta > 0:
		// Paid more, moved no more goods.
		result.Verdict = incrementalDRRCannibalizing
	case turnoverDelta >= 0:
		// Paid less, kept the turnover.
		result.Verdict = incrementalDRRFreed
	default:
		result.Verdict = incrementalDRRCostly
		result.Value = roundRub(spendDelta / turnoverDelta * 100)
	}
	return result
}

// loadIncrementalDRR compares the lookback window against the one immediately
// before it, cabinet-wide. Read failures degrade to "not_enough_data" — this
// number is observational and must never break a run.
func loadIncrementalDRR(
	ctx context.Context,
	queries *sqlcgen.Queries,
	logger zerolog.Logger,
	cabinetID uuid.UUID,
	now time.Time,
	lookbackDays int,
) incrementalDRR {
	if lookbackDays <= 0 {
		return incrementalDRR{Verdict: incrementalDRRNotEnoughData}
	}
	cabinet := uuidToPgtype(cabinetID)
	window := func(from, to time.Time) (spend, turnover float64, orders int64, ok bool) {
		dateFrom := pgtype.Date{Time: from, Valid: true}
		dateTo := pgtype.Date{Time: to, Valid: true}
		sales, err := queries.GetOzonCabinetSalesWindowTotals(ctx, sqlcgen.GetOzonCabinetSalesWindowTotalsParams{
			SellerCabinetID: cabinet, DateFrom: dateFrom, DateTo: dateTo,
		})
		if err != nil {
			logger.Warn().Err(err).Msg("incremental drr: turnover window read failed")
			return 0, 0, 0, false
		}
		spendRow, err := queries.GetOzonCabinetAdSpendWindowTotals(ctx, sqlcgen.GetOzonCabinetAdSpendWindowTotalsParams{
			SellerCabinetID: cabinet, DateFrom: dateFrom, DateTo: dateTo,
		})
		if err != nil {
			logger.Warn().Err(err).Msg("incremental drr: spend window read failed")
			return 0, 0, 0, false
		}
		return pgNumericToFloat(spendRow.SpendRub), pgNumericToFloat(sales.RevenueRub), sales.OrderedUnits, true
	}

	// Two adjacent closed windows: [now-2L, now-L-1] and [now-L, now].
	curFrom := now.AddDate(0, 0, -lookbackDays)
	prevFrom := now.AddDate(0, 0, -2*lookbackDays)
	prevTo := curFrom.AddDate(0, 0, -1)

	prevSpend, prevTurnover, prevOrders, ok := window(prevFrom, prevTo)
	if !ok {
		return incrementalDRR{Verdict: incrementalDRRNotEnoughData}
	}
	curSpend, curTurnover, curOrders, ok := window(curFrom, now)
	if !ok {
		return incrementalDRR{Verdict: incrementalDRRNotEnoughData}
	}
	return computeIncrementalDRR(prevSpend, prevTurnover, prevOrders, curSpend, curTurnover, curOrders)
}

// --- Потолок из юнит-экономики ---
//
// Nobody should have to invent a percentage. The largest ДРР a cabinet can
// carry follows from its own margin:
//
//	profit  = delivered × margin% − spend,   delivered ≈ ordered × buyout
//	break-even on the target profit p%  ⇒  spend/ordered ≤ buyout × (margin% − p%)
//
// The buyout factor matters because /v1/analytics/data reports ORDERED
// turnover: cancellations and returns are not deducted, so a ceiling derived
// straight from ordered revenue is systematically too generous.
const (
	// derivedCeilingMinCoverage is the share of turnover that must have a
	// known cost before the derived ceiling is trusted at all.
	derivedCeilingMinCoverage = 0.5
	// defaultExpectedBuyoutPercent is the assumed share of ordered turnover
	// that is actually delivered and kept, when the strategy sets none.
	// Deliberately conservative — an over-estimate here loosens a safety
	// ceiling.
	defaultExpectedBuyoutPercent = 90.0
)

// cabinetMargin is the turnover-weighted margin of a cabinet plus how much of
// that turnover the number actually covers.
type cabinetMargin struct {
	WeightedMarginPct float64
	Coverage          float64 // 0..1 share of turnover with a known cost
}

// loadCabinetMargin computes the turnover-weighted average margin. SKUs whose
// cost is unknown are excluded from the average and counted against coverage,
// so a cabinet with mostly unpriced goods cannot produce a confident ceiling.
// loadBuyoutPercent returns the measured buyout for a cabinet, falling back to
// the configured (or default) assumption when the postings sample is too thin
// or has not been collected yet.
func loadBuyoutPercent(
	ctx context.Context,
	queries *sqlcgen.Queries,
	cabinetID uuid.UUID,
	configured float64,
) (float64, string) {
	total, cancelled, err := queries.GetOzonCancellationCounts(ctx, uuidToPgtype(cabinetID))
	if err == nil {
		if measured, ok := ozonMeasuredBuyoutPercent(int(total), int(cancelled)); ok {
			return measured, "measured"
		}
	}
	if configured > 0 && configured <= 100 {
		return configured, "configured"
	}
	return defaultExpectedBuyoutPercent, "assumed"
}

func loadCabinetMargin(
	ctx context.Context,
	queries *sqlcgen.Queries,
	logger zerolog.Logger,
	cabinetID uuid.UUID,
	since time.Time,
) cabinetMargin {
	rows, err := queries.OzonCabinetMarginInputs(ctx, sqlcgen.OzonCabinetMarginInputsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Since:           pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("derived ceiling: margin inputs read failed")
		return cabinetMargin{}
	}

	var knownTurnover, allTurnover, weighted float64
	for _, row := range rows {
		turnover := pgNumericToFloat(row.RevenueRub)
		if turnover <= 0 {
			continue
		}
		allTurnover += turnover

		commission := pgNumericToFloat(row.CommissionFboPct)
		if fbs := pgNumericToFloat(row.CommissionFbsPct); fbs > commission {
			commission = fbs
		}
		// acquiring_pct holds a ruble amount despite the column name — same
		// convention the AI economics section already uses.
		margin := ozonSKUMarginPct(
			pgNumericToFloat(row.PriceRub),
			pgNumericToFloat(row.NetPriceRub),
			commission,
			pgNumericToFloat(row.AcquiringPct),
		)
		if margin == nil {
			continue
		}
		knownTurnover += turnover
		weighted += *margin * turnover
	}
	if allTurnover <= 0 || knownTurnover <= 0 {
		return cabinetMargin{}
	}
	return cabinetMargin{
		WeightedMarginPct: roundRub(weighted / knownTurnover),
		Coverage:          knownTurnover / allTurnover,
	}
}

// derivedTotalDRRCeiling turns a measured margin into the ДРР ceiling that
// still leaves targetProfitPct of the delivered turnover as profit. Returns
// nil whenever the inputs cannot support a confident answer — a guardrail
// derived from guesswork is worse than none.
func derivedTotalDRRCeiling(margin cabinetMargin, targetProfitPct, buyoutPercent float64) *float64 {
	if margin.WeightedMarginPct <= 0 || margin.Coverage < derivedCeilingMinCoverage {
		return nil
	}
	if buyoutPercent <= 0 || buyoutPercent > 100 {
		buyoutPercent = defaultExpectedBuyoutPercent
	}
	headroom := margin.WeightedMarginPct - targetProfitPct
	if headroom <= 0 {
		return nil
	}
	ceiling := roundRub(headroom * buyoutPercent / 100)
	if ceiling <= 0 {
		return nil
	}
	return &ceiling
}

// resolveTotalDRRCeiling picks the ceiling actually enforced: an explicit
// max_total_drr_percent always wins, otherwise the one derived from the
// cabinet's own economics. Returns the value and where it came from.
func resolveTotalDRRCeiling(explicit *float64, margin cabinetMargin, targetProfitPct, buyoutPercent float64) (*float64, string) {
	if explicit != nil && *explicit > 0 {
		return explicit, "explicit"
	}
	if derived := derivedTotalDRRCeiling(margin, targetProfitPct, buyoutPercent); derived != nil {
		return derived, "unit_economics"
	}
	return nil, "none"
}

// totalDRRIncreaseBlockReason is the stage-1 guardrail: while the cabinet's
// total ДРР sits above the configured ceiling, nothing may spend MORE. It
// returns a human-readable reason, or "" when the increase is allowed.
//
// Call it only on an increase path — decreases must always get through, that
// is the whole point of a ceiling.
//
// Both branches of the system share this one function on purpose. The
// deterministic strategy and the AI autopilot decide bids in completely
// separate code, and a ceiling installed in only one of them would let the
// other keep scaling the same cabinet.
//
// A stale or missing measurement does NOT block. ozon:sync_analytics runs on
// its own low-frequency schedule, and one failed sync must not silently freeze
// every increase across every cabinet; the caller records the reason instead.
func totalDRRIncreaseBlockReason(maxTotalDRR *float64, total totalDRR) string {
	if maxTotalDRR == nil || *maxTotalDRR <= 0 {
		return ""
	}
	if total.Status != totalDRRStatusOK {
		return ""
	}
	if total.Value < *maxTotalDRR {
		return ""
	}
	return fmt.Sprintf(
		"ДРР от общего оборота %.2f%% достиг потолка %.2f%% — повышение заблокировано",
		total.Value, *maxTotalDRR,
	)
}

// --- Измеренная доля отмен ---

// ozonPostingCancelled reports whether a posting status means the order was
// cancelled. Ozon's statuses for FBS/rFBS include awaiting_registration,
// awaiting_deliver, delivering, delivered, cancelled and not_accepted; only the
// last two mean the goods never reached the customer.
func ozonPostingCancelled(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "cancelled", "canceled", "not_accepted":
		return true
	}
	return false
}

// ozonMeasuredBuyoutPercent turns counted postings into a buyout percentage.
//
// It is an UPPER bound: postings statuses capture cancellations, not returns
// after delivery — those need /v1/returns/list. Still strictly better than the
// blind 90 % the ceiling used to assume.
//
// Below minPostingsForBuyout the sample is too thin to beat the default, so the
// caller keeps its configured value.
const minPostingsForBuyout = 50

func ozonMeasuredBuyoutPercent(total, cancelled int) (float64, bool) {
	if total < minPostingsForBuyout || cancelled < 0 || cancelled > total {
		return 0, false
	}
	return roundRub(float64(total-cancelled) / float64(total) * 100), true
}
