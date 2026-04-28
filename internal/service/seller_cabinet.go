package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// WBTokenValidator validates a Wildberries API token by making a test request.
type WBTokenValidator interface {
	ValidateToken(ctx context.Context, token string) error
}

// SellerCabinetService handles seller cabinet management.
type SellerCabinetService struct {
	queries        *sqlcgen.Queries
	encryptionKey  []byte
	tokenValidator WBTokenValidator
	sellicoClient  *sellico.Client
}

type SellerCabinetListFilter struct {
	Status string
}

// NewSellerCabinetService creates a new SellerCabinetService.
func NewSellerCabinetService(queries *sqlcgen.Queries, encryptionKey []byte, tokenValidator WBTokenValidator, sellicoClient *sellico.Client) *SellerCabinetService {
	return &SellerCabinetService{
		queries:        queries,
		encryptionKey:  encryptionKey,
		tokenValidator: tokenValidator,
		sellicoClient:  sellicoClient,
	}
}

// Create encrypts the API token, validates it against WB API, and saves the seller cabinet.
func (s *SellerCabinetService) Create(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error) {
	// Encrypt the API token with AES-256-GCM.
	encryptedToken, err := crypto.Encrypt(apiToken, s.encryptionKey)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to encrypt API token")
	}

	// Validate the token by making a test request to WB API.
	if err := s.tokenValidator.ValidateToken(ctx, apiToken); err != nil {
		// Surface WB's actual error to the caller — generic "validation failed"
		// makes diagnosing token issues (expired, missing scope, network) impossible
		// from the outside. The underlying err already wraps WB status + URL.
		return nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("WB API token validation failed: %v", err))
	}

	// Save to DB.
	sc, err := s.queries.CreateSellerCabinet(ctx, sqlcgen.CreateSellerCabinetParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Name:           name,
		EncryptedToken: encryptedToken,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create seller cabinet")
	}

	result := sellerCabinetFromSqlc(sc)
	return &result, nil
}

// List returns seller cabinets for the workspace. When the Sellico client is
// configured, it pulls the upstream WildBerries integration list (the canonical
// source under SSO mode). When it's nil (local-auth deployments), it falls
// back to the local seller_cabinets table only.
func (s *SellerCabinetService) List(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, filter SellerCabinetListFilter, limit, offset int32) ([]domain.SellerCabinet, error) {
	if s.sellicoClient == nil {
		return s.listLocalCabinets(ctx, workspaceID, filter, limit, offset)
	}
	integrations, err := s.listWbIntegrations(ctx, token, workspaceRef, workspaceID)
	if err != nil {
		return nil, err
	}

	lastAutoSync, err := s.latestWorkspaceAutoSync(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	result := make([]domain.SellerCabinet, 0, len(integrations))
	for _, integration := range integrations {
		cabinet, syncErr := s.ensureSellicoCabinet(ctx, workspaceID, integration)
		if syncErr != nil {
			return nil, syncErr
		}
		cabinet.LastAutoSync = lastAutoSync
		if filter.Status != "" && cabinet.Status != filter.Status {
			continue
		}
		result = append(result, *cabinet)
	}

	return paginateCabinets(result, limit, offset), nil
}

// Get resolves a seller cabinet by public ref (Sellico integration id or local UUID).
func (s *SellerCabinetService) Get(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error) {
	cabinet, err := s.resolveCabinet(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return nil, err
	}
	lastAutoSync, syncErr := s.latestWorkspaceAutoSync(ctx, workspaceID)
	if syncErr != nil {
		return nil, syncErr
	}
	cabinet.LastAutoSync = lastAutoSync
	return cabinet, nil
}

// ListCampaigns returns campaigns for a seller cabinet after verifying workspace ownership.
func (s *SellerCabinetService) ListCampaigns(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Campaign, error) {
	cabinet, err := s.resolveCabinet(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list seller cabinet campaigns")
	}

	result := make([]domain.Campaign, len(rows))
	for i, row := range rows {
		result[i] = campaignFromSqlc(row)
	}
	return result, nil
}

// ListProducts returns products for a seller cabinet after verifying workspace ownership.
func (s *SellerCabinetService) ListProducts(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Product, error) {
	cabinet, err := s.resolveCabinet(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListProductsBySellerCabinet(ctx, sqlcgen.ListProductsBySellerCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list seller cabinet products")
	}

	result := make([]domain.Product, len(rows))
	for i, row := range rows {
		result[i] = productFromSqlc(row)
	}
	return result, nil
}

// Delete performs a soft delete on the seller cabinet after verifying workspace ownership.
func (s *SellerCabinetService) Delete(ctx context.Context, actorID uuid.UUID, _ string, _ string, workspaceID uuid.UUID, cabinetRef string) error {
	cabinet, err := s.resolveCabinet(ctx, "", "", workspaceID, cabinetRef)
	if err != nil {
		return err
	}
	if cabinet.Source == "sellico" {
		return apperror.New(apperror.ErrValidation, "seller cabinet is managed by Sellico integrations")
	}

	// Soft delete.
	if err := s.queries.SoftDeleteSellerCabinet(ctx, uuidToPgtype(cabinet.ID)); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to delete seller cabinet")
	}

	// Audit log the deletion.
	meta, _ := json.Marshal(map[string]string{
		"cabinet_name": cabinet.Name,
	})
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "delete_seller_cabinet",
		EntityType:  "seller_cabinet",
		EntityID:    uuidToPgtype(cabinet.ID),
		Metadata:    meta,
	})

	return nil
}

