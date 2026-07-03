package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func classifyProductHealth(metrics domain.AdsMetricsSummary, campaignsCount, queriesCount int) (string, *string, *string) {
	switch {
	case metrics.DataMode == "unavailable" || campaignsCount == 0:
		return "insufficient_data", stringPtr("Товар пока не имеет устойчивой связки с кампаниями и рекламной статистикой."), stringPtr("Проверьте привязку товара к кампаниям и дождитесь следующего auto-sync.")
	case metrics.Impressions >= 1000 && metrics.Clicks == 0:
		return "card_issue", stringPtr("Товар получает показы, но не собирает клики."), stringPtr("Проверьте релевантность запросов, главное фото, цену и заголовок карточки; не повышайте ставку вслепую.")
	case metrics.Clicks >= 5 && metrics.Atbs == 0 && metrics.Orders == 0:
		return "card_issue", stringPtr(fmt.Sprintf("Товар получил %d кликов, но 0 корзин и 0 заказов.", metrics.Clicks)), stringPtr("Создайте задачу проверить карточку, цену, фото, отзывы, комплектацию и релевантность трафика.")
	case metrics.Clicks >= 5 && metrics.Atbs > 0 && metrics.Orders == 0:
		return "offer_issue", stringPtr(fmt.Sprintf("Товар получил %d корзин, но 0 заказов.", metrics.Atbs)), stringPtr("Проверьте цену, доставку, остатки, варианты, комплектацию и доверие к карточке; снижайте ставку мягко.")
	case metrics.Spend >= 3000 && metrics.Orders == 0:
		return "stop", stringPtr(fmt.Sprintf("Товар потратил %d ₽ без заказов за выбранный период.", metrics.Spend)), stringPtr("Откройте связанные кампании и сократите расходные запросы; минусуйте только после проверки релевантности.")
	case metrics.Orders > 0 && metrics.DRR > 0 && metrics.DRR > 35:
		return "reduce_bid", stringPtr(fmt.Sprintf("Товар даёт заказы, но ДРР %.1f%% выше безопасного рекламного порога.", metrics.DRR)), stringPtr("Снизьте ставку и оставьте в приоритете только запросы с лучшим CPO.")
	case metrics.Orders >= 3 && metrics.Revenue > metrics.Spend*5:
		return "growth_candidate", stringPtr("Товар даёт заказы и рекламная выручка выше расхода, но маржа не подтверждена в этом read-model."), stringPtr("Проверьте unit economics, остатки и целевой ДРР перед масштабированием.")
	case queriesCount == 0 && metrics.Spend > 0:
		return "partial", stringPtr("По товару есть расход, но запросный слой ещё не доехал полностью."), stringPtr("Откройте Jobs и проверьте полноту sync по normquery и phrase stats.")
	case metrics.Impressions < 200 && metrics.Clicks < 5 && metrics.Orders == 0:
		return "insufficient_data", stringPtr("По товару пока мало рекламной статистики для резкого решения."), stringPtr("Продолжите тест с лимитом и дождитесь большего объёма показов/кликов.")
	default:
		return "hold", stringPtr("Товар участвует в рекламе без сильного негативного сигнала за выбранный период."), stringPtr("Держите текущий режим и мягко оптимизируйте слабые запросы.")
	}
}

func applyProductStockHealth(health string, reason, action *string, evidence productStockEvidence, ok bool) (string, *string, *string) {
	if !ok || evidence.StockTotal > stockAlertThreshold {
		return health, reason, action
	}

	status := "low_stock"
	if evidence.StockTotal == 0 {
		status = "no_stock"
	}

	return status,
		stringPtr(fmt.Sprintf("Товар рекламируется, но подтверждённый остаток: %d шт. Источник: %s.", evidence.StockTotal, evidence.Source)),
		stringPtr("Остановите масштабирование и пополните остатки перед повышением ставок или бюджета.")
}

