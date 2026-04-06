package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
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
	CurrentBid  int
	Impressions int64
	Clicks      int64
	Spend       float64
	Revenue     float64
	Orders      int64
	Placement   string
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
	default:
		return nil
	}

	if newBid == 0 || newBid == ctx.CurrentBid {
		return nil
	}

	// Apply limits
	newBid = clampBid(newBid, params.MinBid, params.MaxBid)
	newBid = limitChange(ctx.CurrentBid, newBid, params.MaxChangePercent)

	if newBid == ctx.CurrentBid {
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
	now := time.Now()
	hour := fmt.Sprintf("%d", now.Hour())
	weekday := fmt.Sprintf("%d", now.Weekday())

	multiplier := params.BaseMultiplier
	if hourMul, ok := params.HourlyMultipliers[hour]; ok {
		multiplier *= hourMul
	}
	if wdMul, ok := params.WeekdayMultipliers[weekday]; ok {
		multiplier *= wdMul
	}

	if multiplier == 1.0 || multiplier == 0 {
		return 0, ""
	}

	newBid := int(math.Round(float64(ctx.CurrentBid) * multiplier))
	reason := fmt.Sprintf("Dayparting: hour=%s weekday=%s multiplier=%.2f", hour, weekday, multiplier)
	return newBid, reason
}

func clampBid(bid, min, max int) int {
	if bid < min {
		return min
	}
	if bid > max {
		return max
	}
	return bid
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