func (s *SellerCabinetService) ResolveCabinetID(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (uuid.UUID, error) {
	cabinet, err := s.resolveCabinet(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return uuid.Nil, err
	}
	return cabinet.ID, nil
}

// --- sqlc → domain mapper ---

func sellerCabinetFromSqlc(sc sqlcgen.SellerCabinet) domain.SellerCabinet {
	result := domain.SellerCabinet{
		ID:             uuidFromPgtype(sc.ID),
		WorkspaceID:    uuidFromPgtype(sc.WorkspaceID),
		Name:           sc.Name,
		EncryptedToken: sc.EncryptedToken,
		Status:         sc.Status,
		Source:         sc.Source,
		CreatedAt:      sc.CreatedAt.Time,
		UpdatedAt:      sc.UpdatedAt.Time,
	}
	if sc.ExternalIntegrationID.Valid {
		value := sc.ExternalIntegrationID.String
		result.ExternalIntegrationID = &value
	}
	if sc.IntegrationType.Valid {
		value := sc.IntegrationType.String
		result.IntegrationType = &value
	}
	if sc.LastSyncedAt.Valid {
		t := sc.LastSyncedAt.Time
		result.LastSyncedAt = &t
	}
	if sc.LastSellicoSyncAt.Valid {
		t := sc.LastSellicoSyncAt.Time
		result.LastSellicoSyncAt = &t
	}
	if sc.DeletedAt.Valid {
		t := sc.DeletedAt.Time
		result.DeletedAt = &t
	}
	return result
}

func (s *SellerCabinetService) latestWorkspaceAutoSync(ctx context.Context, workspaceID uuid.UUID) (*domain.SellerCabinetAutoSyncSummary, error) {
	rows, err := s.queries.ListJobRunsByWorkspace(ctx, sqlcgen.ListJobRunsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          1,
		Offset:         0,
		TaskTypeFilter: textToPgtype("wb:sync_workspace"),
		StatusFilter:   pgtype.Text{},
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load latest workspace sync")
	}
	if len(rows) == 0 {
		return nil, nil
	}

	return sellerCabinetAutoSyncSummaryFromJobRun(rows[0]), nil
}

func sellerCabinetAutoSyncSummaryFromJobRun(jobRun sqlcgen.JobRun) *domain.SellerCabinetAutoSyncSummary {
	metadata := decodeJobRunMetadata(jobRun.Metadata)
	resultState := metadataString(metadata, "result_state")

	var finishedAt *time.Time
	if jobRun.FinishedAt.Valid {
		value := jobRun.FinishedAt.Time
		finishedAt = &value
	}

	return &domain.SellerCabinetAutoSyncSummary{
		JobRunID:      uuidFromPgtype(jobRun.ID),
		Status:        jobRun.Status,
		ResultState:   resultState,
		FinishedAt:    finishedAt,
		Cabinets:      metadataInt(metadata, "cabinets"),
		Campaigns:     metadataInt(metadata, "campaigns"),
		CampaignStats: metadataInt(metadata, "campaign_stats"),
		Phrases:       metadataInt(metadata, "phrases"),
		PhraseStats:   metadataInt(metadata, "phrase_stats"),
		Products:      metadataInt(metadata, "products"),
		SyncIssues:    metadataArrayLen(metadata, "sync_issues"),
	}
}

func decodeJobRunMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return map[string]any{}
	}
	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}
	return typed
}

func metadataInt(metadata map[string]any, key string) int {
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	default:
		return 0
	}
}

func metadataArrayLen(metadata map[string]any, key string) int {
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	items, ok := value.([]any)
	if !ok {
		return 0
	}
	return len(items)
}

func (s *SellerCabinetService) resolveCabinet(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error) {
	if cabinetID, err := uuid.Parse(cabinetRef); err == nil {
		sc, getErr := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
		if errors.Is(getErr, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
		}
		if getErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to get seller cabinet")
		}
		if uuidFromPgtype(sc.WorkspaceID) != workspaceID {
			return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
		}
		result := sellerCabinetFromSqlc(sc)
		return &result, nil
	}

	if token == "" {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}

	integration, err := s.getWbIntegration(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return nil, err
	}
	return s.ensureSellicoCabinet(ctx, workspaceID, *integration)
}

