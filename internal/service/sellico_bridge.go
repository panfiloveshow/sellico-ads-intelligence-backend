package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type SellicoBridgeService struct {
	queries       *sqlcgen.Queries
	client        *sellico.Client
	encryptionKey []byte
}

func NewSellicoBridgeService(queries *sqlcgen.Queries, client *sellico.Client, encryptionKey ...[]byte) *SellicoBridgeService {
	svc := &SellicoBridgeService{
		queries: queries,
		client:  client,
	}
	if len(encryptionKey) > 0 {
		svc.encryptionKey = encryptionKey[0]
	}
	return svc
}

func (s *SellicoBridgeService) Authenticate(ctx context.Context, token string) (*domain.AuthPrincipal, error) {
	externalUser, err := s.client.GetUser(ctx, token)
	if err != nil {
		return nil, err
	}

	localUser, err := s.syncUser(ctx, externalUser)
	if err != nil {
		return nil, err
	}

	return &domain.AuthPrincipal{
		UserID:         uuidFromPgtype(localUser.ID),
		ExternalUserID: externalUser.ID,
		Email:          externalUser.Email,
		Name:           externalUser.Name,
		Token:          token,
	}, nil
}

func (s *SellicoBridgeService) ResolveWorkspaceAccess(ctx context.Context, principal domain.AuthPrincipal, rawWorkspaceID string) (*domain.WorkspaceAccess, error) {
	workspaces, err := s.client.ListWorkspaces(ctx, principal.Token)
	if err != nil {
		return nil, err
	}

	matchedWorkspace, ok := findWorkspace(workspaces, rawWorkspaceID)
	if !ok {
		return nil, nil
	}

	localWorkspace, err := s.syncWorkspace(ctx, matchedWorkspace)
	if err != nil {
		return nil, err
	}

	role := determineRole(principal.ExternalUserID, matchedWorkspace)
	member, err := s.ensureWorkspaceMember(ctx, uuidFromPgtype(localWorkspace.ID), principal.UserID, role)
	if err != nil {
		return nil, err
	}

	// Cache Sellico token for background worker use (integration refresh).
	s.cacheSellicoToken(ctx, localWorkspace.ID, principal.Token)

	return &domain.WorkspaceAccess{
		WorkspaceID: uuidFromPgtype(localWorkspace.ID),
		Role:        member.Role,
	}, nil
}

// cacheSellicoToken encrypts and stores the user's Sellico token for background worker use.
func (s *SellicoBridgeService) cacheSellicoToken(ctx context.Context, workspaceID pgtype.UUID, token string) {
	if len(s.encryptionKey) == 0 || token == "" {
		return
	}
	encrypted, err := crypto.Encrypt(token, s.encryptionKey)
	if err != nil {
		return
	}
	_ = s.queries.CacheSellicoToken(ctx, workspaceID, encrypted)
}