func applyProductEconomicsHealth(health string, reason, action *string, metrics domain.AdsMetricsSummary, business domain.ProductBusinessSummary) (string, *string, *string) {
	if metrics.DataMode == "unavailable" || metrics.Orders == 0 {
		return health, reason, action
	}
	if business.MaxAllowedDRR != nil && metrics.DRR > 0 && metrics.DRR > *business.MaxAllowedDRR {
		return "reduce_bid",
			stringPtr(fmt.Sprintf("Товар даёт заказы, но ДРР %.1f%% выше максимального допустимого ДРР %.1f%% по реальной unit economics.", metrics.DRR, *business.MaxAllowedDRR)),
			stringPtr("Снизьте ставки и оставьте только кластеры, которые укладываются в маржинальный ДРР товара.")
	}
	if business.MarginBeforeAds != nil && *business.MarginBeforeAds <= 0 {
		return "reduce_bid",
			stringPtr(fmt.Sprintf("Маржа до рекламы по реальной unit economics: %d ₽; товар нельзя масштабировать по ROAS.", *business.MarginBeforeAds)),
			stringPtr("Не повышайте ставки; проверьте цену, себестоимость, комиссию, логистику и налоговую модель.")
	}
	if health == "growth_candidate" && productBusinessHasEconomicsEvidence(business) {
		return health,
			stringPtr("Товар даёт заказы, рекламная выручка выше расхода, а unit economics уже есть в read-model."),
			stringPtr("Проверьте остатки и повышайте ставки только в пределах максимального ДРР и лимитов автопилота.")
	}
	return health, reason, action
}

func applyProductSalesFunnelHealth(health string, reason, action *string, business domain.ProductBusinessSummary) (string, *string, *string) {
	if business.SalesFunnelDataMode != "reports" || productHealthIsHardStop(health) {
		return health, reason, action
	}
	if business.SalesFunnelOpenCount >= 30 && business.SalesFunnelCartCount == 0 && business.SalesFunnelOrderCount == 0 {
		return "card_issue",
			stringPtr(fmt.Sprintf("WB Analytics: %d переходов в карточку, но 0 корзин и 0 заказов за период.", business.SalesFunnelOpenCount)),
			stringPtr("Проверьте карточку, цену, фото, отзывы и релевантность трафика; не повышайте ставки до исправления причины.")
	}
	if business.SalesFunnelCartCount >= 3 && business.SalesFunnelOrderCount == 0 {
		return "offer_issue",
			stringPtr(fmt.Sprintf("WB Analytics: %d корзин по карточке, но 0 заказов за период.", business.SalesFunnelCartCount)),
			stringPtr("Проверьте цену, доставку, комплектацию, остатки и доверие к карточке; снижайте ставки мягко.")
	}
	return health, reason, action
}

func productHealthIsHardStop(health string) bool {
	switch health {
	case "stop", "reduce_bid", "no_stock", "low_stock":
		return true
	default:
		return false
	}
}

func classifyCampaignHealth(metrics domain.AdsMetricsSummary, productsCount, queriesCount int, status string) (string, *string, *string) {
	switch {
	case status == "active" && metrics.DataMode == "unavailable":
		return "no_stats", stringPtr("За выбранный период по активной кампании нет подтверждённой рекламной статистики."), stringPtr("Проверьте последний sync, права WB API и не меняйте ставки до появления статистики.")
	case status == "paused" && metrics.Spend > 0:
		return "stale", stringPtr("Кампания уже не активна, но остаётся важной в историческом анализе."), stringPtr("Решите, стоит ли вернуть кампанию в работу или оставить её как reference.")
	case metrics.Spend >= 2000 && metrics.Orders == 0:
		return "waste", stringPtr(fmt.Sprintf("Кампания потратила %d ₽ без заказов за период.", metrics.Spend)), stringPtr("Откройте проблемные запросы и сократите неэффективный расход.")
	case metrics.Impressions >= 1000 && metrics.Clicks == 0:
		return "low_ctr", stringPtr("Кампания получает показы, но почти не вовлекает трафик."), stringPtr("Проверьте тип кампании, запросы и креативную релевантность карточки.")
	case metrics.Orders >= 3 && metrics.Revenue > metrics.Spend*5:
		return "growth_candidate", stringPtr("Кампания даёт заказы и рекламная выручка выше расхода, но маржа товаров не подтверждена в этом read-model."), stringPtr("Проверьте unit economics и остатки связанных товаров перед повышением ставок.")
	case productsCount == 0 || queriesCount == 0:
		return "partial", stringPtr("Кампанийный read-model пока не полностью связан с товарами или запросами."), stringPtr("Используйте Jobs как источник доверия к данным и дождитесь полного sync.")
	default:
		return "monitor", stringPtr("Кампания работает в штатном режиме и требует регулярного мониторинга."), stringPtr("Смотрите на сильные и расходные запросы за выбранный период.")
	}
}

