package service

import (
	"fmt"
	"math"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// Deterministic guardrails shared by the Ozon strategy runner and the AI
// manager. The rule of the module: the LLM proposes, this code decides.
// Every function here is pure — persistence-side inputs (cooldown counters,
// current bids, minimum bids) are loaded by the callers.
//
// ozonStrategyGuardReason (ozon_strategy.go) is part of the same family and
// is reused by the AI manager for cooldown/daily-cap checks.

const (
	// ozonMinWeeklyBudgetRub is the fallback floor for AI budget proposals
	// when Ozon's real minimum is not fetchable (≥1000₽/week per the design).
	ozonMinWeeklyBudgetRub = 1000
	// ozonMinDailyBudgetRub is the daily-budget equivalent of the weekly floor.
	ozonMinDailyBudgetRub = 150
)

// ozonAIBidGuardReason validates one proposed CPC bid (rubles) against the
// hard clamps: positivity, Ozon's per-SKU minimum (when known), the strategy
// MinBid/MaxBid corridor, and MaxChangePercent relative to the current bid.
// An empty return means the proposal passes.
func ozonAIBidGuardReason(currentBid, proposedBid, ozonMinBid float64, params domain.StrategyParams) string {
	if proposedBid <= 0 {
		return "proposed bid must be positive"
	}
	if ozonMinBid > 0 && proposedBid < ozonMinBid {
		return fmt.Sprintf("proposed bid %.2f₽ is below Ozon minimum %.2f₽ for this SKU", proposedBid, ozonMinBid)
	}
	if params.MinBid > 0 && proposedBid < float64(params.MinBid) {
		return fmt.Sprintf("proposed bid %.2f₽ is below strategy min_bid %d₽", proposedBid, params.MinBid)
	}
	if params.MaxBid > 0 && proposedBid > float64(params.MaxBid) {
		return fmt.Sprintf("proposed bid %.2f₽ exceeds strategy max_bid %d₽", proposedBid, params.MaxBid)
	}
	if reason := ozonAIChangePercentReason(currentBid, proposedBid, params.MaxChangePercent, "bid"); reason != "" {
		return reason
	}
	return ""
}

// ozonAIBudgetGuardReason validates one proposed campaign budget (whole
// rubles). weekly selects which floor applies; the change cap compares
// against the current budget when one is set.
func ozonAIBudgetGuardReason(currentBudget *int64, proposedBudget int64, weekly bool, params domain.StrategyParams) string {
	if proposedBudget <= 0 {
		return "proposed budget must be positive"
	}
	floor := int64(ozonMinDailyBudgetRub)
	kind := "daily"
	if weekly {
		floor = ozonMinWeeklyBudgetRub
		kind = "weekly"
	}
	if proposedBudget < floor {
		return fmt.Sprintf("proposed %s budget %d₽ is below the %d₽ minimum", kind, proposedBudget, floor)
	}
	if currentBudget != nil && *currentBudget > 0 {
		if reason := ozonAIChangePercentReason(float64(*currentBudget), float64(proposedBudget), params.MaxChangePercent, "budget"); reason != "" {
			return reason
		}
	}
	return ""
}

// ozonAIUnbudgetedBudgetGuardReason covers the case the percent clamp cannot:
// a campaign with NO configured budget has no anchor, so without this check
// the model could set any number at all. The proposal is bounded by the
// campaign's own recent spend instead: at most 2× the observed run-rate over
// the stats window. No spend history → nothing to anchor on → a human must
// set the first budget.
func ozonAIUnbudgetedBudgetGuardReason(proposedBudget int64, spend14d float64, weekly bool, windowDays int) string {
	if windowDays <= 0 {
		windowDays = aiPackStatsWindowDays
	}
	perDay := spend14d / float64(windowDays)
	cap := perDay * 2
	kind := "дневной"
	if weekly {
		cap *= 7
		kind = "недельный"
	}
	if spend14d <= 0 || cap < 1 {
		return "у кампании нет ни бюджета, ни расхода за окно — безопасный бюджет не рассчитать, первый бюджет задаёт человек"
	}
	if float64(proposedBudget) > cap {
		return fmt.Sprintf("у кампании нет текущего бюджета; предложенный %s бюджет %d₽ превышает 2× фактического расхода (%0.f₽)", kind, proposedBudget, cap)
	}
	return ""
}

// ozonAICPOBidGuardReason validates a proposed CPO (search promo) fixed bid.
// CPO charges only on orders, so only the hard clamps apply: positivity and
// Ozon's CPO minimum when it was fetchable.
func ozonAICPOBidGuardReason(proposedBid, cpoMinBid float64) string {
	if proposedBid <= 0 {
		return "proposed cpo bid must be positive"
	}
	if cpoMinBid > 0 && proposedBid < cpoMinBid {
		return fmt.Sprintf("proposed cpo bid %.2f₽ is below Ozon CPO minimum %.2f₽", proposedBid, cpoMinBid)
	}
	return ""
}

// ozonAIChangePercentReason enforces MaxChangePercent between a current and a
// proposed value. A zero current value cannot anchor a percentage — the
// change is then only bounded by the absolute clamps.
func ozonAIChangePercentReason(current, proposed, maxChangePercent float64, what string) string {
	if maxChangePercent <= 0 || current <= 0 {
		return ""
	}
	changePct := math.Abs(proposed-current) / current * 100
	if changePct > maxChangePercent+1e-9 {
		return fmt.Sprintf("proposed %s change %.1f%% exceeds max_change_percent %.1f%% (current %.2f → proposed %.2f)",
			what, changePct, maxChangePercent, current, proposed)
	}
	return ""
}

// ozonAICampaignStateGuardReason gates pause/activate proposals: pausing only
// makes sense for a running campaign, activating only for a stopped/inactive
// one. Unknown states block the write — the next sync will refresh them.
func ozonAICampaignStateGuardReason(actionType, state string) string {
	switch actionType {
	case domain.AIActionCampaignPause:
		if state != ozonCampaignStateRunning {
			return fmt.Sprintf("campaign is not running (state %s); pause is a no-op", state)
		}
	case domain.AIActionCampaignActivate:
		switch state {
		case "CAMPAIGN_STATE_INACTIVE", "CAMPAIGN_STATE_STOPPED", "CAMPAIGN_STATE_MODERATION_PASSED":
		default:
			return fmt.Sprintf("campaign state %s does not allow activation", state)
		}
	}
	return ""
}

// ozonAIClampBidToChangeLimit pulls a proposed bid back inside
// max_change_percent instead of rejecting it.
//
// The model reasons in whole rubles, so it lands just past the cap constantly:
// 38 → 32 is −15.8 % against a 15 % limit, and 57 → 48 is −15.8 % again. Every
// such proposal used to be thrown away over 0.8 pp of rounding. On the «Тень»
// level that was invisible; on «Автопилот» it would have killed most actions.
//
// The deterministic engine already treats this cap as a bound to clamp to
// (applyBidDecisionLimits → limitChange), not as a validity check. This makes
// the AI path behave the same way.
//
// Returns the bid to use and a note when it differs from what was proposed;
// an empty note means the proposal was already inside the limit.
func ozonAIClampBidToChangeLimit(currentBid, proposedBid, maxChangePercent float64) (float64, string) {
	if maxChangePercent <= 0 || currentBid <= 0 || proposedBid <= 0 {
		return proposedBid, ""
	}
	maxDelta := currentBid * maxChangePercent / 100
	delta := proposedBid - currentBid

	var clamped float64
	switch {
	case delta > maxDelta:
		clamped = currentBid + maxDelta
	case delta < -maxDelta:
		clamped = currentBid - maxDelta
	default:
		return proposedBid, ""
	}
	// Round toward the CURRENT bid, never away from it: rounding outward would
	// step back over the very limit being enforced.
	if delta > 0 {
		clamped = math.Floor(clamped)
	} else {
		clamped = math.Ceil(clamped)
	}
	if clamped <= 0 {
		return proposedBid, ""
	}
	if clamped == currentBid {
		// Ставка настолько мала, что процентный предел меньше рубля: 15 % от
		// 6 ₽ — это 90 копеек. Округление съедало весь шаг, предложение
		// возвращалось неизменным и отклонялось — такие SKU не могли
		// сдвинуться НИКОГДА. Разрешаем ровно рубль: это минимальная
		// возможная величина шага, и в абсолюте она ничтожна.
		if delta > 0 {
			clamped = currentBid + 1
		} else {
			clamped = currentBid - 1
		}
		if clamped <= 0 {
			return proposedBid, ""
		}
	}
	return clamped, fmt.Sprintf(
		"предложено %.2f₽ (%.1f%%), ограничено шагом %.1f%% до %.2f₽",
		proposedBid, (proposedBid-currentBid)/currentBid*100, maxChangePercent, clamped,
	)
}

// ozonAIClampBudgetToChangeLimit is the budget counterpart of the bid clamp:
// «12 000 → 17 000» is a 41.7 % step against a 15 % cap and used to be thrown
// away whole. Budgets are whole rubles and large, so plain clamping is enough —
// the sub-ruble case that bids run into cannot happen here.
func ozonAIClampBudgetToChangeLimit(currentBudget, proposedBudget int64, maxChangePercent float64) (int64, string) {
	if maxChangePercent <= 0 || currentBudget <= 0 || proposedBudget <= 0 {
		return proposedBudget, ""
	}
	maxDelta := float64(currentBudget) * maxChangePercent / 100
	delta := float64(proposedBudget - currentBudget)

	var clamped int64
	switch {
	case delta > maxDelta:
		clamped = currentBudget + int64(math.Floor(maxDelta))
	case delta < -maxDelta:
		clamped = currentBudget - int64(math.Floor(maxDelta))
	default:
		return proposedBudget, ""
	}
	if clamped <= 0 || clamped == currentBudget {
		return proposedBudget, ""
	}
	return clamped, fmt.Sprintf(
		"предложено %d₽ (%.1f%%), ограничено шагом %.1f%% до %d₽",
		proposedBudget, delta/float64(currentBudget)*100, maxChangePercent, clamped,
	)
}
