package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func aiTestParams() domain.StrategyParams {
	return domain.StrategyParams{
		MinBid:           50,
		MaxBid:           500,
		MaxChangePercent: 15,
	}
}

// TestOzonAIBidGuard_RejectsOverLimitProposal is the required case: the AI
// proposes a bid jump beyond MaxChangePercent and the guardrail rejects it.
func TestOzonAIBidGuard_RejectsOverLimitProposal(t *testing.T) {
	params := aiTestParams()

	// +50% vs the 15% cap — rejected.
	reason := ozonAIBidGuardReason(100, 150, 0, params)
	require.NotEmpty(t, reason)
	assert.Contains(t, reason, "max_change_percent")

	// −40% is over the cap too (the cap is symmetric).
	reason = ozonAIBidGuardReason(100, 60, 0, params)
	require.NotEmpty(t, reason)
	assert.Contains(t, reason, "max_change_percent")

	// +10% passes.
	assert.Empty(t, ozonAIBidGuardReason(100, 110, 0, params))
}

func TestOzonAIBidGuard_Clamps(t *testing.T) {
	params := aiTestParams()

	assert.Contains(t, ozonAIBidGuardReason(100, -5, 0, params), "positive")
	// Below Ozon's SKU minimum.
	assert.Contains(t, ozonAIBidGuardReason(60, 55, 58, params), "Ozon minimum")
	// Below strategy min_bid.
	assert.Contains(t, ozonAIBidGuardReason(52, 45, 0, params), "min_bid")
	// Above strategy max_bid (change percent would also fire, but the
	// absolute clamp reports first).
	assert.Contains(t, ozonAIBidGuardReason(490, 550, 0, params), "max_bid")
}

func TestOzonAIBudgetGuard(t *testing.T) {
	params := aiTestParams()
	current := int64(10000)

	// Weekly floor.
	assert.Contains(t, ozonAIBudgetGuardReason(nil, 800, true, params), "1000₽")
	// Daily floor.
	assert.Contains(t, ozonAIBudgetGuardReason(nil, 100, false, params), "150₽")
	// Over-limit change vs current budget.
	reason := ozonAIBudgetGuardReason(&current, 20000, true, params)
	require.NotEmpty(t, reason)
	assert.Contains(t, reason, "max_change_percent")
	// Within limits.
	assert.Empty(t, ozonAIBudgetGuardReason(&current, 11000, true, params))
	// No current budget: only the absolute floor applies.
	assert.Empty(t, ozonAIBudgetGuardReason(nil, 5000, true, params))
}

func TestOzonAICPOBidGuard(t *testing.T) {
	assert.Contains(t, ozonAICPOBidGuardReason(0, 0), "positive")
	assert.Contains(t, ozonAICPOBidGuardReason(30, 50), "CPO minimum")
	assert.Empty(t, ozonAICPOBidGuardReason(60, 50))
	// Unknown minimum (0) does not block — CPO is riskless by design.
	assert.Empty(t, ozonAICPOBidGuardReason(60, 0))
}

func TestOzonAICampaignStateGuard(t *testing.T) {
	assert.Empty(t, ozonAICampaignStateGuardReason(domain.AIActionCampaignPause, "CAMPAIGN_STATE_RUNNING"))
	assert.NotEmpty(t, ozonAICampaignStateGuardReason(domain.AIActionCampaignPause, "CAMPAIGN_STATE_INACTIVE"))
	assert.Empty(t, ozonAICampaignStateGuardReason(domain.AIActionCampaignActivate, "CAMPAIGN_STATE_INACTIVE"))
	assert.Empty(t, ozonAICampaignStateGuardReason(domain.AIActionCampaignActivate, "CAMPAIGN_STATE_STOPPED"))
	assert.NotEmpty(t, ozonAICampaignStateGuardReason(domain.AIActionCampaignActivate, "CAMPAIGN_STATE_RUNNING"))
}