func classifyQuerySignal(phrase domain.Phrase, metrics domain.AdsMetricsSummary) (string, string, *string, *string) {
	switch {
	case metrics.DataMode == "unavailable":
		return "insufficient_data", "insufficient_data", stringPtr("По запросу пока нет строк статистики за выбранный период."), stringPtr("Проверьте последний sync normquery stats и не принимайте авто-решения по этой фразе.")
	case metrics.Orders >= 2 && metrics.Revenue > 0 && metrics.Spend > 0 && metrics.DRR > 0 && metrics.DRR <= 20:
		return "seo_idea", "growing", stringPtr("Запрос даёт заказы с низким ДРР и подходит как SEO-идея для карточки."), stringPtr("Создайте задачу добавить запрос в SEO карточки и аккуратно масштабируйте ставку при наличии маржи.")
	case metrics.Orders > 0 && metrics.Revenue > 0 && metrics.Spend > 0 && metrics.DRR > 0 && metrics.DRR <= 35:
		return "winner", "growing", stringPtr("Запрос даёт заказы и укладывается в безопасный рекламный ДРР по данным WB."), stringPtr("Удерживайте запрос в приоритете; повышайте ставку только после проверки остатков и экономики.")
	case metrics.Spend >= 500 && metrics.Clicks >= 5 && metrics.Orders == 0 && metrics.Atbs == 0:
		return "trash", "waste", stringPtr("Запрос тратит бюджет, но не даёт корзин и заказов."), stringPtr("Проверьте релевантность и предложите минус-фразу после подтверждения менеджером.")
	// Carts but no orders is an offer/price signal (not ad waste), so it must be
	// handled before any spend-based "loser" bucket — even at high spend the right
	// action is to fix the offer, not to cut the bid or minus the phrase.
	case metrics.Clicks >= 5 && metrics.Atbs > 0 && metrics.Orders == 0:
		return "watch", "monitor", stringPtr("Запрос даёт корзины, но заказов пока нет."), stringPtr("Не отключайте резко; проверьте цену, доставку, отзывы и дождитесь следующего периода.")
	case metrics.Spend >= 500 && metrics.Clicks >= 5 && metrics.CTR < 0.01:
		return "loser", "waste", stringPtr("Запрос уже тратит бюджет, но даёт слабое вовлечение."), stringPtr("Снизьте ставку или уберите запрос из активного приоритета.")
	case metrics.Impressions >= 200 && metrics.Clicks == 0:
		return "loser", "low_ctr", stringPtr("Запрос получает показы без кликов."), stringPtr("Проверьте релевантность карточки и не держите ставку выше необходимой.")
	case metrics.Clicks >= 10 && metrics.CTR >= 0.03:
		return "watch", "monitor", stringPtr("Запрос стабильно собирает клики и выглядит перспективно, но еще не доказал заказы."), stringPtr("Оставьте запрос в наблюдении и проверьте корзины/заказы в следующем периоде.")
	case metrics.Impressions < 200 && metrics.Clicks < 5 && metrics.Orders == 0:
		return "insufficient_data", "insufficient_data", stringPtr("По запросу пока мало рекламной статистики для резкого решения."), stringPtr("Продолжите тест с лимитом и дождитесь большего объёма показов/кликов.")
	case phrase.Count != nil && *phrase.Count >= 200:
		return "high_volume", "monitor", stringPtr("Это объёмный запросный кластер, за которым стоит следить отдельно."), stringPtr("Сверяйте CTR и расход, чтобы не сливать бюджет на объёме.")
	default:
		return "monitor", "monitor", stringPtr("Запрос пока не даёт сильного сигнала в одну сторону."), stringPtr("Оставьте запрос в наблюдении и сравните следующий период.")
	}
}

