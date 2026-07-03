package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	stockAlertThreshold       = 5
	phraseTrashMinSpend       = int64(500)
	phraseTrashMinClicks      = int64(20)
	phraseCardIssueMinClicks  = int64(5)
	phraseOfferIssueMinClicks = int64(5)
	phraseSEOIdeaMinOrders    = int64(2)
	phraseDisableImpressions  = int64(2000)
)

// ExtendedRecommendationEngine generates recommendations for SEO, price, content, delivery.
type ExtendedRecommendationEngine struct {
	queries         *sqlcgen.Queries
	recommendations *RecommendationService
	logger          zerolog.Logger
}

func NewExtendedRecommendationEngine(queries *sqlcgen.Queries, recommendations *RecommendationService, logger zerolog.Logger) *ExtendedRecommendationEngine {
	return &ExtendedRecommendationEngine{
		queries:         queries,
		recommendations: recommendations,
		logger:          logger.With().Str("component", "extended_recommendations").Logger(),
	}
}

// GenerateForWorkspace generates extended recommendations (SEO, price, content).
func (e *ExtendedRecommendationEngine) GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var generated []domain.Recommendation

	// 1. Phrase-level: disable ineffective phrases
	phraseRecs, err := e.generatePhraseDisableRecs(ctx, workspaceID)
	if err != nil {
		e.logger.Error().Err(err).Msg("failed to generate phrase disable recs")
	}
	generated = append(generated, phraseRecs...)

	// 2. SEO recommendations from latest analysis
	seoRecs, err := e.generateSEORecs(ctx, workspaceID)
	if err != nil {
		e.logger.Error().Err(err).Msg("failed to generate SEO recs")
	}
	generated = append(generated, seoRecs...)

	// 3. Price optimization from competitor data
	priceRecs, err := e.generatePriceRecs(ctx, workspaceID)
	if err != nil {
		e.logger.Error().Err(err).Msg("failed to generate price recs")
	}
	generated = append(generated, priceRecs...)

	// 4. Stock alerts
	stockRecs, err := e.generateStockAlerts(ctx, workspaceID)
	if err != nil {
		e.logger.Error().Err(err).Msg("failed to generate stock alerts")
	}
	generated = append(generated, stockRecs...)

	e.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("recommendations", len(generated)).
		Msg("extended recommendations generated")

	return generated, nil
}

func (e *ExtendedRecommendationEngine) generateStockAlerts(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation

	links, err := e.queries.ListCampaignProductsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	advertisedProducts := make(map[uuid.UUID]struct{}, len(links))
	for _, link := range links {
		if link.ProductID.Valid {
			advertisedProducts[uuidFromPgtype(link.ProductID)] = struct{}{}
		}
	}
	if len(advertisedProducts) == 0 {
		return nil, nil
	}

	products, err := e.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	for _, product := range products {
		productID := uuidFromPgtype(product.ID)
		if _, ok := advertisedProducts[productID]; !ok {
			continue
		}

		ev := latestProductStockEvidence(ctx, e.queries, product.ID)
		if !ev.OK {
			continue
		}

		// Presence confirmed but quantity unknown (delivery_data in stock): treat as
		// healthy instead of firing a false zero-stock alert.
		if !ev.QuantityKnown {
			continue
		}

		if ev.Stock > stockAlertThreshold {
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeStockAlert, nil, nil, nullableUUID(productID)); closeErr != nil {
				return nil, closeErr
			}
			continue
		}

		input := stockAlertRecommendationInput(workspaceID, productID, product.Title, product.WbProductID, ev.Stock, ev.Source, ev.CapturedAt)
		rec, err := e.recommendations.UpsertActive(ctx, input)
		if err == nil {
			recs = append(recs, *rec)
		}
	}

	return recs, nil
}

