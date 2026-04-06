package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// DeliveryService collects and analyzes delivery data for products.
type DeliveryService struct {
	queries  *sqlcgen.Queries
	wbClient *wb.Client
	logger   zerolog.Logger
}

func NewDeliveryService(queries *sqlcgen.Queries, wbClient *wb.Client, logger zerolog.Logger) *DeliveryService {
	return &DeliveryService{
		queries:  queries,
		wbClient: wbClient,
		logger:   logger.With().Str("component", "delivery").Logger(),
	}
}

// CollectForWorkspace fetches delivery info for all products in a workspace.
func (s *DeliveryService) CollectForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	products, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       200,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	collected := 0
	for _, product := range products {
		deliveryInfos, err := s.wbClient.GetProductDelivery(ctx, product.WbProductID)
		if err != nil {
			s.logger.Debug().Err(err).Int64("nm_id", product.WbProductID).Msg("failed to get delivery info")
			continue
		}

		for _, info := range deliveryInfos {
			s.queries.CreateDeliveryData(ctx, sqlcgen.CreateDeliveryDataParams{
				WorkspaceID:  uuidToPgtype(workspaceID),
				ProductID:    product.ID,
				Region:       info.Region,
				Warehouse:    textToPgtype(info.Warehouse),
				DeliveryDays: int32(info.DeliveryDays),
				DeliveryCost: 0,
				InStock:      info.InStock,
			})

			// Update product stock_total if available
			if info.StockCount >= 0 {
				s.queries.UpdateProductStock(ctx, product.ID, int32(info.StockCount))
			}
		}
		collected++
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("products_collected", collected).
		Msg("delivery data collection completed")

	return collected, nil
}

// GenerateDeliveryRecommendations creates delivery_issue recommendations.
func (s *DeliveryService) GenerateDeliveryRecommendations(ctx context.Context, workspaceID uuid.UUID, recommendations *RecommendationService) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation

	products, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       200,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	for _, product := range products {
		delivery, err := s.queries.GetLatestDeliveryData(ctx, product.ID)
		if err != nil {
			continue
		}

		productID := uuidFromPgtype(product.ID)

		// Long delivery time
		if delivery.DeliveryDays > 5 {
			rec, err := recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(productID),
				Title:       "Длительная доставка снижает конверсию",
				Description: fmt.Sprintf("Товар «%s» доставляется за %d дней. Конкуренты с доставкой 1-3 дня получают больше заказов.", product.Title, delivery.DeliveryDays),
				Type:        domain.RecommendationTypeDeliveryIssue,
				Severity:    domain.SeverityMedium,
				Confidence:  0.72,
				SourceMetrics: map[string]any{
					"product_title": product.Title,
					"delivery_days": delivery.DeliveryDays,
					"region":        delivery.Region,
				},
				NextAction: strPtr("Рассмотрите размещение товара на ближайшем к покупателям складе WB."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		}

		// Out of stock
		if !delivery.InStock {
			rec, err := recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(productID),
				Title:       "Товар отсутствует на складе",
				Description: fmt.Sprintf("Товар «%s» недоступен для заказа. Реклама расходуется впустую.", product.Title),
				Type:        domain.RecommendationTypeStockAlert,
				Severity:    domain.SeverityHigh,
				Confidence:  0.90,
				SourceMetrics: map[string]any{
					"product_title": product.Title,
					"in_stock":      false,
				},
				NextAction: strPtr("Приостановите рекламу и пополните остатки на складе."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		}
	}

	return recs, nil
}
