package service

import (
	"context"
	"fmt"
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
	case metrics.Spend >= 3000 && metrics.Orders == 0:
		return "waste", stringPtr(fmt.Sprintf("Товар потратил %d ₽ без заказов за выбранный период.", metrics.Spend)), stringPtr("Откройте связанные кампании и сократите расходные запросы.")
	case metrics.Impressions >= 1000 && metrics.Clicks == 0:
		return "low_ctr", stringPtr("Товар получает показы, но не собирает клики."), stringPtr("Проверьте релевантность запросов, фото и заголовок карточки.")
	case metrics.Orders >= 3 && metrics.Revenue > metrics.Spend*5:
		return "growing", stringPtr("Товар даёт заказы и окупает рекламный расход."), stringPtr("Масштабируйте сильные кампании и следите за маржинальностью.")
	case queriesCount == 0 && metrics.Spend > 0:
		return "partial", stringPtr("По товару есть расход, но запросный слой ещё не доехал полностью."), stringPtr("Откройте Jobs и проверьте полноту sync по normquery и phrase stats.")
	default:
		return "monitor", stringPtr("Товар участвует в рекламе и требует регулярного мониторинга."), stringPtr("Следите за расходом, заказами и сильными запросами.")
	}
}

func classifyCampaignHealth(metrics domain.AdsMetricsSummary, productsCount, queriesCount int, status string) (string, *string, *string) {
	switch {
	case status == "paused" && metrics.Spend > 0:
		return "stale", stringPtr("Кампания уже не активна, но остаётся важной в историческом анализе."), stringPtr("Решите, стоит ли вернуть кампанию в работу или оставить её как reference.")
	case metrics.Spend >= 2000 && metrics.Orders == 0:
		return "waste", stringPtr(fmt.Sprintf("Кампания потратила %d ₽ без заказов за период.", metrics.Spend)), stringPtr("Откройте проблемные запросы и сократите неэффективный расход.")
	case metrics.Impressions >= 1000 && metrics.Clicks == 0:
		return "low_ctr", stringPtr("Кампания получает показы, но почти не вовлекает трафик."), stringPtr("Проверьте тип кампании, запросы и креативную релевантность карточки.")
	case metrics.Orders >= 3 && metrics.Revenue > metrics.Spend*5:
		return "growing", stringPtr("Кампания даёт заказы и хорошую отдачу на рекламный расход."), stringPtr("Масштабируйте сильные запросы без потери marginal ROAS.")
	case productsCount == 0 || queriesCount == 0:
		return "partial", stringPtr("Кампанийный read-model пока не полностью связан с товарами или запросами."), stringPtr("Используйте Jobs как источник доверия к данным и дождитесь полного sync.")
	default:
		return "monitor", stringPtr("Кампания работает в штатном режиме и требует регулярного мониторинга."), stringPtr("Смотрите на сильные и расходные запросы за выбранный период.")
	}
}

func classifyQuerySignal(phrase domain.Phrase, metrics domain.AdsMetricsSummary) (string, string, *string, *string) {
	switch {
	case metrics.Spend >= 500 && metrics.Clicks >= 5 && metrics.CTR < 0.01:
		return "waste", "waste", stringPtr("Запрос уже тратит бюджет, но даёт слабое вовлечение."), stringPtr("Снизьте ставку или уберите запрос из активного приоритета.")
	case metrics.Impressions >= 200 && metrics.Clicks == 0:
		return "waste", "low_ctr", stringPtr("Запрос получает показы без кликов."), stringPtr("Проверьте релевантность карточки и не держите ставку выше необходимой.")
	case metrics.Clicks >= 10 && metrics.CTR >= 0.03:
		return "promising", "growing", stringPtr("Запрос стабильно собирает клики и выглядит перспективно."), stringPtr("Проверьте кампанию и усиливайте сильный спрос аккуратно.")
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
	metadata := decodeJobRunMetadata(row.Metadata)
	resultState := metadataString(metadata, "result_state")
	summary := &domain.SellerCabinetAutoSyncSummary{
		JobRunID:       uuidFromPgtype(row.ID),
		Status:         row.Status,
		ResultState:    resultState,
		FreshnessState: "unknown",
		Cabinets:       metadataInt(metadata, "cabinets"),
		Campaigns:      metadataInt(metadata, "campaigns"),
		CampaignStats:  metadataInt(metadata, "campaign_stats"),
		Phrases:        metadataInt(metadata, "phrases"),
		PhraseStats:    metadataInt(metadata, "phrase_stats"),
		Products:       metadataInt(metadata, "products"),
		SyncIssues:     metadataInt(metadata, "sync_issues"),
	}
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
