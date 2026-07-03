package service

import (
	"strings"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestClassifyQuerySignalSEOIdea(t *testing.T) {
	signal, health, reason, action := classifyQuerySignal(domain.Phrase{}, domain.AdsMetricsSummary{
		Clicks:  40,
		Atbs:    8,
		Orders:  3,
		Spend:   1200,
		Revenue: 12000,
		DRR:     10,
	})

	if signal != "seo_idea" || health != "growing" {
		t.Fatalf("expected seo_idea/growing, got %s/%s", signal, health)
	}
	if reason == nil || action == nil {
		t.Fatalf("expected explanation and action")
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func TestClassifyProductHealthMarksROASOnlyAsGrowthCandidate(t *testing.T) {
	health, reason, action := classifyProductHealth(domain.AdsMetricsSummary{
		Clicks:  80,
		Orders:  5,
		Spend:   1000,
		Revenue: 7000,
	}, 1, 3)

	if health != "growth_candidate" {
		t.Fatalf("expected growth_candidate without product margin evidence, got %s", health)
	}
	if reason == nil || action == nil {
		t.Fatalf("expected reason and action")
	}
	if !containsAll(*reason, "маржа", "не подтверждена") || !containsAll(*action, "unit economics", "остатки") {
		t.Fatalf("expected margin-aware explanation/action, got reason=%q action=%q", *reason, *action)
	}
}

func TestApplyProductEconomicsHealthUsesRealMaxDRR(t *testing.T) {
	maxAllowedDRR := 12.0

	health, reason, action := applyProductEconomicsHealth("growth_candidate", stringPtr("old"), stringPtr("old"), domain.AdsMetricsSummary{
		DataMode: "exact",
		Orders:   5,
		Spend:    1800,
		Revenue:  10000,
		DRR:      18,
	}, domain.ProductBusinessSummary{
		MaxAllowedDRR:     &maxAllowedDRR,
		EconomicsDataMode: "manual",
	})

	if health != "reduce_bid" {
		t.Fatalf("expected reduce_bid when real max DRR is exceeded, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "18.0%", "12.0%", "unit economics") || !containsAll(*action, "маржинальный ДРР") {
		t.Fatalf("expected max DRR evidence in reason/action, got reason=%v action=%v", reason, action)
	}
}

func TestApplyProductEconomicsHealthConfirmsGrowthCandidateWithRealEconomics(t *testing.T) {
	margin := int64(2500)
	maxAllowedDRR := 30.0

	health, reason, action := applyProductEconomicsHealth("growth_candidate", stringPtr("old"), stringPtr("old"), domain.AdsMetricsSummary{
		DataMode: "exact",
		Orders:   5,
		Spend:    1000,
		Revenue:  10000,
		DRR:      10,
	}, domain.ProductBusinessSummary{
		MarginBeforeAds:   &margin,
		MaxAllowedDRR:     &maxAllowedDRR,
		EconomicsDataMode: "manual",
	})

	if health != "growth_candidate" {
		t.Fatalf("expected growth_candidate with real economics, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "unit economics", "read-model") || !containsAll(*action, "максимального ДРР", "лимитов автопилота") {
		t.Fatalf("expected confirmed economics explanation/action, got reason=%v action=%v", reason, action)
	}
}

func TestApplyProductSalesFunnelHealthDetectsCardIssueFromRealWBOpenCounts(t *testing.T) {
	health, reason, action := applyProductSalesFunnelHealth("hold", stringPtr("ok"), stringPtr("ok"), domain.ProductBusinessSummary{
		SalesFunnelOpenCount:  45,
		SalesFunnelCartCount:  0,
		SalesFunnelOrderCount: 0,
		SalesFunnelDataMode:   "reports",
	})

	if health != "card_issue" {
		t.Fatalf("expected card_issue from real WB open/cart/order counts, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "45 переходов", "0 корзин", "0 заказов") || !containsAll(*action, "карточку", "не повышайте ставки") {
		t.Fatalf("expected WB funnel evidence in reason/action, got reason=%v action=%v", reason, action)
	}
}

func TestApplyProductSalesFunnelHealthDetectsOfferIssueFromRealWBCarts(t *testing.T) {
	health, reason, action := applyProductSalesFunnelHealth("hold", stringPtr("ok"), stringPtr("ok"), domain.ProductBusinessSummary{
		SalesFunnelOpenCount:  80,
		SalesFunnelCartCount:  5,
		SalesFunnelOrderCount: 0,
		SalesFunnelDataMode:   "reports",
	})

	if health != "offer_issue" {
		t.Fatalf("expected offer_issue from real WB cart/order counts, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "5 корзин", "0 заказов") || !containsAll(*action, "цену", "доставку") {
		t.Fatalf("expected WB funnel offer evidence in reason/action, got reason=%v action=%v", reason, action)
	}
}

func TestApplyProductSalesFunnelHealthDoesNotOverrideHardStop(t *testing.T) {
	health, reason, action := applyProductSalesFunnelHealth("reduce_bid", stringPtr("keep"), stringPtr("keep"), domain.ProductBusinessSummary{
		SalesFunnelOpenCount:  60,
		SalesFunnelCartCount:  0,
		SalesFunnelOrderCount: 0,
		SalesFunnelDataMode:   "reports",
	})

	if health != "reduce_bid" || reason == nil || *reason != "keep" || action == nil || *action != "keep" {
		t.Fatalf("expected sales funnel guard not to override hard stop, got health=%s reason=%v action=%v", health, reason, action)
	}
}

func TestClassifyProductHealthDetectsCardIssueFromClicksWithoutCarts(t *testing.T) {
	health, reason, action := classifyProductHealth(domain.AdsMetricsSummary{
		Impressions: 800,
		Clicks:      12,
		Spend:       900,
		Atbs:        0,
		Orders:      0,
	}, 1, 4)

	if health != "card_issue" {
		t.Fatalf("expected card_issue, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "0 корзин", "0 заказов") || !containsAll(*action, "карточку", "цену") {
		t.Fatalf("expected card issue explanation/action, got reason=%v action=%v", reason, action)
	}
}

func TestClassifyProductHealthDetectsOfferIssueFromCartsWithoutOrders(t *testing.T) {
	health, reason, action := classifyProductHealth(domain.AdsMetricsSummary{
		Clicks: 12,
		Atbs:   3,
		Spend:  900,
		Orders: 0,
	}, 1, 4)

	if health != "offer_issue" {
		t.Fatalf("expected offer_issue, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "3 корзин", "0 заказов") || !containsAll(*action, "доставку", "остатки") {
		t.Fatalf("expected offer issue explanation/action, got reason=%v action=%v", reason, action)
	}
}

func TestClassifyProductHealthDetectsReduceBidForHighDRROrders(t *testing.T) {
	health, reason, action := classifyProductHealth(domain.AdsMetricsSummary{
		Clicks:  30,
		Atbs:    5,
		Orders:  2,
		Spend:   2400,
		Revenue: 4000,
		DRR:     60,
	}, 1, 4)

	if health != "reduce_bid" {
		t.Fatalf("expected reduce_bid, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "ДРР") || !containsAll(*action, "Снизьте ставку") {
		t.Fatalf("expected reduce bid explanation/action, got reason=%v action=%v", reason, action)
	}
}

func TestClassifyProductHealthUsesHoldForNormalProduct(t *testing.T) {
	health, reason, action := classifyProductHealth(domain.AdsMetricsSummary{
		Impressions: 600,
		Clicks:      20,
		Atbs:        4,
		Orders:      1,
		Spend:       700,
		Revenue:     1800,
		DRR:         30,
	}, 1, 4)

	if health != "hold" {
		t.Fatalf("expected hold, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*action, "Держите") {
		t.Fatalf("expected hold action, got reason=%v action=%v", reason, action)
	}
}

func TestApplyProductStockHealthMarksNoStockFromRealEvidence(t *testing.T) {
	health, reason, action := applyProductStockHealth("hold", stringPtr("ok"), stringPtr("hold"), productStockEvidence{
		StockTotal: 0,
		Source:     "product_snapshot",
	}, true)

	if health != "no_stock" {
		t.Fatalf("expected no_stock, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "подтверждённый остаток: 0", "product_snapshot") || !containsAll(*action, "Остановите масштабирование") {
		t.Fatalf("expected stock evidence reason/action, got reason=%v action=%v", reason, action)
	}
}

func TestApplyProductStockHealthIgnoresMissingEvidence(t *testing.T) {
	health, reason, action := applyProductStockHealth("hold", stringPtr("ok"), stringPtr("hold"), productStockEvidence{}, false)

	if health != "hold" || reason == nil || *reason != "ok" || action == nil || *action != "hold" {
		t.Fatalf("expected unchanged health without evidence, got %s %v %v", health, reason, action)
	}
}

func TestApplyProductStockHealthMarksLowStockFromRealEvidence(t *testing.T) {
	health, reason, action := applyProductStockHealth("hold", stringPtr("ok"), stringPtr("hold"), productStockEvidence{
		StockTotal: 3,
		Source:     "product_snapshot",
	}, true)

	if health != "low_stock" {
		t.Fatalf("expected low_stock, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "подтверждённый остаток: 3", "product_snapshot") {
		t.Fatalf("expected low stock evidence reason/action, got reason=%v action=%v", reason, action)
	}
}

func TestClassifyCampaignHealthMarksROASOnlyAsGrowthCandidate(t *testing.T) {
	health, reason, action := classifyCampaignHealth(domain.AdsMetricsSummary{
		Clicks:  80,
		Orders:  5,
		Spend:   1000,
		Revenue: 7000,
	}, 1, 3, "active")

	if health != "growth_candidate" {
		t.Fatalf("expected growth_candidate without product margin evidence, got %s", health)
	}
	if reason == nil || action == nil {
		t.Fatalf("expected reason and action")
	}
	if !containsAll(*reason, "маржа", "не подтверждена") || !containsAll(*action, "unit economics", "остатки") {
		t.Fatalf("expected margin-aware explanation/action, got reason=%q action=%q", *reason, *action)
	}
}

func TestClassifyCampaignHealthMarksActiveMissingStats(t *testing.T) {
	health, reason, action := classifyCampaignHealth(domain.AdsMetricsSummary{
		DataMode: "unavailable",
	}, 1, 1, "active")

	if health != "no_stats" {
		t.Fatalf("expected no_stats for active campaign without stat rows, got %s", health)
	}
	if reason == nil || action == nil || !containsAll(*reason, "нет подтверждённой рекламной статистики") || !containsAll(*action, "не меняйте ставки") {
		t.Fatalf("expected no-stats explanation/action, got reason=%v action=%v", reason, action)
	}
}

func TestProductScaleViewIncludesGrowthCandidateButProfitableCampaignViewDoesNot(t *testing.T) {
	if !matchesProductView("scale", "growth_candidate", nil) {
		t.Fatalf("growth_candidate should remain visible in product scale candidates")
	}
	if matchesCampaignView("profitable", "active", "growth_candidate", nil) {
		t.Fatalf("growth_candidate must not be treated as proven profitable")
	}
}

func TestCampaignWatchViewIncludesNoStatsStatus(t *testing.T) {
	if !matchesCampaignView("watch", "active", "no_stats", nil) {
		t.Fatalf("watch view should include active campaigns without stats")
	}
}

func TestProductSaveViewIncludesDecisionStatuses(t *testing.T) {
	for _, status := range []string{"stop", "reduce_bid", "card_issue", "offer_issue", "low_stock", "no_stock"} {
		if !matchesProductView("save", status, nil) {
			t.Fatalf("save view should include %s", status)
		}
	}
}

func TestProductWatchViewIncludesHoldStatus(t *testing.T) {
	if !matchesProductView("watch", "hold", nil) {
		t.Fatalf("watch view should include hold")
	}
}

func TestClassifyQuerySignalWinner(t *testing.T) {
	signal, health, _, _ := classifyQuerySignal(domain.Phrase{}, domain.AdsMetricsSummary{
		Clicks:  20,
		Atbs:    3,
		Orders:  1,
		Spend:   900,
		Revenue: 4500,
		DRR:     20,
	})

	if signal != "winner" || health != "growing" {
		t.Fatalf("expected winner/growing, got %s/%s", signal, health)
	}
}

func TestClassifyQuerySignalTrash(t *testing.T) {
	signal, health, _, _ := classifyQuerySignal(domain.Phrase{}, domain.AdsMetricsSummary{
		Clicks: 10,
		Spend:  700,
	})

	if signal != "trash" || health != "waste" {
		t.Fatalf("expected trash/waste, got %s/%s", signal, health)
	}
}

func TestClassifyQuerySignalWatchForCartsWithoutOrders(t *testing.T) {
	signal, health, _, _ := classifyQuerySignal(domain.Phrase{}, domain.AdsMetricsSummary{
		Clicks: 12,
		Atbs:   2,
		Spend:  400,
	})

	if signal != "watch" || health != "monitor" {
		t.Fatalf("expected watch/monitor, got %s/%s", signal, health)
	}
}

func TestClassifyQuerySignalWatchForHighSpendCartsWithoutOrders(t *testing.T) {
	// High spend with carts but no orders is an offer/price problem, not ad waste:
	// it must classify as watch/monitor, never as a "loser" that suggests cutting the bid.
	signal, health, _, action := classifyQuerySignal(domain.Phrase{}, domain.AdsMetricsSummary{
		Clicks: 12,
		Atbs:   3,
		Spend:  1500,
		Orders: 0,
	})

	if signal != "watch" || health != "monitor" {
		t.Fatalf("expected watch/monitor for high-spend carts-without-orders, got %s/%s", signal, health)
	}
	if action == nil || !containsAll(*action, "цену") {
		t.Fatalf("expected offer-fix action, got %v", action)
	}
}

func TestClassifyQuerySignalInsufficientDataForLowExactStats(t *testing.T) {
	signal, health, reason, action := classifyQuerySignal(domain.Phrase{}, domain.AdsMetricsSummary{
		DataMode:    "exact",
		Impressions: 120,
		Clicks:      3,
		Spend:       180,
	})

	if signal != "insufficient_data" || health != "insufficient_data" {
		t.Fatalf("expected insufficient_data, got %s/%s", signal, health)
	}
	if reason == nil || !strings.Contains(*reason, "мало рекламной статистики") {
		t.Fatalf("expected low data reason, got %v", reason)
	}
	if action == nil || !strings.Contains(*action, "Продолжите тест с лимитом") {
		t.Fatalf("expected limited test action, got %v", action)
	}
}

func TestQuerySignalCompatibilityFilters(t *testing.T) {
	if !querySignalMatches("winner", "promising") {
		t.Fatalf("winner should match legacy promising filter")
	}
	if !querySignalMatches("seo_idea", "promising") {
		t.Fatalf("seo_idea should match legacy promising filter")
	}
	if !querySignalMatches("trash", "waste") {
		t.Fatalf("trash should match legacy waste filter")
	}
	if !querySignalMatches("loser", "waste") {
		t.Fatalf("loser should match legacy waste filter")
	}
	if !querySignalMatches("watch", "monitor") {
		t.Fatalf("watch should match monitor selection")
	}
}

func TestCountProductDecisionsUsesCurrentSummaryDecisions(t *testing.T) {
	counts := countProductDecisions([]domain.ProductAdsSummary{
		{Decision: domain.ProductDecisionSummary{Decision: "scale_candidate_partial"}},
		{Decision: domain.ProductDecisionSummary{Decision: "scale_candidate_partial"}},
		{Decision: domain.ProductDecisionSummary{Decision: "fix_product_readiness"}},
		{},
	})

	if counts["scale_candidate_partial"] != 2 {
		t.Fatalf("expected 2 scale candidates, got %+v", counts)
	}
	if counts["fix_product_readiness"] != 1 {
		t.Fatalf("expected 1 readiness fix, got %+v", counts)
	}
	if counts["unknown"] != 1 {
		t.Fatalf("expected empty decision to be counted as unknown, got %+v", counts)
	}
}

func TestCountProductDecisionsReturnsNilForNoProducts(t *testing.T) {
	if counts := countProductDecisions(nil); counts != nil {
		t.Fatalf("expected nil counts for no products, got %+v", counts)
	}
}
