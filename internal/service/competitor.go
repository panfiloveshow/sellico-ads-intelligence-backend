package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// CompetitorService extracts, tracks, and compares competitors from SERP data.
type CompetitorService struct {
	queries *sqlcgen.Queries
	logger  zerolog.Logger
}

func NewCompetitorService(queries *sqlcgen.Queries, logger zerolog.Logger) *CompetitorService {
	return &CompetitorService{
		queries: queries,
		logger:  logger.With().Str("component", "competitor").Logger(),
	}
}

// ExtractFromSERP scans SERP snapshots and identifies competitors for tracked products.
func (s *CompetitorService) ExtractFromSERP(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	products, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	productNMIDs := make(map[int64]uuid.UUID)
	for _, p := range products {
		productNMIDs[p.WbProductID] = uuidFromPgtype(p.ID)
	}

	snapshots, err := s.queries.ListSERPSnapshotsByWorkspace(ctx, sqlcgen.ListSERPSnapshotsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       500,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	found := 0
	for _, snap := range snapshots {
		items, err := s.queries.ListSERPResultItemsBySnapshot(ctx, snap.ID)
		if err != nil {
			continue
		}

		// Find our products in this SERP
		ourPositions := make(map[int64]int)
		for _, item := range items {
			if _, isOurs := productNMIDs[item.WbProductID]; isOurs {
				ourPositions[item.WbProductID] = int(item.Position)
			}
		}

		// All items NOT ours are competitors
		for _, item := range items {
			if _, isOurs := productNMIDs[item.WbProductID]; isOurs {
				continue
			}

			// Find which of our products this competes with (closest position)
			for ourNMID, ourPos := range ourPositions {
				productID := productNMIDs[ourNMID]

				compPrice := int64(0)
				if item.Price.Valid {
					compPrice = item.Price.Int64
				}
				compReviews := int32(0)
				if item.ReviewsCount.Valid {
					compReviews = item.ReviewsCount.Int32
				}

				_, err := s.queries.UpsertCompetitor(ctx, sqlcgen.UpsertCompetitorParams{
					WorkspaceID:            uuidToPgtype(workspaceID),
					ProductID:              uuidToPgtype(productID),
					CompetitorNmID:         item.WbProductID,
					CompetitorTitle:        item.Title,
					CompetitorBrand:        pgtype.Text{},
					CompetitorPrice:        compPrice,
					CompetitorRating:       0,
					CompetitorReviewsCount: compReviews,
					Query:                  snap.Query,
					Region:                 snap.Region,
					LastPosition:           item.Position,
					OurPosition:            int32(ourPos),
				})
				if err != nil {
					continue
				}
				found++
			}
		}
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("competitors_found", found).
		Msg("competitors extracted from SERP")

	return found, nil
}

// ListByProduct returns competitors for a specific product.
func (s *CompetitorService) ListByProduct(ctx context.Context, productID uuid.UUID, limit, offset int32) ([]domain.Competitor, error) {
	rows, err := s.queries.ListCompetitorsByProduct(ctx, sqlcgen.ListCompetitorsByProductParams{
		ProductID: uuidToPgtype(productID),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list competitors")
	}

	result := make([]domain.Competitor, len(rows))
	for i, row := range rows {
		result[i] = competitorFromSqlc(row)
	}
	return result, nil
}

// ListByWorkspace returns all tracked competitors.
func (s *CompetitorService) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.Competitor, error) {
	rows, err := s.queries.ListCompetitorsByWorkspace(ctx, sqlcgen.ListCompetitorsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list competitors")
	}

	result := make([]domain.Competitor, len(rows))
	for i, row := range rows {
		result[i] = competitorFromSqlc(row)
	}
	return result, nil
}

// Compare returns a comparison between our product and a competitor.
func (s *CompetitorService) Compare(ctx context.Context, ourProduct domain.Product, competitor domain.Competitor) domain.CompetitorComparison {
	ourPrice := ptrInt64(ourProduct.Price)
	comparison := domain.CompetitorComparison{
		Competitor:    competitor,
		PriceDelta:    ourPrice - competitor.CompetitorPrice,
		RatingDelta:   0,
		ReviewsDelta:  0,
		PositionDelta: competitor.OurPosition - competitor.LastPosition,
	}

	if competitor.CompetitorPrice > 0 {
		comparison.PriceDeltaPct = float64(ourPrice-competitor.CompetitorPrice) / float64(competitor.CompetitorPrice) * 100
	}

	// Determine advantage/threat
	if comparison.PriceDelta < 0 {
		comparison.Advantage = "price"
	} else if comparison.PriceDelta > 0 {
		comparison.Threat = "price"
	}

	if comparison.PositionDelta < 0 {
		if comparison.Advantage == "" {
			comparison.Advantage = "position"
		}
	} else if comparison.PositionDelta > 0 {
		if comparison.Threat == "" {
			comparison.Threat = "position"
		}
	}

	return comparison
}

func competitorFromSqlc(row sqlcgen.CompetitorRow) domain.Competitor {
	return domain.Competitor{
		ID:                   uuidFromPgtype(row.ID),
		WorkspaceID:          uuidFromPgtype(row.WorkspaceID),
		ProductID:            uuidFromPgtype(row.ProductID),
		CompetitorNMID:       row.CompetitorNmID,
		CompetitorTitle:      row.CompetitorTitle,
		CompetitorBrand:      row.CompetitorBrand.String,
		CompetitorPrice:      row.CompetitorPrice,
		CompetitorRating:     row.CompetitorRating,
		CompetitorReviewsCount: int(row.CompetitorReviewsCount),
		Query:                row.Query,
		Region:               row.Region,
		LastPosition:         int(row.LastPosition),
		OurPosition:          int(row.OurPosition),
		FirstSeenAt:          row.FirstSeenAt.Time,
		LastSeenAt:           row.LastSeenAt.Time,
		Source:               row.Source,
		CreatedAt:            row.CreatedAt.Time,
		UpdatedAt:            row.UpdatedAt.Time,
	}
}
