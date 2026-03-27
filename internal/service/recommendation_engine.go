package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	campaignHighImpressionsThreshold = 1000
	campaignZeroOrdersClickThreshold = 40
	campaignHighCPCThreshold         = 50.0
	campaignPoorCPOThreshold         = 1500.0
	phraseBidRaiseClickThreshold     = 10
	positionDropThreshold            = 10
)

// RecommendationEngine generates explainable recommendations from stored analytics.
type RecommendationEngine struct {
	queries         *sqlcgen.Queries
	recommendations *RecommendationService
	logger          zerolog.Logger
}

func NewRecommendationEngine(queries *sqlcgen.Queries, recommendations *RecommendationService, logger zerolog.Logger) *RecommendationEngine {
	return &RecommendationEngine{
		queries:         queries,
		recommendations: recommendations,
		logger:          logger,
	}
}

func (e *RecommendationEngine) GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error) {
	var generated []domain.Recommendation

	campaigns, err := e.queries.ListCampaignsByWorkspace(ctx, sqlcgen.ListCampaignsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}
	for _, campaign := range campaigns {
		stat, statErr := e.queries.GetLatestCampaignStat(ctx, campaign.ID)
		if errors.Is(statErr, pgx.ErrNoRows) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}

		if stat.Impressions >= campaignHighImpressionsThreshold && stat.Clicks == 0 {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				CampaignID:  nullableUUID(uuidFromPgtype(campaign.ID)),
				Title:       "High impressions with zero clicks",
				Description: fmt.Sprintf("Campaign %q collected %d impressions but no clicks on %s.", campaign.Name, stat.Impressions, stat.Date.Time.Format("2006-01-02")),
				Type:        domain.RecommendationTypeLowCTR,
				Severity:    domain.SeverityHigh,
				Confidence:  0.84,
				SourceMetrics: map[string]any{
					"impressions": stat.Impressions,
					"clicks":      stat.Clicks,
					"date":        stat.Date.Time.Format("2006-01-02"),
				},
				NextAction: strPtr("Review listing relevance and improve CTR before increasing spend."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
		}

		orders := nullableInt64(stat.Orders)
		if stat.Clicks >= campaignZeroOrdersClickThreshold && (orders == nil || *orders == 0) {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				CampaignID:  nullableUUID(uuidFromPgtype(campaign.ID)),
				Title:       "Clicks are not converting into orders",
				Description: fmt.Sprintf("Campaign %q generated %d clicks with zero orders on %s.", campaign.Name, stat.Clicks, stat.Date.Time.Format("2006-01-02")),
				Type:        domain.RecommendationTypeHighSpendLowOrders,
				Severity:    domain.SeverityHigh,
				Confidence:  0.81,
				SourceMetrics: map[string]any{
					"clicks": stat.Clicks,
					"orders": 0,
					"spend":  stat.Spend,
					"date":   stat.Date.Time.Format("2006-01-02"),
				},
				NextAction: strPtr("Review search terms and listing conversion quality; consider pausing weak traffic."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
		}

		if stat.Clicks > 0 {
			cpc := float64(stat.Spend) / float64(stat.Clicks)
			cpo := campaignPoorCPOThreshold + 1
			if orders != nil && *orders > 0 {
				cpo = float64(stat.Spend) / float64(*orders)
			}
			if cpc >= campaignHighCPCThreshold && cpo >= campaignPoorCPOThreshold {
				rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
					WorkspaceID: workspaceID,
					CampaignID:  nullableUUID(uuidFromPgtype(campaign.ID)),
					Title:       "Cost is high relative to outcomes",
					Description: fmt.Sprintf("Campaign %q has CPC %.2f and CPO %.2f, which indicates inefficient spend.", campaign.Name, cpc, cpo),
					Type:        domain.RecommendationTypeBidAdjustment,
					Severity:    domain.SeverityMedium,
					Confidence:  0.76,
					SourceMetrics: map[string]any{
						"cpc":    cpc,
						"cpo":    cpo,
						"clicks": stat.Clicks,
						"spend":  stat.Spend,
					},
					NextAction: strPtr("Lower bids or pause expensive placements with weak return."),
				})
				if upsertErr != nil {
					return nil, upsertErr
				}
				generated = append(generated, *rec)
			}
		}
	}

	phrases, err := e.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}
	for _, phrase := range phrases {
		stat, statErr := e.queries.GetLatestPhraseStat(ctx, phrase.ID)
		if errors.Is(statErr, pgx.ErrNoRows) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}

		bid, bidErr := e.queries.GetLatestBidSnapshot(ctx, phrase.ID)
		if errors.Is(bidErr, pgx.ErrNoRows) {
			continue
		}
		if bidErr != nil {
			return nil, bidErr
		}

		currentBid := int64(0)
		if phrase.CurrentBid.Valid {
			currentBid = phrase.CurrentBid.Int64
		}
		if stat.Clicks >= phraseBidRaiseClickThreshold && bid.CompetitiveBid > currentBid {
			rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				PhraseID:    nullableUUID(uuidFromPgtype(phrase.ID)),
				Title:       "Competitive bid is above the current phrase bid",
				Description: fmt.Sprintf("Phrase %q is producing clicks, but the competitive bid (%d) is higher than the current bid (%d).", phrase.Keyword, bid.CompetitiveBid, currentBid),
				Type:        domain.RecommendationTypeBidAdjustment,
				Severity:    domain.SeverityMedium,
				Confidence:  0.72,
				SourceMetrics: map[string]any{
					"keyword":         phrase.Keyword,
					"clicks":          stat.Clicks,
					"current_bid":     currentBid,
					"competitive_bid": bid.CompetitiveBid,
					"leadership_bid":  bid.LeadershipBid,
				},
				NextAction: strPtr("Increase the phrase bid toward the competitive range if this traffic remains profitable."),
			})
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *rec)
		}
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
		positions, posErr := e.queries.ListPositionsByProduct(ctx, sqlcgen.ListPositionsByProductParams{
			ProductID: product.ID,
			Limit:     50,
			Offset:    0,
		})
		if posErr != nil {
			return nil, posErr
		}
		if len(positions) < 2 {
			continue
		}

		latest := positions[0]
		for _, previous := range positions[1:] {
			if previous.Query != latest.Query || previous.Region != latest.Region {
				continue
			}
			delta := int(latest.Position - previous.Position)
			if delta >= positionDropThreshold {
				severity := domain.SeverityMedium
				if delta >= 20 {
					severity = domain.SeverityHigh
				}
				rec, upsertErr := e.recommendations.UpsertActive(ctx, RecommendationUpsertInput{
					WorkspaceID: workspaceID,
					ProductID:   nullableUUID(uuidFromPgtype(product.ID)),
					Title:       "Product search position dropped",
					Description: fmt.Sprintf("Product %q dropped from position %d to %d for query %q in region %q.", product.Title, previous.Position, latest.Position, latest.Query, latest.Region),
					Type:        domain.RecommendationTypePositionDrop,
					Severity:    severity,
					Confidence:  0.78,
					SourceMetrics: map[string]any{
						"previous_position": previous.Position,
						"current_position":  latest.Position,
						"query":             latest.Query,
						"region":            latest.Region,
					},
					NextAction: strPtr("Check delivery, competition, and bid pressure for the affected search context."),
				})
				if upsertErr != nil {
					return nil, upsertErr
				}
				generated = append(generated, *rec)
			}
			break
		}
	}

	e.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("recommendations", len(generated)).
		Msg("generated recommendations")

	return generated, nil
}

func strPtr(v string) *string {
	return &v
}
