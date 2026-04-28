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

// IntegrationRefreshService discovers and re-imports Sellico-managed WB
// integrations into the local seller_cabinets table.
//
// Two paths are supported:
//
//  1. Legacy user-token mode (RefreshAllWorkspaces) — iterates workspaces
//     that have a cached personal Sellico token and calls
//     ListWorkspaceIntegrations(userToken). Kept for backwards compatibility
//     with deployments that still rely on per-user tokens.
//
//  2. Service-account mode (RefreshViaServiceAccount) — uses a single
//     backend-owned token (SELLICO_API_TOKEN or login result) to call
//     GetIntegrations(serviceToken, workspaceID) for every workspace.
//     This is the recommended path — see financial-dashboard reference
//     project (rules.md) — and the only one suitable for tenants that
//     have not granted us their personal token yet.
//
// Both paths upsert via UpsertSellicoSellerCabinet (dedup by
// external_integration_id) so they are idempotent and safe to alternate.
type IntegrationRefreshService struct {
	queries       *sqlcgen.Queries
	sellicoClient *sellico.Client
	tokenManager  *sellico.ServiceTokenManager // optional; nil disables service-account path
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

// WithServiceAccount enables the service-account discovery path. Pass a
// configured manager (see sellico.NewServiceTokenManager). Without this call
// only the legacy per-user path is available.
func (s *IntegrationRefreshService) WithServiceAccount(mgr *sellico.ServiceTokenManager) *IntegrationRefreshService {
	s.tokenManager = mgr
	return s
}

// Refresh runs all configured discovery paths and is the entry point used by
// the worker. It always invokes the legacy per-user path (cheap no-op if no
// workspace has a cached token) and additionally invokes the service-account
// path when WithServiceAccount has been called.
//
// Errors from individual paths are logged but not returned to the caller —
// the worker should keep going even if one path fails (e.g. Sellico down).
func (s *IntegrationRefreshService) Refresh(ctx context.Context) error {
	if s.tokenManager != nil {
		if err := s.RefreshViaServiceAccount(ctx); err != nil {
			s.logger.Warn().Err(err).Msg("service-account refresh failed; continuing with legacy path")
		}
	}
	return s.RefreshAllWorkspaces(ctx)
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

// RefreshViaServiceAccount fetches every integration in one shot from the
// Sellico /collector/integrations endpoint (per backandrules.md: the canonical
// service-account "collector" feed), groups them by Sellico work_space_id,
// joins each group to the local workspace via external_workspace_id, and
// upserts WB-typed integrations as encrypted local seller_cabinets.
//
// One HTTP round-trip regardless of how many workspaces we manage. On 401 the
// service token is invalidated and the call retried once — covers static-token
// rotation without an ops restart.
//
// Integrations whose Sellico work_space_id has no matching local workspace
// are silently dropped (the operator hasn't linked that tenant yet).
// Integrations missing an api_key are fetched in detail via /get-integration/{id}
// — the list endpoint may strip secrets for premium-gated tenants.
func (s *IntegrationRefreshService) RefreshViaServiceAccount(ctx context.Context) error {
	if s.tokenManager == nil {
		return fmt.Errorf("integration_refresh: service-account manager not configured (call WithServiceAccount)")
	}

	token, err := s.tokenManager.Get(ctx)
	if err != nil {
		return fmt.Errorf("get service token: %w", err)
	}

	integrations, err := s.sellicoClient.CollectorIntegrations(ctx, token)
	if err == sellico.ErrUnauthorized {
		s.tokenManager.Invalidate()
		token, err = s.tokenManager.Get(ctx)
		if err != nil {
			return fmt.Errorf("re-fetch service token after 401: %w", err)
		}
		integrations, err = s.sellicoClient.CollectorIntegrations(ctx, token)
	}
	if err != nil {
		return fmt.Errorf("collector/integrations: %w", err)
	}

	// Build external_workspace_id → local UUID map so we can join each
	// integration to a known tenant in O(1).
	workspaces, err := s.queries.ListWorkspacesWithExternalID(ctx, 5000)
	if err != nil {
		return fmt.Errorf("list workspaces with external id: %w", err)
	}
	wsByExternalID := make(map[string]uuid.UUID, len(workspaces))
	for _, ws := range workspaces {
		if ws.ExternalWorkspaceID.Valid && ws.ExternalWorkspaceID.String != "" {
			wsByExternalID[ws.ExternalWorkspaceID.String] = uuidFromPgtype(ws.WorkspaceID)
		}
	}

	var upserted, unknownWorkspace, nonWB, missingKey, errors int
	for _, integration := range integrations {
		if integration.Type != "WildBerries" {
			nonWB++
			continue
		}
		localWorkspaceID, ok := wsByExternalID[integration.WorkspaceID]
		if !ok {
			unknownWorkspace++
			continue
		}
		apiKey := integration.APIKey
		if apiKey == "" {
			full, err := s.sellicoClient.GetIntegration(ctx, token, integration.ID)
			if err != nil {
				s.logger.Warn().Err(err).Str("integration_id", integration.ID).Msg("failed to fetch full integration")
				errors++
				continue
			}
			apiKey = full.APIKey
		}
		if apiKey == "" {
			missingKey++
			continue
		}
		encrypted, err := crypto.Encrypt(apiKey, s.encryptionKey)
		if err != nil {
			s.logger.Warn().Err(err).Str("integration_id", integration.ID).Msg("failed to encrypt api_key")
			errors++
			continue
		}
		if _, err := s.queries.UpsertSellicoSellerCabinet(ctx, sqlcgen.UpsertSellicoSellerCabinetParams{
			WorkspaceID:           uuidToPgtype(localWorkspaceID),
			Name:                  integration.Name,
			EncryptedToken:        encrypted,
			ExternalIntegrationID: textToPgtype(integration.ID),
		}); err != nil {
			s.logger.Warn().Err(err).Str("integration_id", integration.ID).Msg("upsert seller_cabinet failed")
			errors++
			continue
		}
		upserted++
	}

	s.logger.Info().
		Int("total_integrations", len(integrations)).
		Int("wb_upserted", upserted).
		Int("non_wb_skipped", nonWB).
		Int("unknown_workspace_skipped", unknownWorkspace).
		Int("missing_api_key", missingKey).
		Int("errors", errors).
		Msg("service-account integration refresh complete")
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