// TestMarshalAIContextPack_SizeCap builds an oversized pack (200 campaigns ×
// 30 SKUs with daily rows) and verifies the degradation ladder lands under
// the cap without erroring.
func TestMarshalAIContextPack_SizeCap(t *testing.T) {
	pack := &aiContextPack{
		Rules: aiPackRules{TargetDRRPct: 12, AutomationLevel: 3, MaxChangePercent: 15, MinBidRub: 50, MaxBidRub: 500},
	}
	for i := 0; i < 200; i++ {
		campaign := aiPackCampaign{
			OzonCampaignID: int64(100000 + i),
			Title:          fmt.Sprintf("Кампания с длинным названием номер %d — трафареты и вывод в топ", i),
			State:          "CAMPAIGN_STATE_RUNNING",
			Placement:      "PLACEMENT_TOP_PROMOTION",
			Totals:         aiPackTotals{Views: 100000, Clicks: 2500, SpendRub: 45000.55, Orders: 120, RevenueRub: 380000.10, DRR: 11.8},
		}
		for d := 0; d < aiPackStatsWindowDays; d++ {
			campaign.Daily = append(campaign.Daily, aiPackDay{"2026-08-01", int64(7000), int64(180), 3200.5, int64(9), 27000.0})
		}
		for p := 0; p < aiPackTopProductsPerCampaign; p++ {
			campaign.Products = append(campaign.Products, aiPackProduct{SKU: int64(900000000 + i*100 + p), BidRub: 120.5})
		}
		pack.Campaigns = append(pack.Campaigns, campaign)
	}
	for i := 0; i < aiPackCPOLimit; i++ {
		bid := 95.0
		pack.CPO = append(pack.CPO, aiPackCPO{SKU: int64(800000000 + i), Enabled: i%2 == 0, BidRub: &bid})
	}
	for i := 0; i < aiPackEconomicsLimit; i++ {
		price, minPrice, netPrice, commission := 1990.0, 1490.0, 700.0, 18.5
		pack.Economics = append(pack.Economics, aiPackEconomics{
			SKU: int64(900000000 + i), OfferID: fmt.Sprintf("ART-%06d", i),
			PriceRub: &price, MinPriceRub: &minPrice, NetPriceRub: &netPrice,
			CommissionFBOPct: &commission, ColorIndex: "GREEN",
		})
	}

	// Sanity: the raw pack really is oversized before capping.
	raw, err := json.Marshal(pack)
	require.NoError(t, err)
	require.Greater(t, len(raw), aiContextPackMaxBytes)

	payload, err := marshalAIContextPack(pack, aiContextPackMaxBytes)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(payload), aiContextPackMaxBytes)

	// The capped payload must stay valid JSON and keep the rules block.
	var decoded aiContextPack
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.Equal(t, 12.0, decoded.Rules.TargetDRRPct)
	assert.NotEmpty(t, decoded.Campaigns, "campaign detail must survive the cuts")
}

func TestOzonAIClampBidToChangeLimit(t *testing.T) {
	tests := []struct {
		name        string
		current     float64
		proposed    float64
		maxChange   float64
		wantBid     float64
		wantClamped bool
	}{
		{
			// Ровно случай из продакшена: 38 → 32 это −15.8 % при лимите 15 %.
			// Раньше предложение выбрасывалось целиком из-за 0.8 п.п.
			name:    "снижение чуть за лимитом подтягивается к границе",
			current: 38, proposed: 32, maxChange: 15,
			wantBid: 33, wantClamped: true, // 38 − 5.7 = 32.3 → вверх до 33
		},
		{
			name:    "второй случай из продакшена",
			current: 57, proposed: 48, maxChange: 15,
			wantBid: 49, wantClamped: true, // 57 − 8.55 = 48.45 → вверх до 49
		},
		{
			name:    "внутри лимита остаётся как есть",
			current: 47, proposed: 40, maxChange: 15,
			wantBid: 40, wantClamped: false,
		},
		{
			name:    "повышение за лимитом тоже подтягивается",
			current: 100, proposed: 130, maxChange: 15,
			wantBid: 115, wantClamped: true,
		},
		{
			name:    "лимит не задан — не вмешиваемся",
			current: 38, proposed: 10, maxChange: 0,
			wantBid: 10, wantClamped: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, note := ozonAIClampBidToChangeLimit(tc.current, tc.proposed, tc.maxChange)
			if got != tc.wantBid {
				t.Fatalf("bid = %v, want %v", got, tc.wantBid)
			}
			if clamped := note != ""; clamped != tc.wantClamped {
				t.Fatalf("clamped = %v (note %q), want %v", clamped, note, tc.wantClamped)
			}
		})
	}
}