func (s *SellicoBridgeService) syncUser(ctx context.Context, externalUser *sellico.User) (sqlcgen.User, error) {
	user, err := s.queries.GetUserByExternalUserID(ctx, textToPgtype(externalUser.ID))
	if err == nil {
		if user.Email == externalUser.Email && user.Name == externalUser.Name && user.AuthSource == "sellico" {
			return user, nil
		}

		return s.queries.UpdateExternalUser(ctx, sqlcgen.UpdateExternalUserParams{
			ID:             user.ID,
			Email:          externalUser.Email,
			Name:           externalUser.Name,
			ExternalUserID: textToPgtype(externalUser.ID),
			AuthSource:     "sellico",
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.User{}, err
	}

	user, err = s.queries.GetUserByEmail(ctx, externalUser.Email)
	if err == nil {
		return s.queries.UpdateExternalUser(ctx, sqlcgen.UpdateExternalUserParams{
			ID:             user.ID,
			Email:          externalUser.Email,
			Name:           externalUser.Name,
			ExternalUserID: textToPgtype(externalUser.ID),
			AuthSource:     "sellico",
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.User{}, err
	}

	return s.queries.CreateExternalUser(ctx, sqlcgen.CreateExternalUserParams{
		Email:          externalUser.Email,
		PasswordHash:   unusablePasswordHash("user", externalUser.ID),
		Name:           fallbackName(externalUser),
		ExternalUserID: textToPgtype(externalUser.ID),
		AuthSource:     "sellico",
	})
}

func (s *SellicoBridgeService) syncWorkspace(ctx context.Context, externalWorkspace sellico.Workspace) (sqlcgen.Workspace, error) {
	externalID := externalWorkspace.ExternalID
	if externalID == "" {
		externalID = externalWorkspace.ID
	}
	slug := workspaceSlug(externalID)

	workspace, err := s.queries.GetWorkspaceByExternalWorkspaceID(ctx, textToPgtype(externalID))
	if err == nil {
		if workspace.Name == externalWorkspace.Name && workspace.Slug == slug && workspace.Source == "sellico" {
			return workspace, nil
		}

		return s.queries.UpdateExternalWorkspace(ctx, sqlcgen.UpdateExternalWorkspaceParams{
			ID:                  workspace.ID,
			Name:                externalWorkspace.Name,
			Slug:                slug,
			ExternalWorkspaceID: textToPgtype(externalID),
			Source:              "sellico",
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.Workspace{}, err
	}

	workspace, err = s.queries.GetWorkspaceBySlug(ctx, slug)
	if err == nil {
		return s.queries.UpdateExternalWorkspace(ctx, sqlcgen.UpdateExternalWorkspaceParams{
			ID:                  workspace.ID,
			Name:                externalWorkspace.Name,
			Slug:                slug,
			ExternalWorkspaceID: textToPgtype(externalID),
			Source:              "sellico",
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.Workspace{}, err
	}

	return s.queries.CreateExternalWorkspace(ctx, sqlcgen.CreateExternalWorkspaceParams{
		Name:                externalWorkspace.Name,
		Slug:                slug,
		ExternalWorkspaceID: textToPgtype(externalID),
		Source:              "sellico",
	})
}

func (s *SellicoBridgeService) ensureWorkspaceMember(ctx context.Context, workspaceID, userID uuid.UUID, role string) (sqlcgen.WorkspaceMember, error) {
	member, err := s.queries.GetWorkspaceMember(ctx, sqlcgen.GetWorkspaceMemberParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(userID),
	})
	if err == nil {
		if member.Role == role {
			return member, nil
		}

		return s.queries.UpdateWorkspaceMemberRole(ctx, sqlcgen.UpdateWorkspaceMemberRoleParams{
			ID:   member.ID,
			Role: role,
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.WorkspaceMember{}, err
	}

	return s.queries.CreateWorkspaceMember(ctx, sqlcgen.CreateWorkspaceMemberParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(userID),
		Role:        role,
	})
}

func fallbackName(user *sellico.User) string {
	if strings.TrimSpace(user.Name) != "" {
		return user.Name
	}
	if strings.TrimSpace(user.Email) != "" {
		return user.Email
	}
	return "Sellico User"
}

func workspaceSlug(externalID string) string {
	return fmt.Sprintf("sellico-%s", externalID)
}

func unusablePasswordHash(prefix, id string) string {
	return fmt.Sprintf("!sellico-%s-%s-%s", prefix, id, uuid.NewString())
}

func determineRole(externalUserID string, workspace sellico.Workspace) string {
	if externalUserID != "" && (externalUserID == workspace.UserID || externalUserID == workspace.OwnerID) {
		return domain.RoleOwner
	}

	return domain.RoleManager
}

func findWorkspace(workspaces []sellico.Workspace, rawWorkspaceID string) (sellico.Workspace, bool) {
	for _, workspace := range workspaces {
		if rawWorkspaceID == workspace.ExternalID || rawWorkspaceID == workspace.ID || rawWorkspaceID == workspace.AccountID {
			return workspace, true
		}
	}

	return sellico.Workspace{}, false
}
