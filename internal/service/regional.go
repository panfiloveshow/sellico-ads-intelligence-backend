package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// RegionalAnalyticsService aggregates position and delivery data by region.
type RegionalAnalyticsService struct {
	queries *sqlcgen.Queries
	logger  zerolog.Logger
}

func NewRegionalAnalyticsService(queries *sqlcgen.Queries, logger zerolog.Logger) *RegionalAnalyticsService {
	return &RegionalAnalyticsService{
		queries: queries,
		logger:  logger.With().Str("component", "regional").Logger(),
	}
}

// AggregatePositionsByRegion computes average positions per product/query/region for the period.
func (s *RegionalAnalyticsService) AggregatePositionsByRegion(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	now := time.Now()
	periodStart := now.AddDate(0, 0, -7)
	periodEnd := now

	positions, err := s.queries.ListPositionsByWorkspace(ctx, sqlcgen.ListPositionsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       10000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	// Group by product+query+region
	type key struct {
		productID string
		query     string
		region    string
	}
	groups := make(map[key][]int32)
	for _, pos := range positions {
		if !pos.CheckedAt.Valid || pos.CheckedAt.Time.Before(periodStart) {
			continue
		}
		k := key{
			productID: uuidFromPgtype(pos.ProductID).String(),
			query:     pos.Query,
			region:    pos.Region,
		}
		groups[k] = append(groups[k], pos.Position)
	}

	aggregated := 0
	for k, positions := range groups {
		if len(positions) == 0 {
			continue
		}

		sum := int32(0)
		best := positions[0]
		worst := positions[0]
		for _, p := range positions {
			sum += p
			if p < best {
				best = p
			}
			if p > worst {
				worst = p
			}
		}
		avg := float64(sum) / float64(len(positions))

		productID, _ := uuid.Parse(k.productID)
		s.queries.UpsertRegionalPositionAggregate(ctx, sqlcgen.UpsertRegionalPositionAggregateParams{
			WorkspaceID:   uuidToPgtype(workspaceID),
			ProductID:     uuidToPgtype(productID),
			Query:         k.query,
			Region:        k.region,
			AvgPosition:   avg,
			BestPosition:  best,
			WorstPosition: worst,
			CheckCount:    int32(len(positions)),
			PeriodStart:   pgtype.Date{Time: periodStart, Valid: true},
			PeriodEnd:     pgtype.Date{Time: periodEnd, Valid: true},
		})
		aggregated++
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("aggregated", aggregated).
		Msg("regional position aggregation completed")

	return aggregated, nil
}
