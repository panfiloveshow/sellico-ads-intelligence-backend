package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// BidExecutor sends bid changes to Wildberries API.
type BidExecutor interface {
	UpdateCampaignBid(ctx context.Context, token string, wbCampaignID int64, placement string, newBid int) error
}

// BidEngine calculates and applies bid changes based on strategies.
type BidEngine struct {
	logger zerolog.Logger
}

// NewBidEngine creates a new BidEngine.
func NewBidEngine(logger zerolog.Logger) *BidEngine {
	return &BidEngine{
		logger: logger.With().Str("component", "bid_engine").Logger(),
	}
}

// BidDecision represents the engine's decision for a single entity.
type BidDecision struct {
	CampaignID uuid.UUID
	ProductID  *uuid.UUID
	PhraseID   *uuid.UUID
	Placement  string
	OldBid     int
	NewBid     int
	Reason     string
	ACoS       *float64
	ROAS       *float64
}

// BidContext contains the data needed to calculate a bid.
type BidContext struct {
	CurrentBid        int
	Impressions       int64
	Clicks            int64
	Spend             float64
	Revenue           float64
	Orders            int64
	Placement         string
	IncreaseGuardrail *BidIncreaseGuardrail

	// Search-playbook inputs (populated only for search_playbook strategies).
	AvgPosition     float64 // impression-weighted average position of the campaign's keywords (1 = top)
	HasPosition     bool    // true when real position evidence is available
	PrevImpressions int64   // impressions of the prior half-window, for the flat-impression pullback rule
	BuyerPrice      float64 // buyer-facing product price in rubles, for the sacrificial-spend rule

	// Dayparting inputs are persisted per strategy/binding by BidAutomationService.
	DaypartingBaselineBid int
	DaypartingSlotApplied bool
	DecisionTime          time.Time
}

// BidIncreaseGuardrail blocks scale-up decisions when real readiness data is missing.
type BidIncreaseGuardrail struct {
	Allowed              bool
	Reason               string
	MaxAllowedDRRPercent float64
	MaxAllowedCPO        float64
	BuyerPrice           float64
}

const (
	minOrdersForProfitSafeIncrease int64 = 3
	profitSafeCPOFactor                  = 0.90
)

const (
	bidIncreaseMinRating       = 4.2
	bidIncreaseMinReviewsCount = 10
)

func productReputationBidIncreaseBlockReason(product sqlcgen.Product) string {
	if product.Rating.Valid && product.Rating.Float64 > 0 && product.Rating.Float64 < bidIncreaseMinRating {
		return fmt.Sprintf("real product rating %.1f is below %.1f; increase is blocked", product.Rating.Float64, bidIncreaseMinRating)
	}
	if product.ReviewsCount.Valid && product.ReviewsCount.Int32 >= 0 && product.ReviewsCount.Int32 < bidIncreaseMinReviewsCount {
		return fmt.Sprintf("real product reviews count %d is below %d; increase is blocked", product.ReviewsCount.Int32, bidIncreaseMinReviewsCount)
	}
	return ""
}

