package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
)

type serviceTokenProvider interface {
	Get(ctx context.Context) (string, error)
	Invalidate()
}

type SellicoUnitEconomicsReadinessProvider struct {
	client       *sellico.Client
	tokenManager serviceTokenProvider
	path         string
	logger       zerolog.Logger
}

func NewSellicoUnitEconomicsReadinessProvider(client *sellico.Client, tokenManager serviceTokenProvider, path string, logger zerolog.Logger) *SellicoUnitEconomicsReadinessProvider {
	return &SellicoUnitEconomicsReadinessProvider{
		client:       client,
		tokenManager: tokenManager,
		path:         path,
		logger:       logger.With().Str("component", "sellico_unit_economics").Logger(),
	}
}

func (p *SellicoUnitEconomicsReadinessProvider) CheckBidIncreaseReadiness(ctx context.Context, input UnitEconomicsReadinessInput) (*UnitEconomicsReadiness, error) {
	if p == nil || p.client == nil || p.tokenManager == nil || p.path == "" {
		return nil, sellico.ErrNoServiceAccount
	}

	token, err := p.tokenManager.Get(ctx)
	if err != nil {
		return nil, err
	}

	response, err := p.client.CheckUnitEconomicsReadiness(ctx, token, p.path, sellico.UnitEconomicsReadinessRequest{
		WorkspaceID:     input.WorkspaceID.String(),
		SellerCabinetID: input.SellerCabinetID.String(),
		ProductIDs:      uuidStrings(input.ProductIDs),
		WBProductIDs:    input.WBProductIDs,
	})
	if errors.Is(err, sellico.ErrUnauthorized) {
		p.tokenManager.Invalidate()
		token, retryErr := p.tokenManager.Get(ctx)
		if retryErr != nil {
			return nil, retryErr
		}
		response, err = p.client.CheckUnitEconomicsReadiness(ctx, token, p.path, sellico.UnitEconomicsReadinessRequest{
			WorkspaceID:     input.WorkspaceID.String(),
			SellerCabinetID: input.SellerCabinetID.String(),
			ProductIDs:      uuidStrings(input.ProductIDs),
			WBProductIDs:    input.WBProductIDs,
		})
	}
	if err != nil {
		return nil, err
	}

	return &UnitEconomicsReadiness{
		IntegrationID:              response.IntegrationID,
		Source:                     response.Source,
		CheckedAt:                  response.CheckedAt,
		Complete:                   response.Complete,
		CheckedProductIDs:          response.CheckedProductIDs,
		MissingEconomicsProductIDs: response.MissingEconomicsProductIDs,
		UnprofitableProductIDs:     response.UnprofitableProductIDs,
		StaleProductIDs:            response.StaleProductIDs,
	}, nil
}

func uuidStrings(ids []uuid.UUID) []string {
	if len(ids) == 0 {
		return nil
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, id.String())
	}
	return result
}
