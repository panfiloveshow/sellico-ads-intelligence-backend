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