// CalculateBid computes a new bid based on strategy type and current performance.
func (e *BidEngine) CalculateBid(strategy domain.Strategy, ctx BidContext) *BidDecision {
	params := strategy.Params.Merged()

	if ctx.CurrentBid == 0 {
		return nil
	}

	var newBid int
	var reason string
	var acos, roas *float64

	switch strategy.Type {
	case domain.StrategyTypeACoS:
		newBid, reason, acos = e.calculateACoSBid(params, ctx)
	case domain.StrategyTypeROAS:
		newBid, reason, roas = e.calculateROASBid(params, ctx)
	case domain.StrategyTypeAntiSliv:
		newBid, reason, acos = e.calculateAntiSlivBid(params, ctx)
	case domain.StrategyTypeDayparting:
		newBid, reason = e.calculateDaypartingBid(params, ctx)
	case domain.StrategyTypeSearchPlaybook:
		newBid, reason = e.calculateSearchPlaybookBid(params, ctx)
	default:
		return nil
	}

	if newBid == 0 || newBid == ctx.CurrentBid {
		return nil
	}

	// Apply limits and keep the reason auditable.
	newBid, reason = applyBidDecisionLimits(ctx.CurrentBid, newBid, reason, params)

	if newBid == ctx.CurrentBid {
		return nil
	}

	if newBid > ctx.CurrentBid {
		if guardrailReason := economicsIncreaseGuardrailReason(params, ctx, newBid); guardrailReason != "" {
			e.logger.Info().Str("placement", ctx.Placement).Str("reason", guardrailReason).
				Msg("bid increase blocked by unit economics DRR ceiling")
			return nil
		}
		if guardrailReason := maxACoSIncreaseGuardrailReason(params, ctx); guardrailReason != "" {
			e.logger.Info().
				Str("placement", ctx.Placement).
				Int("current_bid", ctx.CurrentBid).
				Int("calculated_bid", newBid).
				Str("reason", guardrailReason).
				Msg("bid increase blocked by max_acos guardrail")
			return nil
		}
	}

	if newBid > ctx.CurrentBid {
		if guardrailReason := maxCostIncreaseGuardrailReason(params, ctx); guardrailReason != "" {
			e.logger.Info().
				Str("placement", ctx.Placement).
				Int("current_bid", ctx.CurrentBid).
				Int("calculated_bid", newBid).
				Str("reason", guardrailReason).
				Msg("bid increase blocked by max cost guardrail")
			return nil
		}
	}

	if newBid > ctx.CurrentBid && ctx.IncreaseGuardrail != nil && !ctx.IncreaseGuardrail.Allowed {
		e.logger.Info().
			Str("placement", ctx.Placement).
			Int("current_bid", ctx.CurrentBid).
			Int("calculated_bid", newBid).
			Str("reason", ctx.IncreaseGuardrail.Reason).
			Msg("bid increase blocked by guardrail")
		return nil
	}

	return &BidDecision{
		Placement: ctx.Placement,
		OldBid:    ctx.CurrentBid,
		NewBid:    newBid,
		Reason:    reason,
		ACoS:      acos,
		ROAS:      roas,
	}
}

func economicsIncreaseGuardrailReason(params domain.StrategyParams, ctx BidContext, proposedBid int) string {
	if ctx.IncreaseGuardrail == nil {
		return ""
	}
	if ctx.Revenue <= 0 {
		ceiling := ctx.IncreaseGuardrail.MaxAllowedCPO
		if ceiling <= 0 {
			return "unit economics CPO ceiling is unavailable"
		}
		if ctx.Orders < minOrdersForProfitSafeIncrease {
			return fmt.Sprintf("at least %d attributed orders are required for a profit-safe increase", minOrdersForProfitSafeIncrease)
		}
		if ctx.Clicks < int64(params.MinClicks) {
			return fmt.Sprintf("at least %d clicks are required for a profit-safe increase", params.MinClicks)
		}
		currentCPO := ctx.Spend / float64(ctx.Orders)
		projectedCPO := currentCPO
		if ctx.CurrentBid > 0 && proposedBid > ctx.CurrentBid {
			projectedCPO *= float64(proposedBid) / float64(ctx.CurrentBid)
		}
		safeCeiling := ceiling * profitSafeCPOFactor
		if projectedCPO >= safeCeiling {
			return fmt.Sprintf("projected CPO %.2f is at or above safe ceiling %.2f (90%% of unit economics break-even %.2f)", projectedCPO, safeCeiling, ceiling)
		}
		return ""
	}
	if ctx.IncreaseGuardrail.MaxAllowedDRRPercent <= 0 {
		return "unit economics DRR ceiling is unavailable"
	}
	ceiling := ctx.IncreaseGuardrail.MaxAllowedDRRPercent
	currentDRR := ctx.Spend / ctx.Revenue * 100
	if currentDRR >= ceiling {
		return fmt.Sprintf("current DRR %.1f%% is at or above unit economics ceiling %.1f%%", currentDRR, ceiling)
	}
	if params.TargetACoS > 0 && params.TargetACoS > ceiling {
		return fmt.Sprintf("target ACoS %.1f%% exceeds unit economics ceiling %.1f%%", params.TargetACoS, ceiling)
	}
	if params.TargetROAS > 0 {
		minimumROAS := 100 / ceiling
		if params.TargetROAS < minimumROAS {
			return fmt.Sprintf("target ROAS %.2f is below unit economics break-even %.2f", params.TargetROAS, minimumROAS)
		}
	}
	return ""
}

