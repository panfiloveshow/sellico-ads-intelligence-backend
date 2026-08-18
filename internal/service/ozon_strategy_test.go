package service

import (
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// TestCalculateBidOzonTargetDRRMatchesACoS pins the phase-2 dispatch contract:
// ozon_cpc_target_drr must produce exactly the ACoS decision (ДРР == ACoS),
// including the applyBidDecisionLimits clamping.
func TestCalculateBidOzonTargetDRRMatchesACoS(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	params := domain.StrategyParams{
		TargetACoS:       10, // target ДРР 10%
		MinClicks:        5,
		MinBid:           1,
		MaxBid:           10000,
		MaxChangePercent: 50,
	}
	ctx := BidContext{
		CurrentBid:  100,
		Impressions: 10000,
		Clicks:      200,
		Spend:       2000, // ДРР = 2000/10000*100 = 20% > target 10%
		Revenue:     10000,
		Orders:      20,
		Placement:   "ozon_cpc",
	}

	ozonDecision := engine.CalculateBid(domain.Strategy{Type: domain.StrategyTypeOzonCPCTargetDRR, Params: params}, ctx)
	acosDecision := engine.CalculateBid(domain.Strategy{Type: domain.StrategyTypeACoS, Params: params}, ctx)

	if ozonDecision == nil || acosDecision == nil {
		t.Fatalf("expected decisions from both strategy types, got ozon=%v acos=%v", ozonDecision, acosDecision)
	}
	if ozonDecision.NewBid != acosDecision.NewBid || ozonDecision.OldBid != acosDecision.OldBid {
		t.Fatalf("ozon decision %d→%d diverged from acos decision %d→%d",
			ozonDecision.OldBid, ozonDecision.NewBid, acosDecision.OldBid, acosDecision.NewBid)
	}
	if ozonDecision.NewBid >= ctx.CurrentBid {
		t.Fatalf("ДРР above target must reduce the bid, got %d → %d", ctx.CurrentBid, ozonDecision.NewBid)
	}
	if ozonDecision.ACoS == nil || *ozonDecision.ACoS != 20 {
		t.Fatalf("expected recorded ДРР 20%%, got %v", ozonDecision.ACoS)
	}
}

// TestCalculateBidOzonTargetDRRRespectsMinClicks: no click evidence — no decision.
func TestCalculateBidOzonTargetDRRRespectsMinClicks(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	decision := engine.CalculateBid(domain.Strategy{
		Type:   domain.StrategyTypeOzonCPCTargetDRR,
		Params: domain.StrategyParams{TargetACoS: 10, MinClicks: 100},
	}, BidContext{CurrentBid: 100, Clicks: 5, Spend: 500, Revenue: 1000})
	if decision != nil {
		t.Fatalf("expected nil decision below min_clicks, got %+v", decision)
	}
}

func TestOzonStrategyGuardReason(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	params := domain.StrategyParams{CooldownMinutes: 120, MaxChangesPerDay: 3}

	t.Run("clear", func(t *testing.T) {
		old := now.Add(-3 * time.Hour)
		if reason := ozonStrategyGuardReason(1, &old, params, now); reason != "" {
			t.Fatalf("expected no guard reason, got %q", reason)
		}
	})
	t.Run("no history", func(t *testing.T) {
		if reason := ozonStrategyGuardReason(0, nil, params, now); reason != "" {
			t.Fatalf("expected no guard reason without history, got %q", reason)
		}
	})
	t.Run("cooldown active", func(t *testing.T) {
		recent := now.Add(-30 * time.Minute)
		if reason := ozonStrategyGuardReason(1, &recent, params, now); reason == "" {
			t.Fatal("expected cooldown guard reason")
		}
	})
	t.Run("daily cap reached", func(t *testing.T) {
		old := now.Add(-5 * time.Hour)
		if reason := ozonStrategyGuardReason(3, &old, params, now); reason == "" {
			t.Fatal("expected daily cap guard reason")
		}
	})
	t.Run("cooldown boundary is exclusive", func(t *testing.T) {
		exactly := now.Add(-120 * time.Minute)
		if reason := ozonStrategyGuardReason(1, &exactly, params, now); reason != "" {
			t.Fatalf("change exactly cooldown ago must pass, got %q", reason)
		}
	})
}

func TestOzonStrategyDayStart(t *testing.T) {
	now := time.Date(2026, 8, 7, 23, 59, 59, 0, time.FixedZone("MSK", 3*3600))
	dayStart := ozonStrategyDayStart(now)
	if dayStart != time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("unexpected day start %v", dayStart)
	}
}

// TestWBAutomationSkipsOzonStrategies pins that IsOzonStrategy covers the new
// type and that price/ozon classification does not overlap.
func TestWBAutomationSkipsOzonStrategies(t *testing.T) {
	if !domain.IsOzonStrategy(domain.StrategyTypeOzonCPCTargetDRR) {
		t.Fatal("ozon_cpc_target_drr must be classified as an ozon strategy")
	}
	if domain.IsOzonStrategy(domain.StrategyTypeACoS) || domain.IsOzonStrategy(domain.StrategyTypePriceMarginFloor) {
		t.Fatal("WB strategy types must not be classified as ozon strategies")
	}
	if domain.IsPriceStrategy(domain.StrategyTypeOzonCPCTargetDRR) {
		t.Fatal("ozon strategy must not be classified as a price strategy")
	}
}

// Кампании, у которых товаров не спросить, синк не должен спрашивать: Ozon
// отвечает 400 «Кампания не найдена», а с 25.08.2026 Performance API считает
// запросы по квотам, и каждый заведомый отказ отъедает лимит живых кампаний.
func TestOzonCampaignSyncSkipsProducts(t *testing.T) {
	// Архивные и завершённые — независимо от типа.
	skippedStates := []string{"CAMPAIGN_STATE_ARCHIVED", "CAMPAIGN_STATE_FINISHED", "campaign_state_archived", " CAMPAIGN_STATE_ARCHIVED "}
	for _, state := range skippedStates {
		if !ozonCampaignSyncSkipsProducts(state, "SKU") {
			t.Fatalf("состояние %q обязано пропускаться", state)
		}
	}

	// Типы без поштучных товаров — даже когда кампания работает. Список снят
	// с боевых ответов 2026-08-18.
	skippedTypes := []string{"SEARCH_PROMO", "REF_VK", "REF_BLOGGER", "ALL_SKU_PROMO", "BRAND_SHELF", "BANNER", "search_promo"}
	for _, advType := range skippedTypes {
		if !ozonCampaignSyncSkipsProducts("CAMPAIGN_STATE_RUNNING", advType) {
			t.Fatalf("тип %q обязан пропускаться даже у работающей кампании", advType)
		}
	}

	// SKU опрашиваем в любом живом состоянии: остановленную кампанию можно
	// запустить снова.
	for _, state := range []string{"CAMPAIGN_STATE_RUNNING", "CAMPAIGN_STATE_INACTIVE", "CAMPAIGN_STATE_PLANNED", ""} {
		if ozonCampaignSyncSkipsProducts(state, "SKU") {
			t.Fatalf("SKU-кампанию в состоянии %q обязаны опрашивать", state)
		}
	}

	// Незнакомый тип считаем поддерживаемым: один лишний запрос дешевле, чем
	// молча не синхронизировать новый вид кампаний.
	if ozonCampaignSyncSkipsProducts("CAMPAIGN_STATE_RUNNING", "SOMETHING_OZON_ADDED_LATER") {
		t.Fatal("неизвестный тип обязан опрашиваться, а не пропускаться молча")
	}
}