// listLocalCabinets is the no-Sellico path: read directly from seller_cabinets,
// resolve the latest auto-sync, and apply the same status filter + pagination.
func (s *SellerCabinetService) listLocalCabinets(ctx context.Context, workspaceID uuid.UUID, filter SellerCabinetListFilter, limit, offset int32) ([]domain.SellerCabinet, error) {
	rows, err := s.queries.ListSellerCabinetsByWorkspace(ctx, sqlcgen.ListSellerCabinetsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list local seller cabinets")
	}
	lastAutoSync, _ := s.latestWorkspaceAutoSync(ctx, workspaceID)
	result := make([]domain.SellerCabinet, 0, len(rows))
	for _, row := range rows {
		cabinet := sellerCabinetFromSqlc(row)
		cabinet.LastAutoSync = lastAutoSync
		if filter.Status != "" && cabinet.Status != filter.Status {
			continue
		}
		result = append(result, cabinet)
	}
	return paginateCabinets(result, limit, offset), nil
}

func (s *SellerCabinetService) listWbIntegrations(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID) ([]sellico.Integration, error) {
	if s.sellicoClient == nil {
		return nil, apperror.New(apperror.ErrInternal, "sellico client is not configured")
	}

	workspaceRefs, err := s.sellicoWorkspaceRefs(ctx, workspaceRef, workspaceID)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, ref := range workspaceRefs {
		integrations, listErr := s.sellicoClient.ListWorkspaceIntegrations(ctx, token, ref)
		if listErr != nil {
			lastErr = listErr
			continue
		}

		result := make([]sellico.Integration, 0, len(integrations))
		for _, integration := range integrations {
			if integration.Type != "WildBerries" {
				continue
			}
			if integration.APIKey == "" {
				details, detailsErr := s.sellicoClient.GetWorkspaceIntegration(ctx, token, ref, integration.ID)
				if detailsErr == nil && details != nil {
					integration = *details
				}
			}
			if strings.TrimSpace(integration.APIKey) == "" {
				continue
			}
			result = append(result, integration)
		}

		return result, nil
	}

	_ = lastErr
	return nil, apperror.New(apperror.ErrInternal, "failed to load workspace integrations from Sellico")
}

func (s *SellerCabinetService) getWbIntegration(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, integrationID string) (*sellico.Integration, error) {
	if s.sellicoClient == nil {
		return nil, apperror.New(apperror.ErrInternal, "sellico client is not configured")
	}

	workspaceRefs, err := s.sellicoWorkspaceRefs(ctx, workspaceRef, workspaceID)
	if err != nil {
		return nil, err
	}

	for _, ref := range workspaceRefs {
		integration, getErr := s.sellicoClient.GetWorkspaceIntegration(ctx, token, ref, integrationID)
		if getErr != nil {
			continue
		}
		if integration.Type != "WildBerries" || strings.TrimSpace(integration.APIKey) == "" {
			continue
		}

		return integration, nil
	}

	return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
}

func (s *SellerCabinetService) ensureSellicoCabinet(ctx context.Context, workspaceID uuid.UUID, integration sellico.Integration) (*domain.SellerCabinet, error) {
	encryptedToken, err := crypto.Encrypt(integration.APIKey, s.encryptionKey)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to encrypt API token")
	}

	row, err := s.queries.UpsertSellicoSellerCabinet(ctx, sqlcgen.UpsertSellicoSellerCabinetParams{
		WorkspaceID:           uuidToPgtype(workspaceID),
		Name:                  integration.Name,
		EncryptedToken:        encryptedToken,
		Status:                domain.StatusActive,
		ExternalIntegrationID: textToPgtype(integration.ID),
		IntegrationType:       textToPgtype(integration.Type),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to sync seller cabinet from Sellico")
	}

	result := sellerCabinetFromSqlc(row)
	return &result, nil
}

func (s *SellerCabinetService) externalWorkspaceID(ctx context.Context, workspaceID uuid.UUID) (string, error) {
	workspace, err := s.queries.GetWorkspaceByID(ctx, uuidToPgtype(workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperror.New(apperror.ErrNotFound, "workspace not found")
	}
	if err != nil {
		return "", apperror.New(apperror.ErrInternal, "failed to load workspace")
	}
	if !workspace.ExternalWorkspaceID.Valid || strings.TrimSpace(workspace.ExternalWorkspaceID.String) == "" {
		return "", apperror.New(apperror.ErrValidation, "workspace is not linked to Sellico")
	}
	return workspace.ExternalWorkspaceID.String, nil
}

func (s *SellerCabinetService) sellicoWorkspaceRefs(ctx context.Context, workspaceRef string, workspaceID uuid.UUID) ([]string, error) {
	refs := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(workspaceRef); trimmed != "" {
		refs = append(refs, trimmed)
	}

	externalID, err := s.externalWorkspaceID(ctx, workspaceID)
	if err == nil && externalID != "" && !containsString(refs, externalID) {
		refs = append(refs, externalID)
	}

	if len(refs) == 0 && err != nil {
		return nil, err
	}

	return refs, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func paginateCabinets(items []domain.SellerCabinet, limit, offset int32) []domain.SellerCabinet {
	if offset >= int32(len(items)) {
		return []domain.SellerCabinet{}
	}

	end := int(offset + limit)
	if limit <= 0 || end > len(items) {
		end = len(items)
	}

	return items[offset:end]
}