func (e *BidEngine) calculateACoSBid(params domain.StrategyParams, ctx BidContext) (int, string, *float64) {
	if ctx.Clicks < int64(params.MinClicks) {
		return 0, "", nil
	}
	if ctx.Revenue == 0 {
		return 0, "", nil
	}

	currentACoS := ctx.Spend / ctx.Revenue * 100
	targetACoS := params.TargetACoS
	acos := &currentACoS

	if targetACoS == 0 {
		return 0, "", nil
	}

	if currentACoS > targetACoS {
		ratio := targetACoS / currentACoS
		newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
		reason := fmt.Sprintf("ACoS %.1f%% > target %.1f%%, reducing bid by ratio %.2f", currentACoS, targetACoS, ratio)
		return newBid, reason, acos
	}

	if currentACoS < targetACoS*0.8 {
		ratio := targetACoS / currentACoS
		if ratio > 1.3 {
			ratio = 1.3 // cap increase
		}
		newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
		reason := fmt.Sprintf("ACoS %.1f%% well below target %.1f%%, increasing bid by ratio %.2f", currentACoS, targetACoS, ratio)
		return newBid, reason, acos
	}

	return 0, "", nil
}

func (e *BidEngine) calculateROASBid(params domain.StrategyParams, ctx BidContext) (int, string, *float64) {
	if ctx.Clicks < int64(params.MinClicks) {
		return 0, "", nil
	}
	if ctx.Spend == 0 {
		return 0, "", nil
	}

	currentROAS := ctx.Revenue / ctx.Spend
	targetROAS := params.TargetROAS
	roas := &currentROAS

	if targetROAS == 0 {
		return 0, "", nil
	}

	if currentROAS < targetROAS {
		ratio := currentROAS / targetROAS
		newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
		reason := fmt.Sprintf("ROAS %.2f < target %.2f, reducing bid by ratio %.2f", currentROAS, targetROAS, ratio)
		return newBid, reason, roas
	}

	if currentROAS > targetROAS*1.2 {
		ratio := currentROAS / targetROAS
		if ratio > 1.3 {
			ratio = 1.3
		}
		newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
		reason := fmt.Sprintf("ROAS %.2f exceeds target %.2f by 20%%+, increasing bid by ratio %.2f", currentROAS, targetROAS, ratio)
		return newBid, reason, roas
	}

	return 0, "", nil
}

// defaultTargetPositionForTier maps a keyword frequency tier to its target average
// position when the strategy does not pin one explicitly (per the launch playbook:
// high-freq keys are probed at top-4, mid-freq pushed to top-3, low-freq to top-1).
func defaultTargetPositionForTier(tier string) float64 {
	switch tier {
	case "high":
		return 4
	case "mid":
		return 3
	case "low":
		return 1
	}
	return 0
}

