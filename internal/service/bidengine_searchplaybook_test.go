package service

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// playbook builds a merged search_playbook strategy for a tier.
func playbook(tier string, maxACoS float64) domain.Strategy {
	return domain.Strategy{
		Type: domain.StrategyTypeSearchPlaybook,
		Params: domain.StrategyParams{
			FrequencyTier:             tier,
			MaxACoS:                   maxACoS,
			MaxChangePercent:          15,
			MinBid:                    50,
			MaxBid:                    5000,
			AllowIncreaseWithoutStock: true, // isolate the engine from stock/economics guardrails
		}.Merged(),
	}
}

func TestSearchPlaybook_ClimbsWhenBelowTargetPosition(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	// mid tier → target position 3; we sit at 6.2 → should raise.
	d := engine.CalculateBid(playbook("mid", 0), BidContext{
		CurrentBid:        100,
		Impressions:       500,
		Orders:            2,
		Revenue:           4000,
		Spend:             200,
		AvgPosition:       6.2,
		HasPosition:       true,
		Placement:         "search",
		IncreaseGuardrail: &BidIncreaseGuardrail{Allowed: true, MaxAllowedDRRPercent: 100},
	})
	require.NotNil(t, d)
	require.Greater(t, d.NewBid, d.OldBid)
}

func TestSearchPlaybook_SacrificialCutOnZeroOrdersOverBuyerPrice(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	// low tier, 0 orders, spend (1200) ≥ 100% of buyer price (1000) → cut.
	d := engine.CalculateBid(playbook("low", 0), BidContext{
		CurrentBid:  300,
		Impressions: 800,
		Orders:      0,
		Spend:       1200,
		BuyerPrice:  1000,
		AvgPosition: 1.0,
		HasPosition: true,
		Placement:   "search",
	})
	require.NotNil(t, d)
	require.Less(t, d.NewBid, d.OldBid)
}

func TestSearchPlaybook_DRRCeilingReducesBid(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	// At target but DRR = 500/1000*100 = 50% > ceiling 20% → reduce.
	d := engine.CalculateBid(playbook("mid", 20), BidContext{
		CurrentBid:  200,
		Impressions: 400,
		Orders:      3,
		Revenue:     1000,
		Spend:       500,
		AvgPosition: 2.5,
		HasPosition: true,
		Placement:   "search",
	})
	require.NotNil(t, d)
	require.Less(t, d.NewBid, d.OldBid)
}

func TestSearchPlaybook_PullsBackWhenAtTargetAndImpressionsFlat(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	// At target (pos 1), impressions flat vs prior window (500 vs 490 ≈ +2% ≤ 20%) → pull back.
	d := engine.CalculateBid(playbook("low", 0), BidContext{
		CurrentBid:      300,
		Impressions:     500,
		PrevImpressions: 490,
		Orders:          4,
		Revenue:         6000,
		Spend:           300,
		AvgPosition:     1.0,
		HasPosition:     true,
		Placement:       "search",
	})
	require.NotNil(t, d)
	require.Less(t, d.NewBid, d.OldBid)
}

func TestSearchPlaybook_HoldsWithoutPositionEvidence(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	d := engine.CalculateBid(playbook("mid", 0), BidContext{
		CurrentBid:  100,
		Impressions: 500,
		HasPosition: false, // no real position → do nothing
		Placement:   "search",
	})
	require.Nil(t, d)
}
