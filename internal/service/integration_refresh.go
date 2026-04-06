package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// IntegrationRefreshService refreshes Sellico integrations (WB cabinets) using cached tokens.
// Used by the background worker to auto-discover new WB integrations added in Sellico frontend.
type IntegrationRefreshService struct {
	queries       *sqlcgen.Queries
	sellicoClient *sellico.Client
	cabinetSvc    *SellerCabinetService
	encryptionKey []byte
	logger        zerolog.Logger
}

func NewIntegrationRefreshService(
	queries *sqlcgen.Queries,
	sellicoClient *sellico.Client,
	cabinetSvc *SellerCabinetService,
	encryptionKey []byte,
	logger zerolog.Logger,
) *IntegrationRefreshService {
	return &IntegrationRefreshService{
		queries:       queries,
		sellicoClient: sellicoClient,
		cabinetSvc:    cabinetSvc,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "integration_refresh").Logger(),
	}
}

// RefreshAllWorkspaces refreshes Sellico integrations for all workspaces with cached tokens.
// Called by the worker sweep before data sync.
func (s *IntegrationRefreshService) RefreshAllWorkspaces(ctx context.Context) error {
	workspaces, err := s.queries.ListWorkspacesWithSellicoToken(ctx, 500)
	if err != nil {
		return fmt.Errorf("list workspaces with sellico token: %w", err)
	}

	refreshed := 0
	skipped := 0
	failed := 0

	for _, ws := range workspaces {
		// Skip tokens older than 24h — likely expired
		if ws.TokenUpdatedAt.Valid && time.Since(ws.TokenUpdatedAt.Time) > 24*time.Hour {
			skipped++
			continue
		}

		if !ws.EncryptedToken.Valid || ws.EncryptedToken.String == "" {
			skipped++
			continue
		}

		token, decryptErr := crypto.Decrypt(ws.EncryptedToken.String, s.encryptionKey)
		if decryptErr != nil {
			s.logger.Warn().
				Str("workspace_id", uuidFromPgtype(ws.WorkspaceID).String()).
				Msg("failed to decrypt cached sellico token")
			failed++
			continue
		}

		workspaceID := uuidFromPgtype(ws.WorkspaceID)
		workspaceRef := ""
		if ws.ExternalWorkspaceID.Valid {
			workspaceRef = ws.ExternalWorkspaceID.String
		}

		if err := s.refreshWorkspaceIntegrations(ctx, workspaceID, token, workspaceRef); err != nil {
			s.logger.Warn().
				Err(err).
				Str("workspace_id", workspaceID.String()).
				Msg("failed to refresh sellico integrations")
			failed++
			continue
		}
		refreshed++
	}

	s.logger.Info().
		Int("refreshed", refreshed).
		Int("skipped", skipped).
		Int("failed", failed).
		Int("total", len(workspaces)).
		Msg("integration refresh complete")

	return nil
}

// refreshWorkspaceIntegrations fetches integrations from Sellico and upserts local cabinets.
func (s *IntegrationRefreshService) refreshWorkspaceIntegrations(ctx context.Context, workspaceID uuid.UUID, token, workspaceRef string) error {
	integrations, err := s.sellicoClient.ListWorkspaceIntegrations(ctx, token, workspaceRef)
	if err != nil {
		return fmt.Errorf("list sellico integrations: %w", err)
	}

	for _, integration := range integrations {
		if integration.Type != "WildBerries" || integration.APIKey == "" {
			continue
		}

		encrypted, err := crypto.Encrypt(integration.APIKey, s.encryptionKey)
		if err != nil {
			continue
		}

		if _, err := s.queries.UpsertSellicoSellerCabinet(ctx, sqlcgen.UpsertSellicoSellerCabinetParams{
			WorkspaceID:            uuidToPgtype(workspaceID),
			Name:                   integration.Name,
			EncryptedToken:         encrypted,
			ExternalIntegrationID:  textToPgtype(integration.ID),
		}); err != nil {
			s.logger.Warn().
				Err(err).
				Str("integration_id", integration.ID).
				Msg("failed to upsert sellico cabinet")
		}
	}

	return nil
}