func freshnessStateFromSync(sync *domain.SellerCabinetAutoSyncSummary) string {
	if sync == nil || sync.FinishedAt == nil {
		return "unknown"
	}
	return freshnessState(*sync.FinishedAt)
}

func freshnessState(finishedAt time.Time) string {
	age := time.Since(finishedAt)
	switch {
	case age <= 24*time.Hour:
		return "fresh"
	case age <= 72*time.Hour:
		return "aging"
	default:
		return "stale"
	}
}

func countActiveCampaigns(items []domain.CampaignPerformanceSummary) int {
	count := 0
	for _, item := range items {
		if item.Status == "active" {
			count++
		}
	}
	return count
}

func countProductDecisions(items []domain.ProductAdsSummary) map[string]int {
	if len(items) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, item := range items {
		decision := strings.TrimSpace(item.Decision.Decision)
		if decision == "" {
			decision = "unknown"
		}
		counts[decision]++
	}
	return counts
}

func attentionSeverityRank(value string) int {
	switch value {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func cabinetCampaignKey(cabinetID uuid.UUID, wbCampaignID int64) string {
	return cabinetID.String() + ":" + fmt.Sprintf("%d", wbCampaignID)
}

func cabinetProductKey(cabinetID uuid.UUID, wbProductID int64) string {
	return cabinetID.String() + ":" + fmt.Sprintf("%d", wbProductID)
}

func workspaceUUID(workspaceID uuid.UUID) pgtype.UUID {
	return uuidToPgtype(workspaceID)
}

func stringPtr(value string) *string {
	return &value
}

func (s *AdsReadService) latestWorkspaceAutoSync(ctx context.Context, workspaceID uuid.UUID) *domain.SellerCabinetAutoSyncSummary {
	rows, err := s.queries.ListJobRunsByWorkspace(ctx, sqlcgen.ListJobRunsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          1,
		Offset:         0,
		TaskTypeFilter: textToPgtype("wb:sync_workspace"),
	})
	if err != nil || len(rows) == 0 {
		return nil
	}

	row := rows[0]
	summary := sellerCabinetAutoSyncSummaryFromJobRun(row)
	summary.FreshnessState = "unknown"
	if row.FinishedAt.Valid {
		finishedAt := row.FinishedAt.Time
		summary.FinishedAt = &finishedAt
		summary.FreshnessState = freshnessState(finishedAt)
	}
	return summary
}

type adsDecryptedCabinet struct {
	cabinet domain.SellerCabinet
	token   string
}

func (s *AdsReadService) listWorkspaceCabinets(ctx context.Context, workspaceID uuid.UUID) ([]adsDecryptedCabinet, error) {
	rows, err := s.queries.ListActiveSellerCabinetsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list active seller cabinets")
	}

	result := make([]adsDecryptedCabinet, 0, len(rows))
	for _, row := range rows {
		cabinet := sellerCabinetFromSqlc(row)
		token, decryptErr := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
		if decryptErr != nil {
			s.logger.Warn().
				Err(decryptErr).
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", cabinet.ID.String()).
				Msg("skipping seller cabinet with undecryptable token in ads read")
			continue
		}
		result = append(result, adsDecryptedCabinet{
			cabinet: cabinet,
			token:   token,
		})
	}
	return result, nil
}