func stockAlertRecommendationInput(workspaceID, productID uuid.UUID, title string, wbProductID int64, stock int32, source string, capturedAt time.Time) RecommendationUpsertInput {
	severity := domain.SeverityHigh
	if stock == 0 {
		severity = domain.SeverityCritical
	}
	return RecommendationUpsertInput{
		WorkspaceID: workspaceID,
		ProductID:   nullableUUID(productID),
		Title:       "Реклама идет на товар с низким остатком",
		Description: fmt.Sprintf("Товар «%s» рекламируется, но подтвержденный остаток: %d шт. Источник: %s.", title, stock, source),
		Type:        domain.RecommendationTypeStockAlert,
		Severity:    severity,
		Confidence:  0.86,
		SourceMetrics: map[string]any{
			"product_title": title,
			"wb_product_id": wbProductID,
			"stock_total":   stock,
			"source":        source,
			"captured_at":   capturedAt.Format(time.RFC3339),
			"threshold":     stockAlertThreshold,
		},
		NextAction: strPtr("Приостановите масштабирование рекламы и пополните остатки перед повышением ставок."),
	}
}

func (e *ExtendedRecommendationEngine) generatePhraseDisableRecs(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation
	thresholds := e.loadThresholds(ctx, workspaceID)

	phrases, err := e.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	for _, phrase := range phrases {
		stat, err := e.queries.GetLatestPhraseStat(ctx, phrase.ID)
		if err != nil {
			continue
		}

		// High impressions, zero clicks for extended period -> suggest disable.
		if stat.Impressions >= phraseDisableImpressions && stat.Clicks == 0 {
			rec, err := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				PhraseID:    nullableUUID(uuidFromPgtype(phrase.ID)),
				Title:       "Неэффективная фраза — рекомендуется отключить",
				Description: fmt.Sprintf("Фраза «%s» набрала %d показов без единого клика. Рекомендуется добавить в минус-фразы.", phrase.Keyword, stat.Impressions),
				Type:        domain.RecommendationTypeDisablePhrase,
				Severity:    domain.SeverityMedium,
				Confidence:  0.82,
				SourceMetrics: map[string]any{
					"keyword":     phrase.Keyword,
					"impressions": stat.Impressions,
					"clicks":      stat.Clicks,
				},
				NextAction: strPtr("Добавьте эту фразу в минус-фразы кампании или отключите кластер."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeDisablePhrase, nil, nullableUUID(uuidFromPgtype(phrase.ID)), nil); closeErr != nil {
			return nil, closeErr
		}

		if isTrashPhraseCandidate(stat, thresholds) {
			input := trashPhraseRecommendationInput(workspaceID, phrase, stat, thresholds)
			rec, err := e.recommendations.UpsertActive(ctx, input)
			if err == nil {
				recs = append(recs, *rec)
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeAddMinusPhrase, nil, nullableUUID(uuidFromPgtype(phrase.ID)), nil); closeErr != nil {
			return nil, closeErr
		}

		if isCardConversionIssueCandidate(stat) {
			input := cardConversionIssueRecommendationInput(workspaceID, phrase, stat)
			rec, err := e.recommendations.UpsertActive(ctx, input)
			if err == nil {
				recs = append(recs, *rec)
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeCardConversionIssue, nil, nullableUUID(uuidFromPgtype(phrase.ID)), nil); closeErr != nil {
			return nil, closeErr
		}

		if isOfferConversionIssueCandidate(stat) {
			input := offerConversionIssueRecommendationInput(workspaceID, phrase, stat)
			rec, err := e.recommendations.UpsertActive(ctx, input)
			if err == nil {
				recs = append(recs, *rec)
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeOfferConversionIssue, nil, nullableUUID(uuidFromPgtype(phrase.ID)), nil); closeErr != nil {
			return nil, closeErr
		}

		if isSEOIdeaPhraseCandidate(stat, thresholds) {
			input := seoIdeaPhraseRecommendationInput(workspaceID, phrase, stat, thresholds)
			rec, err := e.recommendations.UpsertActive(ctx, input)
			if err == nil {
				recs = append(recs, *rec)
			}
		} else if closeErr := e.recommendations.CloseActive(ctx, workspaceID, domain.RecommendationTypeOptimizeSEO, nil, nullableUUID(uuidFromPgtype(phrase.ID)), nil); closeErr != nil {
			return nil, closeErr
		}
	}

	return recs, nil
}

func (e *ExtendedRecommendationEngine) loadThresholds(ctx context.Context, workspaceID uuid.UUID) domain.RecommendationThresholds {
	raw, err := e.queries.GetWorkspaceSettings(ctx, uuidToPgtype(workspaceID))
	if err != nil || len(raw) == 0 || string(raw) == "{}" {
		return domain.DefaultThresholds()
	}

	var settings domain.WorkspaceSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return domain.DefaultThresholds()
	}
	return settings.RecommendationThresholds.Merged()
}

func isTrashPhraseCandidate(stat sqlcgen.PhraseStat, thresholds domain.RecommendationThresholds) bool {
	atbs := nullableInt64(stat.Atbs)
	orders := nullableInt64(stat.Orders)
	minSpend := minPhraseMinusSpend(thresholds)
	return stat.Spend >= minSpend &&
		stat.Clicks >= phraseTrashMinClicks &&
		(atbs == nil || *atbs == 0) &&
		(orders == nil || *orders == 0)
}

func minPhraseMinusSpend(thresholds domain.RecommendationThresholds) int64 {
	targetCPO := thresholds.Merged().CampaignPoorCPO
	minSpend := int64(targetCPO * 1.5)
	if minSpend < phraseTrashMinSpend {
		return phraseTrashMinSpend
	}
	return minSpend
}

func isCardConversionIssueCandidate(stat sqlcgen.PhraseStat) bool {
	atbs := nullableInt64(stat.Atbs)
	orders := nullableInt64(stat.Orders)
	return stat.Clicks >= phraseCardIssueMinClicks &&
		(atbs == nil || *atbs == 0) &&
		(orders == nil || *orders == 0)
}

func isOfferConversionIssueCandidate(stat sqlcgen.PhraseStat) bool {
	atbs := nullableInt64(stat.Atbs)
	orders := nullableInt64(stat.Orders)
	return stat.Clicks >= phraseOfferIssueMinClicks &&
		atbs != nil && *atbs > 0 &&
		(orders == nil || *orders == 0)
}

func isSEOIdeaPhraseCandidate(stat sqlcgen.PhraseStat, thresholds domain.RecommendationThresholds) bool {
	orders := nullableInt64(stat.Orders)
	if orders == nil || *orders < phraseSEOIdeaMinOrders || stat.Spend <= 0 {
		return false
	}
	targetCPO := thresholds.Merged().CampaignPoorCPO
	if targetCPO <= 0 {
		return false
	}
	cpo := float64(stat.Spend) / float64(*orders)
	return cpo <= targetCPO
}

func trashPhraseRecommendationInput(workspaceID uuid.UUID, phrase sqlcgen.Phrase, stat sqlcgen.PhraseStat, thresholds domain.RecommendationThresholds) RecommendationUpsertInput {
	phraseID := uuidFromPgtype(phrase.ID)
	severity := domain.SeverityMedium
	minSpend := minPhraseMinusSpend(thresholds)
	targetCPO := thresholds.Merged().CampaignPoorCPO
	if stat.Spend >= minSpend*3 {
		severity = domain.SeverityHigh
	}
	return RecommendationUpsertInput{
		WorkspaceID: workspaceID,
		PhraseID:    nullableUUID(phraseID),
		Title:       "Кластер тратит бюджет без корзин и заказов",
		Description: fmt.Sprintf("Кластер «%s» потратил %d ₽, получил %d кликов, но не дал корзин и заказов. Расход выше 1.5x целевого CPO %.0f ₽; это кандидат на минус-фразу после проверки релевантности.", phrase.Keyword, stat.Spend, stat.Clicks, targetCPO),
		Type:        domain.RecommendationTypeAddMinusPhrase,
		Severity:    severity,
		Confidence:  0.84,
		SourceMetrics: map[string]any{
			"keyword":           phrase.Keyword,
			"wb_norm_query":     phrase.WbNormQuery,
			"campaign_id":       uuidFromPgtype(phrase.CampaignID),
			"product_id":        uuidToPtr(phrase.ProductID),
			"wb_product_id":     int8ToPtr(phrase.WbProductID),
			"date":              stat.Date.Time.Format("2006-01-02"),
			"spend":             stat.Spend,
			"clicks":            stat.Clicks,
			"atbs":              int64(0),
			"orders":            int64(0),
			"campaign_poor_cpo": targetCPO,
			"min_spend":         minSpend,
			"min_clicks":        phraseTrashMinClicks,
			"decision_basis":    "real phrase_stats spend/clicks/atbs/orders",
		},
		NextAction: strPtr("Проверьте релевантность запроса к товару и добавьте кластер в минус-фразы через официальный WB API."),
	}
}

func cardConversionIssueRecommendationInput(workspaceID uuid.UUID, phrase sqlcgen.Phrase, stat sqlcgen.PhraseStat) RecommendationUpsertInput {
	phraseID := uuidFromPgtype(phrase.ID)
	severity := domain.SeverityMedium
	if stat.Clicks >= phraseTrashMinClicks || stat.Spend >= phraseTrashMinSpend*2 {
		severity = domain.SeverityHigh
	}
	return RecommendationUpsertInput{
		WorkspaceID: workspaceID,
		PhraseID:    nullableUUID(phraseID),
		Title:       "Клики по кластеру не переходят в корзины",
		Description: fmt.Sprintf("Кластер «%s» получил %d кликов, но 0 корзин и 0 заказов по рекламной статистике WB. Проблема вероятнее в карточке, цене, фото, отзывах или релевантности запроса.", phrase.Keyword, stat.Clicks),
		Type:        domain.RecommendationTypeCardConversionIssue,
		Severity:    severity,
		Confidence:  0.80,
		SourceMetrics: map[string]any{
			"keyword":        phrase.Keyword,
			"wb_norm_query":  phrase.WbNormQuery,
			"campaign_id":    uuidFromPgtype(phrase.CampaignID),
			"product_id":     uuidToPtr(phrase.ProductID),
			"wb_product_id":  int8ToPtr(phrase.WbProductID),
			"date":           stat.Date.Time.Format("2006-01-02"),
			"spend":          stat.Spend,
			"clicks":         stat.Clicks,
			"atbs":           int64(0),
			"orders":         int64(0),
			"min_clicks":     phraseCardIssueMinClicks,
			"decision_basis": "real phrase_stats clicks/atbs/orders; no card metrics inferred",
		},
		NextAction: strPtr("Создайте задачу менеджеру: проверить цену, главное фото, отзывы, комплектацию, доставку и релевантность запроса; ставку снижайте мягко, а минусуйте только после достаточного порога данных."),
	}
}

func offerConversionIssueRecommendationInput(workspaceID uuid.UUID, phrase sqlcgen.Phrase, stat sqlcgen.PhraseStat) RecommendationUpsertInput {
	phraseID := uuidFromPgtype(phrase.ID)
	atbs := int64(0)
	if value := nullableInt64(stat.Atbs); value != nil {
		atbs = *value
	}
	severity := domain.SeverityMedium
	if atbs >= 5 || stat.Spend >= phraseTrashMinSpend*2 {
		severity = domain.SeverityHigh
	}
	return RecommendationUpsertInput{
		WorkspaceID: workspaceID,
		PhraseID:    nullableUUID(phraseID),
		Title:       "Корзины по кластеру не становятся заказами",
		Description: fmt.Sprintf("Кластер «%s» дал %d корзин, но 0 заказов по рекламной статистике WB. Проблема ближе к офферу: цена, доставка, остатки, варианты, комплектация или доверие к карточке.", phrase.Keyword, atbs),
		Type:        domain.RecommendationTypeOfferConversionIssue,
		Severity:    severity,
		Confidence:  0.78,
		SourceMetrics: map[string]any{
			"keyword":        phrase.Keyword,
			"wb_norm_query":  phrase.WbNormQuery,
			"campaign_id":    uuidFromPgtype(phrase.CampaignID),
			"product_id":     uuidToPtr(phrase.ProductID),
			"wb_product_id":  int8ToPtr(phrase.WbProductID),
			"date":           stat.Date.Time.Format("2006-01-02"),
			"spend":          stat.Spend,
			"clicks":         stat.Clicks,
			"atbs":           atbs,
			"orders":         int64(0),
			"min_clicks":     phraseOfferIssueMinClicks,
			"decision_basis": "real phrase_stats clicks/atbs/orders; no delivery or price inferred",
		},
		NextAction: strPtr("Не режьте кластер резко: проверьте цену, доставку, остатки, варианты товара и отзывы; ставку снижайте мягко до подтверждения причины."),
	}
}

func seoIdeaPhraseRecommendationInput(workspaceID uuid.UUID, phrase sqlcgen.Phrase, stat sqlcgen.PhraseStat, thresholds domain.RecommendationThresholds) RecommendationUpsertInput {
	phraseID := uuidFromPgtype(phrase.ID)
	orders := int64(0)
	if value := nullableInt64(stat.Orders); value != nil {
		orders = *value
	}
	atbs := int64(0)
	if value := nullableInt64(stat.Atbs); value != nil {
		atbs = *value
	}
	targetCPO := thresholds.Merged().CampaignPoorCPO
	cpo := 0.0
	if orders > 0 {
		cpo = float64(stat.Spend) / float64(orders)
	}
	return RecommendationUpsertInput{
		WorkspaceID: workspaceID,
		PhraseID:    nullableUUID(phraseID),
		Title:       "Рекламный кластер можно проверить для SEO карточки",
		Description: fmt.Sprintf("Кластер «%s» дал %d заказов при CPO %.0f ₽ по рекламной статистике WB. Это реальный коммерческий сигнал для SEO-задачи, но маржинальность нужно проверить отдельно.", phrase.Keyword, orders, cpo),
		Type:        domain.RecommendationTypeOptimizeSEO,
		Severity:    domain.SeverityMedium,
		Confidence:  0.78,
		SourceMetrics: map[string]any{
			"keyword":        phrase.Keyword,
			"wb_norm_query":  phrase.WbNormQuery,
			"campaign_id":    uuidFromPgtype(phrase.CampaignID),
			"product_id":     uuidToPtr(phrase.ProductID),
			"wb_product_id":  int8ToPtr(phrase.WbProductID),
			"date":           stat.Date.Time.Format("2006-01-02"),
			"spend":          stat.Spend,
			"clicks":         stat.Clicks,
			"atbs":           atbs,
			"orders":         orders,
			"cpo":            cpo,
			"target_cpo":     targetCPO,
			"min_orders":     phraseSEOIdeaMinOrders,
			"decision_basis": "real phrase_stats spend/orders CPO; no margin or revenue inferred",
		},
		NextAction: strPtr("Создайте задачу добавить запрос в SEO карточки или контент-план после проверки маржи и релевантности."),
	}
}

func (e *ExtendedRecommendationEngine) generateSEORecs(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation

	products, err := e.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       200,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	for _, product := range products {
		analysis, err := e.queries.GetLatestSEOAnalysis(ctx, product.ID)
		if err != nil {
			continue
		}

		// Low SEO score → recommend improvement
		if analysis.OverallScore < 50 {
			productID := uuidFromPgtype(product.ID)
			rec, err := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(productID),
				Title:       "Карточка товара требует SEO-оптимизации",
				Description: fmt.Sprintf("SEO-оценка товара «%s»: %d/100. Заголовок: %d, ключевые слова: %d.", product.Title, analysis.OverallScore, analysis.TitleScore, analysis.KeywordsScore),
				Type:        domain.RecommendationTypeOptimizeSEO,
				Severity:    domain.SeverityMedium,
				Confidence:  0.75,
				SourceMetrics: map[string]any{
					"product_title":     product.Title,
					"overall_score":     analysis.OverallScore,
					"title_score":       analysis.TitleScore,
					"description_score": analysis.DescriptionScore,
					"keywords_score":    analysis.KeywordsScore,
				},
				NextAction: strPtr("Откройте SEO-анализ товара и примените рекомендации по заголовку и ключевым словам."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		}

		// Short title
		if analysis.TitleScore < 40 {
			productID := uuidFromPgtype(product.ID)
			rec, err := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(productID),
				Title:       "Заголовок товара слабый — улучшите для роста CTR",
				Description: fmt.Sprintf("Заголовок «%s» набрал %d/100 баллов. Добавьте ключевые слова и увеличьте длину.", product.Title, analysis.TitleScore),
				Type:        domain.RecommendationTypeImproveTitle,
				Severity:    domain.SeverityHigh,
				Confidence:  0.80,
				SourceMetrics: map[string]any{
					"product_title": product.Title,
					"title_score":   analysis.TitleScore,
				},
				NextAction: strPtr("Добавьте популярные ключевые слова в заголовок. Оптимальная длина: 60-120 символов."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		}
	}

	return recs, nil
}

func (e *ExtendedRecommendationEngine) generatePriceRecs(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation

	// Get competitors with our products
	competitors, err := e.queries.ListCompetitorsByWorkspace(ctx, sqlcgen.ListCompetitorsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       500,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	// Group by our product
	type priceSignal struct {
		competitorPrice int64
		competitorTitle string
		ourPosition     int32
		theirPosition   int32
	}
	productSignals := make(map[uuid.UUID][]priceSignal)
	for _, comp := range competitors {
		if comp.CompetitorPrice <= 0 {
			continue
		}
		productID := uuidFromPgtype(comp.ProductID)
		productSignals[productID] = append(productSignals[productID], priceSignal{
			competitorPrice: comp.CompetitorPrice,
			competitorTitle: comp.CompetitorTitle,
			ourPosition:     comp.OurPosition,
			theirPosition:   comp.LastPosition,
		})
	}

	for productID, signals := range productSignals {
		if len(signals) < 3 {
			continue
		}

		product, err := e.queries.GetProductByID(ctx, uuidToPgtype(productID))
		if err != nil {
			continue
		}

		ourPrice := int64(0)
		if product.Price.Valid {
			ourPrice = product.Price.Int64
		}
		if ourPrice == 0 {
			continue
		}

		// Count how many competitors are cheaper and better positioned
		cheaperAndBetter := 0
		for _, sig := range signals {
			if sig.competitorPrice < ourPrice && sig.theirPosition < sig.ourPosition {
				cheaperAndBetter++
			}
		}

		if cheaperAndBetter >= 3 {
			rec, err := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(productID),
				Title:       "Цена выше конкурентов — рассмотрите снижение",
				Description: fmt.Sprintf("У товара «%s» (%d₽) найдено %d конкурентов с более низкой ценой и лучшей позицией.", product.Title, ourPrice, cheaperAndBetter),
				Type:        domain.RecommendationTypePriceOptimization,
				Severity:    domain.SeverityMedium,
				Confidence:  0.70,
				SourceMetrics: map[string]any{
					"our_price":           ourPrice,
					"cheaper_competitors": cheaperAndBetter,
					"total_competitors":   len(signals),
				},
				NextAction: strPtr("Проанализируйте конкурентов и рассмотрите снижение цены или улучшение карточки."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		}
	}

	return recs, nil
}
