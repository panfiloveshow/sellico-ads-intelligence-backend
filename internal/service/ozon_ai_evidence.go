package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// The legacy bids/set adapter writes an ambiguous `bid` field, while the
// integration design documents that endpoint as percentage-based/deprecated.
// A fixed-ruble write must stay unavailable until that contract is verified.
const aiCPOBidUnavailableReason = "Автоматическая установка ставки CPO в рублях недоступна: контракт записи Ozon не подтверждён"

func (s *OzonAIManagerService) cpoEconomicsGuardReason(p aiProposal, data *aiCabinetData, params domain.StrategyParams) string {
	product, ok := data.cpoBySKU[p.Target.SKU]
	if !ok {
		return "Товар недоступен для управления оплатой за заказ в этой стратегии"
	}
	if product.UpdatedAt.IsZero() || time.Since(product.UpdatedAt) > time.Duration(params.MaxDataAgeHours)*time.Hour {
		return "Данные оплаты за заказ устарели — требуется синхронизация"
	}
	// bidPrice is the explicit monetary cost returned by Ozon. Do not treat
	// an ambiguous percentage bid as rubles merely because a mirror says so.
	current := product.BidPriceRub
	if current == nil || !isFinitePositive(*current) {
		return "Текущая стоимость заказа в рублях не подтверждена Ozon"
	}
	value := *current
	if p.ActionType == domain.AIActionCPOBid {
		if p.NewValue == nil {
			return "Для ставки CPO требуется значение"
		}
		value = *p.NewValue
		if !isFinitePositive(value) {
			return "Ставка CPO должна быть конечным положительным числом"
		}
		if math.Abs(value-*current) < 0.005 {
			return "Ставка CPO уже установлена"
		}
		if reason := ozonAIChangePercentReason(*current, value, params.MaxChangePercent, "CPO"); reason != "" {
			return reason
		}
	}
	if params.MaxBid > 0 && value > float64(params.MaxBid) {
		return "Ставка CPO превышает максимальную ставку стратегии"
	}
	// A reduction of an existing cost cannot increase the per-order burden.
	if p.ActionType == domain.AIActionCPOBid && value < *current {
		return ""
	}
	if data.totalDRR.Status != totalDRRStatusOK {
		return "ДРР от общего оборота не измерен полностью — увеличение расходов CPO запрещено"
	}
	if reason := totalDRRIncreaseBlockReason(data.totalDRRCeiling, data.totalDRR); reason != "" {
		return reason
	}
	econ, ok := data.economicsBySKU[p.Target.SKU]
	if !ok || econ.PriceRub == nil || !isFinitePositive(*econ.PriceRub) || econ.MarginPct == nil || !isFinitePositive(*econ.MarginPct) {
		return "Цена и положительная маржа товара не подтверждены — увеличение расходов CPO запрещено"
	}
	if econ.CommissionFBOPct == nil && econ.CommissionFBSPct == nil {
		return "Комиссия Ozon неизвестна — допустимую стоимость заказа рассчитать нельзя"
	}
	// Reserve the observed advertising burden before allocating margin to
	// CPO. This conservative ceiling is not a guarantee of future profit.
	availablePct := *econ.MarginPct - data.totalDRR.Value
	if params.TargetACoS > 0 {
		availablePct = math.Min(availablePct, params.TargetACoS)
	}
	if data.totalDRRCeiling != nil {
		availablePct = math.Min(availablePct, *data.totalDRRCeiling-data.totalDRR.Value)
	}
	maxCost := *econ.PriceRub * math.Max(0, availablePct) / 100
	if value >= maxCost {
		return fmt.Sprintf("Стоимость заказа %.2f ₽ не оставляет запаса в доступной рекламной марже (менее %.2f ₽)", value, maxCost)
	}
	return ozonAIStockIncreaseGuardReason(stockPtr(data.stockBySKU, p.Target.SKU), params)
}

func isFinitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (s *OzonAIManagerService) activationObservationReason(ctx context.Context, cabinetID uuid.UUID, campaign sqlcgen.OzonCampaign, p aiProposal, data *aiCabinetData, params domain.StrategyParams, now time.Time) string {
	event, err := s.queries.GetLastOzonAIStateEvent(ctx, uuidToPgtype(cabinetID), campaign.ID, p.Target.OzonCampaignID)
	if err != nil || event.Action != domain.AIActionCampaignPause || event.Source != domain.BidSourceAI {
		return "Последняя подтверждённая остановка не принадлежит ИИ — запуск только вручную или через Копилот"
	}
	days := params.LookbackDays
	if days < aiImpactWindowDays {
		days = aiImpactWindowDays
	}
	until := event.At.Time.UTC().Truncate(24*time.Hour).AddDate(0, 0, days+1)
	if now.Before(until) {
		return fmt.Sprintf("После остановки идёт наблюдение; повторный запуск возможен не раньше %s", until.Format("02.01.2006"))
	}
	repaired, err := s.queries.HasOzonAIRepairAfter(ctx, campaign.ID, uuidToPgtype(cabinetID), p.Target.OzonCampaignID, event.At)
	if err != nil || !repaired {
		return "После остановки не подтверждено изменение ставок или бюджета: повторный запуск с прежними настройками запрещён"
	}
	return aiCampaignDataReason(campaign, data, params, now)
}

func aiCampaignDataReason(campaign sqlcgen.OzonCampaign, data *aiCabinetData, params domain.StrategyParams, now time.Time) string {
	if !campaign.UpdatedAt.Valid || now.Sub(campaign.UpdatedAt.Time) > time.Duration(params.MaxDataAgeHours)*time.Hour {
		return "Состояние кампании устарело — требуется синхронизация"
	}
	days := data.statsByCampaign[campaign.OzonCampaignID]
	var last time.Time
	var clicks int64
	for _, day := range days {
		d, err := time.Parse("2006-01-02", fmt.Sprint(day[0]))
		if err != nil {
			continue
		}
		if d.After(last) {
			last = d
		}
		if v, ok := day[2].(int64); ok {
			clicks += v
		}
	}
	if last.IsZero() || now.Sub(last.Add(24*time.Hour)) > time.Duration(params.MaxDataAgeHours)*time.Hour {
		return "Нет свежей статистики кампании за завершённые дни"
	}
	if clicks < int64(params.MinClicks) {
		return fmt.Sprintf("Недостаточно кликов для решения: %d, требуется %d", clicks, params.MinClicks)
	}
	return ""
}

// A toggle can refresh the mirror timestamp without refreshing its fee.
// Re-read the raw API response; the UI's best-effort mirror fallback cannot
// authorize new spending. This read also runs before executing a whole plan.
func (s *OzonAIManagerService) liveCPOEnableGuard(ctx context.Context, workspaceID, cabinetID uuid.UUID, p aiProposal, data *aiCabinetData, params domain.StrategyParams) string {
	if _, ok := data.cpoBySKU[p.Target.SKU]; !ok {
		return "Товар CPO вне области стратегии"
	}
	creds, err := s.cabinetPerfCreds(ctx, workspaceID, cabinetID)
	if err != nil {
		return "Не удалось подтвердить текущую стоимость CPO: " + truncateError(err.Error())
	}
	products, err := s.actions.perfClient.ListSearchPromoProducts(ctx, creds)
	if err != nil {
		return "Не удалось подтвердить текущую стоимость CPO: " + truncateError(err.Error())
	}
	for _, live := range products {
		if live.SKU != p.Target.SKU {
			continue
		}
		if live.Enabled {
			return "Ozon уже подтверждает включённую оплату за заказ — изменений нет"
		}
		if !isFinitePositive(live.BidPriceRub) {
			return "Ozon не подтвердил текущую стоимость заказа в рублях"
		}
		local := data.cpoBySKU[p.Target.SKU]
		local.BidPriceRub = &live.BidPriceRub
		local.UpdatedAt = time.Now().UTC()
		local.Enabled = live.Enabled
		data.cpoBySKU[p.Target.SKU] = local
		return s.cpoEconomicsGuardReason(p, data, params)
	}
	return "Ozon не вернул товар при проверке текущей стоимости CPO — включение не выполнено"
}
