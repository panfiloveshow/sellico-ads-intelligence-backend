package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
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
	// Stock alerts will be generated when product snapshot data includes stock_total.
	// For now this is a placeholder — stock data needs to be collected via WB content API or catalog parser.
	return nil, nil
}

func (e *ExtendedRecommendationEngine) generatePhraseDisableRecs(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation

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

		// High impressions, zero clicks for extended period → suggest disable
		if stat.Impressions >= 2000 && stat.Clicks == 0 {
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
		}
	}

	return recs, nil
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
					"our_price":          ourPrice,
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