// calculateSearchPlaybookBid drives the campaign bid toward a frequency-tier target
// position, gated by four rules evaluated in priority order:
//  1. Sacrificial spend — 0 orders and spend ≥ N% of buyer price → cut hard.
//  2. DRR ceiling — orders exist but DRR above max_acos → reduce toward the ceiling.
//  3. Position targeting — below target position, climb (kept feeding while orders come);
//     competitive pressure (bot holding the top with no orders near the sacrificial cap)
//     stops the climb instead of overpaying.
//  4. Flat-impression pullback — at target with impressions flat vs the prior window →
//     step the bid back to hold the spot cheaper.
//
// Returns a raw target bid (0 = hold); CalculateBid applies change/min/max limits and the
// stock/economics increase guardrails afterwards.
func (e *BidEngine) calculateSearchPlaybookBid(params domain.StrategyParams, ctx BidContext) (int, string) {
	if !ctx.HasPosition || ctx.Impressions == 0 {
		return 0, "" // no real position evidence yet — do nothing
	}

	target := params.TargetPosition
	if target == 0 {
		target = defaultTargetPositionForTier(params.FrequencyTier)
	}
	if target <= 0 {
		return 0, "" // neither target_position nor a known tier configured
	}

	// Rule 1: sacrificial product. No orders and spend has reached the buyer-price cap.
	sacrificialCap := 0.0
	if params.SacrificialSpendPricePct > 0 && ctx.BuyerPrice > 0 {
		sacrificialCap = ctx.BuyerPrice * params.SacrificialSpendPricePct / 100
	}
	if sacrificialCap > 0 && ctx.Orders == 0 && ctx.Spend >= sacrificialCap {
		newBid := int(math.Round(float64(ctx.CurrentBid) * 0.7))
		return newBid, fmt.Sprintf("sacrificial cap: spend %.0f ≥ %.0f%% of buyer price %.0f with 0 orders, cutting bid",
			ctx.Spend, params.SacrificialSpendPricePct, ctx.BuyerPrice)
	}

	// Rule 2: profitability ceiling (reuses max_acos as the DRR cap). WB does
	// not return normquery revenue, so cluster decisions use the equivalent CPO
	// ceiling derived from the real buyer price. No campaign revenue is borrowed.
	if ceiling := params.MaxACoS; ceiling > 0 && ctx.Orders > 0 {
		if ctx.Revenue > 0 {
			drr := ctx.Spend / ctx.Revenue * 100
			if drr > ceiling {
				ratio := ceiling / drr
				newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
				return newBid, fmt.Sprintf("DRR %.1f%% > ceiling %.1f%%, reducing bid toward target", drr, ceiling)
			}
		} else if ctx.BuyerPrice > 0 {
			currentCPO := ctx.Spend / float64(ctx.Orders)
			maxCPO := ctx.BuyerPrice * ceiling / 100
			if currentCPO > maxCPO {
				ratio := maxCPO / currentCPO
				newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
				return newBid, fmt.Sprintf("CPO %.1f > ceiling %.1f from buyer price and DRR %.1f%%, reducing bid toward target", currentCPO, maxCPO, ceiling)
			}
		}
	}

	// Rule 3: position targeting. avg_pos is 1-based; a larger number is a worse spot.
	if ctx.AvgPosition > target {
		// Competitive pressure: the top is contested and we are burning toward the
		// sacrificial cap with nothing to show — stop climbing instead of overpaying.
		if ctx.Orders == 0 && sacrificialCap > 0 && ctx.Spend >= sacrificialCap*0.7 {
			return 0, ""
		}
		step := params.MaxChangePercent
		if step <= 0 {
			step = 15
		}
		newBid := int(math.Round(float64(ctx.CurrentBid) * (1 + step/100)))
		return newBid, fmt.Sprintf("avg position %.1f worse than target %.1f, raising bid to climb", ctx.AvgPosition, target)
	}

	// Rule 4: at/above target — pull back when impressions are flat vs the prior window.
	if params.FlatImpressionsPct > 0 && params.RollbackStepPercent > 0 && ctx.PrevImpressions > 0 {
		growth := (float64(ctx.Impressions) - float64(ctx.PrevImpressions)) / float64(ctx.PrevImpressions) * 100
		if math.Abs(growth) <= params.FlatImpressionsPct {
			newBid := int(math.Round(float64(ctx.CurrentBid) * (1 - params.RollbackStepPercent/100)))
			return newBid, fmt.Sprintf("at target (pos %.1f), impressions flat within %.0f%% — pulling back %.0f%% to hold cheaper",
				ctx.AvgPosition, params.FlatImpressionsPct, params.RollbackStepPercent)
		}
	}

	return 0, "" // at target, holding
}

func (e *BidEngine) calculateAntiSlivBid(params domain.StrategyParams, ctx BidContext) (int, string, *float64) {
	maxACoS := params.MaxACoS
	if maxACoS == 0 {
		return 0, "", nil
	}

	// No revenue at all — reduce aggressively
	if ctx.Revenue == 0 && ctx.Clicks > 0 {
		reduction := 0.5 // default 50% reduction
		if ctx.Clicks > 50 {
			reduction = 0.15 // 85% reduction for high-click no-revenue
		} else if ctx.Clicks > 20 {
			reduction = 0.3
		}
		newBid := int(math.Round(float64(ctx.CurrentBid) * reduction))
		reason := fmt.Sprintf("Anti-Sliv: %d clicks with zero revenue, reducing bid to %.0f%%", ctx.Clicks, reduction*100)
		return newBid, reason, nil
	}

	if ctx.Revenue == 0 {
		return 0, "", nil
	}

	currentACoS := ctx.Spend / ctx.Revenue * 100
	acos := &currentACoS

	if currentACoS > maxACoS {
		ratio := maxACoS / currentACoS
		if ratio < 0.2 {
			ratio = 0.2 // minimum ratio floor
		}
		newBid := int(math.Round(float64(ctx.CurrentBid) * ratio))
		reason := fmt.Sprintf("Anti-Sliv: ACoS %.1f%% > max %.1f%%, emergency bid reduction ratio %.2f", currentACoS, maxACoS, ratio)
		return newBid, reason, acos
	}

	return 0, "", nil
}

