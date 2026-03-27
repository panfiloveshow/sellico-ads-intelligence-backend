package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
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
}

// NewSellerCabinetService creates a new SellerCabinetService.
func NewSellerCabinetService(queries *sqlcgen.Queries, encryptionKey []byte, tokenValidator WBTokenValidator) *SellerCabinetService {
	return &SellerCabinetService{
		queries:        queries,
		encryptionKey:  encryptionKey,
		tokenValidator: tokenValidator,
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
		return nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("invalid WB API token: %s", err.Error()))
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

// List returns seller cabinets for the given workspace (without decrypted tokens).
func (s *SellerCabinetService) List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.SellerCabinet, error) {
	rows, err := s.queries.ListSellerCabinetsByWorkspace(ctx, sqlcgen.ListSellerCabinetsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list seller cabinets")
	}

	result := make([]domain.SellerCabinet, len(rows))
	for i, row := range rows {
		result[i] = sellerCabinetFromSqlc(row)
	}
	return result, nil
}

// Get returns a single seller cabinet by ID, verifying workspace ownership.
func (s *SellerCabinetService) Get(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error) {
	sc, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get seller cabinet")
	}

	// Verify workspace ownership.
	if uuidFromPgtype(sc.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}

	result := sellerCabinetFromSqlc(sc)
	return &result, nil
}

// Delete performs a soft delete on the seller cabinet after verifying workspace ownership.
func (s *SellerCabinetService) Delete(ctx context.Context, actorID, workspaceID, cabinetID uuid.UUID) error {
	// Get the cabinet to verify it exists and belongs to the workspace.
	sc, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to get seller cabinet")
	}

	if uuidFromPgtype(sc.WorkspaceID) != workspaceID {
		return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}

	// Soft delete.
	if err := s.queries.SoftDeleteSellerCabinet(ctx, uuidToPgtype(cabinetID)); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to delete seller cabinet")
	}

	// Audit log the deletion.
	meta, _ := json.Marshal(map[string]string{
		"cabinet_name": sc.Name,
	})
	_, _ = s.queries.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "delete_seller_cabinet",
		EntityType:  "seller_cabinet",
		EntityID:    sc.ID,
		Metadata:    meta,
	})

	return nil
}

// --- sqlc → domain mapper ---

func sellerCabinetFromSqlc(sc sqlcgen.SellerCabinet) domain.SellerCabinet {
	result := domain.SellerCabinet{
		ID:             uuidFromPgtype(sc.ID),
		WorkspaceID:    uuidFromPgtype(sc.WorkspaceID),
		Name:           sc.Name,
		EncryptedToken: sc.EncryptedToken,
		Status:         sc.Status,
		CreatedAt:      sc.CreatedAt.Time,
		UpdatedAt:      sc.UpdatedAt.Time,
	}
	if sc.LastSyncedAt.Valid {
		t := sc.LastSyncedAt.Time
		result.LastSyncedAt = &t
	}
	if sc.DeletedAt.Valid {
		t := sc.DeletedAt.Time
		result.DeletedAt = &t
	}
	return result
}
