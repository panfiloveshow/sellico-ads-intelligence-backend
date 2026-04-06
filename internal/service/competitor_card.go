package service

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// CompetitorCardService provides detailed competitor analysis with comparisons.
type CompetitorCardService struct {
	queries *sqlcgen.Queries
}

func NewCompetitorCardService(queries *sqlcgen.Queries) *CompetitorCardService {
	return &CompetitorCardService{queries: queries}
}

// CompetitorCard is a full competitor view with history and comparison.
type CompetitorCard struct {
	Competitor domain.Competitor            `json:"competitor"`
	Comparison domain.CompetitorComparison  `json:"comparison"`
	History    []domain.CompetitorSnapshot  `json:"history"`
	Queries    []string                     `json:"queries"` // all queries where this competitor appears
}

// GetCard returns a full competitor card with comparison and history.
func (s *CompetitorCardService) GetCard(ctx context.Context, competitorID uuid.UUID) (*CompetitorCard, error) {
	// Get competitor
	comp, err := s.queries.GetCompetitorByID(ctx, uuidToPgtype(competitorID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "competitor not found")
	}

	competitor := competitorFromSqlc(comp)

	// Get our product for comparison
	product, err := s.queries.GetProductByID(ctx, uuidToPgtype(competitor.ProductID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "our product not found")
	}

	ourProduct := productFromSqlc(product)
	comparison := compareProducts(ourProduct, competitor)

	// Get history snapshots
	historyRows, _ := s.queries.ListCompetitorSnapshots(ctx, uuidToPgtype(competitorID), 30)
	history := make([]domain.CompetitorSnapshot, len(historyRows))
	for i, row := range historyRows {
		history[i] = domain.CompetitorSnapshot{
			ID:           uuidFromPgtype(row.ID),
			CompetitorID: uuidFromPgtype(row.CompetitorID),
			Price:        row.Price,
			Rating:       row.Rating,
			ReviewsCount: int(row.ReviewsCount),
			Position:     int(row.Position),
			OurPosition:  int(row.OurPosition),
			CapturedAt:   row.CapturedAt.Time,
		}
	}

	// Get all queries where this competitor NM ID appears
	allComps, _ := s.queries.ListCompetitorsByWorkspace(ctx, sqlcgen.ListCompetitorsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(competitor.WorkspaceID),
		Limit:       1000,
		Offset:      0,
	})
	querySet := make(map[string]bool)
	for _, c := range allComps {
		if c.CompetitorNmID == competitor.CompetitorNMID {
			querySet[c.Query] = true
		}
	}
	queries := make([]string, 0, len(querySet))
	for q := range querySet {
		queries = append(queries, q)
	}
	sort.Strings(queries)

	return &CompetitorCard{
		Competitor: competitor,
		Comparison: comparison,
		History:    history,
		Queries:    queries,
	}, nil
}

// TopCompetitors returns the most frequently seen competitors across all queries.
func (s *CompetitorCardService) TopCompetitors(ctx context.Context, workspaceID uuid.UUID, limit int32) ([]domain.Competitor, error) {
	all, err := s.queries.ListCompetitorsByWorkspace(ctx, sqlcgen.ListCompetitorsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       5000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	// Count appearances per competitor NM ID
	type compScore struct {
		competitor sqlcgen.CompetitorRow
		count      int
	}
	scores := make(map[int64]*compScore)
	for _, c := range all {
		if s, ok := scores[c.CompetitorNmID]; ok {
			s.count++
		} else {
			scores[c.CompetitorNmID] = &compScore{competitor: c, count: 1}
		}
	}

	sorted := make([]*compScore, 0, len(scores))
	for _, s := range scores {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })

	result := make([]domain.Competitor, 0, limit)
	for i, s := range sorted {
		if int32(i) >= limit {
			break
		}
		result = append(result, competitorFromSqlc(s.competitor))
	}
	return result, nil
}

func compareProducts(our domain.Product, comp domain.Competitor) domain.CompetitorComparison {
	ourPrice := ptrInt64(our.Price)
	comparison := domain.CompetitorComparison{
		Competitor:    comp,
		PriceDelta:    ourPrice - comp.CompetitorPrice,
		PositionDelta: comp.OurPosition - comp.LastPosition,
	}

	if comp.CompetitorPrice > 0 {
		comparison.PriceDeltaPct = float64(ourPrice-comp.CompetitorPrice) / float64(comp.CompetitorPrice) * 100
	}

	// Determine advantages
	advantages := []string{}
	threats := []string{}

	if comparison.PriceDelta < 0 {
		advantages = append(advantages, "price")
	} else if comparison.PriceDelta > 0 {
		threats = append(threats, "price")
	}

	if comparison.PositionDelta < 0 {
		advantages = append(advantages, "position")
	} else if comparison.PositionDelta > 0 {
		threats = append(threats, "position")
	}

	if len(advantages) > 0 {
		comparison.Advantage = advantages[0]
	}
	if len(threats) > 0 {
		comparison.Threat = threats[0]
	}

	return comparison
}