func (e *BidEngine) calculateDaypartingBid(params domain.StrategyParams, ctx BidContext) (int, string) {
	if ctx.DaypartingSlotApplied {
		return 0, ""
	}
	now := ctx.DecisionTime
	if now.IsZero() {
		now = time.Now()
	}
	timezone := strings.TrimSpace(params.Timezone)
	if timezone == "" {
		timezone = "Europe/Moscow"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return 0, ""
	}
	now = now.In(location)
	hour := fmt.Sprintf("%d", now.Hour())
	weekday := fmt.Sprintf("%d", now.Weekday())

	multiplier := params.BaseMultiplier
	if hourMul, ok := params.HourlyMultipliers[hour]; ok {
		multiplier *= hourMul
	}
	if wdMul, ok := params.WeekdayMultipliers[weekday]; ok {
		multiplier *= wdMul
	}

	baseline := ctx.DaypartingBaselineBid
	if baseline <= 0 {
		baseline = ctx.CurrentBid
	}

	newBid := int(math.Round(float64(baseline) * multiplier))
	if newBid == ctx.CurrentBid {
		return 0, ""
	}
	reason := fmt.Sprintf("Dayparting: timezone=%s hour=%s weekday=%s baseline=%d multiplier=%.2f", timezone, hour, weekday, baseline, multiplier)
	return newBid, reason
}

func applyBidDecisionLimits(oldBid, calculatedBid int, reason string, params domain.StrategyParams) (int, string) {
	limitedBid := calculatedBid
	notes := make([]string, 0, 2)

	// MaxChangePercent is a hard unattended-automation cap. Absolute bounds may
	// refine a step only when that bound itself is reachable within the cap. If the
	// current bid is already farther outside the configured range, hold the action
	// for operator review instead of making an oversized corrective jump.
	changeLimitedBid := limitChange(oldBid, limitedBid, params.MaxChangePercent)
	if changeLimitedBid != limitedBid {
		limitedBid = changeLimitedBid
		notes = append(notes, fmt.Sprintf("max_change_percent %.1f applied", params.MaxChangePercent))
	}

	if params.MinBid > 0 && limitedBid < params.MinBid {
		if limitChange(oldBid, params.MinBid, params.MaxChangePercent) != params.MinBid {
			return oldBid, fmt.Sprintf("%s; action held: min_bid %d requires a step beyond max_change_percent %.1f", reason, params.MinBid, params.MaxChangePercent)
		}
		limitedBid = params.MinBid
		notes = append(notes, fmt.Sprintf("min_bid %d applied", params.MinBid))
	}
	if params.MaxBid > 0 && limitedBid > params.MaxBid {
		if limitChange(oldBid, params.MaxBid, params.MaxChangePercent) != params.MaxBid {
			return oldBid, fmt.Sprintf("%s; action held: max_bid %d requires a step beyond max_change_percent %.1f", reason, params.MaxBid, params.MaxChangePercent)
		}
		limitedBid = params.MaxBid
		notes = append(notes, fmt.Sprintf("max_bid %d applied", params.MaxBid))
	}
	if len(notes) == 0 {
		return limitedBid, reason
	}
	if reason == "" {
		return limitedBid, fmt.Sprintf("Bid limits: %s", strings.Join(notes, "; "))
	}
	return limitedBid, fmt.Sprintf("%s; limits: %s", reason, strings.Join(notes, "; "))
}

func limitChange(oldBid, newBid int, maxChangePercent float64) int {
	if maxChangePercent <= 0 || oldBid == 0 {
		return newBid
	}

	maxDelta := int(math.Ceil(float64(oldBid) * maxChangePercent / 100))
	delta := newBid - oldBid

	if delta > maxDelta {
		return oldBid + maxDelta
	}
	if delta < -maxDelta {
		return oldBid - maxDelta
	}
	return newBid
}

func maxACoSIncreaseGuardrailReason(params domain.StrategyParams, ctx BidContext) string {
	if params.MaxACoS <= 0 || ctx.Revenue <= 0 {
		return ""
	}

	currentACoS := ctx.Spend / ctx.Revenue * 100
	if currentACoS < params.MaxACoS {
		return ""
	}

	return fmt.Sprintf("current ACoS %.1f%% is at or above max_acos %.1f%%", currentACoS, params.MaxACoS)
}

func maxCostIncreaseGuardrailReason(params domain.StrategyParams, ctx BidContext) string {
	if params.MaxCPC > 0 {
		if ctx.Clicks <= 0 {
			return "current CPC evidence is unavailable while max_cpc is configured"
		}
		cpc := ctx.Spend / float64(ctx.Clicks)
		if cpc >= params.MaxCPC {
			return fmt.Sprintf("current CPC %.2f is at or above max_cpc %.2f", cpc, params.MaxCPC)
		}
	}
	if params.MaxCPO > 0 {
		if ctx.Orders <= 0 {
			return "current CPO evidence is unavailable while max_cpo is configured"
		}
		cpo := ctx.Spend / float64(ctx.Orders)
		if cpo >= params.MaxCPO {
			return fmt.Sprintf("current CPO %.2f is at or above max_cpo %.2f", cpo, params.MaxCPO)
		}
	}
	return ""
}