// Зажатая ставка обязана проходить тот же гардрейл, который её отклонял.
func TestClampedBidPassesChangeGuard(t *testing.T) {
	params := domain.StrategyParams{MaxChangePercent: 15, MinBid: 5, MaxBid: 500}

	if reason := ozonAIBidGuardReason(38, 32, 0, params); reason == "" {
		t.Fatal("исходное предложение должно отклоняться — иначе тест бессмысленен")
	}
	clamped, note := ozonAIClampBidToChangeLimit(38, 32, params.MaxChangePercent)
	if note == "" {
		t.Fatal("ожидалось ограничение")
	}
	if reason := ozonAIBidGuardReason(38, clamped, 0, params); reason != "" {
		t.Fatalf("зажатая ставка всё ещё отклоняется: %s", reason)
	}
}

// Ставка меньше ~7 ₽ не могла сдвинуться вообще: 15 % от 6 ₽ — это 90 копеек,
// целого рубля не помещается, и предложение отклонялось. На проде такие SKU
// стояли неподвижно: «current 6.00 → proposed 5.00» и «current 5.00 →
// proposed 6.00» отклонялись после того, как ограничение уже было внедрено.
func TestClampAllowsOneRubleStepOnTinyBids(t *testing.T) {
	cases := []struct {
		current, proposed, want float64
	}{
		{6, 5, 5}, // вниз: рубль — минимально возможный шаг
		{5, 6, 6}, // вверх
		{4, 1, 3}, // крупное предложение всё равно ужимается до рубля
	}
	for _, c := range cases {
		got, note := ozonAIClampBidToChangeLimit(c.current, c.proposed, 15)
		if note == "" {
			t.Fatalf("ставка %.0f→%.0f: ожидалось ограничение", c.current, c.proposed)
		}
		if got != c.want {
			t.Fatalf("ставка %.0f→%.0f: получено %.0f, ожидалось %.0f", c.current, c.proposed, got, c.want)
		}
	}
	// Ставка 1 ₽ вниз шагнуть не может — ушла бы в ноль.
	if got, note := ozonAIClampBidToChangeLimit(1, 0.5, 15); note != "" || got != 0.5 {
		t.Fatalf("ставка 1₽ вниз: получено %.2f (%q), вмешиваться нельзя", got, note)
	}
}

// Бюджеты не зажимались вовсе: «12 000 → 17 000» это 41.7 % при пределе 15 %,
// и предложение выбрасывалось целиком.
func TestClampBudgetToChangeLimit(t *testing.T) {
	got, note := ozonAIClampBudgetToChangeLimit(12000, 17000, 15)
	if note == "" {
		t.Fatal("ожидалось ограничение бюджета")
	}
	if got != 13800 { // 12000 + 15 %
		t.Fatalf("получено %d, ожидалось 13800", got)
	}
	// Зажатое значение обязано проходить тот же страж, который его отклонял.
	params := domain.StrategyParams{MaxChangePercent: 15}
	current := int64(12000)
	if reason := ozonAIBudgetGuardReason(&current, 17000, false, params); reason == "" {
		t.Fatal("исходное предложение должно отклоняться — иначе тест бессмыслен")
	}
	if reason := ozonAIBudgetGuardReason(&current, got, false, params); reason != "" {
		t.Fatalf("зажатый бюджет всё ещё отклоняется: %s", reason)
	}
	// Внутри предела не трогаем.
	if v, n := ozonAIClampBudgetToChangeLimit(12000, 13000, 15); n != "" || v != 13000 {
		t.Fatalf("получено %d (%q), ожидалось без изменений", v, n)
	}
}

// Кампания без бюджета не имела якоря для процентного предела: модель могла
// поставить любой бюджет. Теперь якорь — собственный расход кампании (2×).
func TestUnbudgetedBudgetGuard(t *testing.T) {
	// Расход 7000₽ за 14 дней = 500₽/день → недельный потолок 7000₽.
	if reason := ozonAIUnbudgetedBudgetGuardReason(7000, 7000, true, 14); reason != "" {
		t.Fatalf("бюджет в пределах 2× расхода должен проходить: %s", reason)
	}
	if reason := ozonAIUnbudgetedBudgetGuardReason(7001, 7000, true, 14); reason == "" {
		t.Fatal("бюджет выше 2× расхода обязан отклоняться")
	}
	// Дневной вариант: потолок 1000₽.
	if reason := ozonAIUnbudgetedBudgetGuardReason(1001, 7000, false, 14); reason == "" {
		t.Fatal("дневной бюджет выше 2× дневного расхода обязан отклоняться")
	}
	// Нет расхода — нет якоря: первый бюджет задаёт человек.
	if reason := ozonAIUnbudgetedBudgetGuardReason(5000, 0, true, 14); reason == "" {
		t.Fatal("без расхода любое предложение обязано отклоняться")
	}
}
