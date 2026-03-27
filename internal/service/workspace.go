package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// WorkspaceService handles workspace and member management.
type WorkspaceService struct {
	queries *sqlcgen.Queries
}

// NewWorkspaceService creates a new WorkspaceService.
func NewWorkspaceService(queries *sqlcgen.Queries) *WorkspaceService {
	return &WorkspaceService{queries: queries}
}

// Create creates a new workspace and assigns the creator as owner.
func (s *WorkspaceService) Create(ctx context.Context, userID uuid.UUID, name, slug string) (*domain.Workspace, error) {
	// Check slug uniqueness.
	_, err := s.queries.GetWorkspaceBySlug(ctx, slug)
	if err == nil {
		return nil, apperror.New(apperror.ErrConflict, "workspace slug already taken")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrInternal, "failed to check workspace slug")
	}

	ws, err := s.queries.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{
		Name: name,
		Slug: slug,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create workspace")
	}

	// Assign creator as owner.
	_, err = s.queries.CreateWorkspaceMember(ctx, sqlcgen.CreateWorkspaceMemberParams{
		WorkspaceID: ws.ID,
		UserID:      uuidToPgtype(userID),
		Role:        domain.RoleOwner,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to assign owner role")
	}

	result := workspaceFromSqlc(ws)
	return &result, nil
}

// List returns workspaces the user is a member of.
func (s *WorkspaceService) List(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]domain.Workspace, error) {
	rows, err := s.queries.ListWorkspacesByUserID(ctx, sqlcgen.ListWorkspacesByUserIDParams{
		UserID: uuidToPgtype(userID),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list workspaces")
	}

	result := make([]domain.Workspace, len(rows))
	for i, row := range rows {
		result[i] = workspaceFromSqlc(row)
	}
	return result, nil
}

// Get returns a single workspace by ID.
func (s *WorkspaceService) Get(ctx context.Context, workspaceID uuid.UUID) (*domain.Workspace, error) {
	ws, err := s.queries.GetWorkspaceByID(ctx, uuidToPgtype(workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "workspace not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get workspace")
	}

	result := workspaceFromSqlc(ws)
	return &result, nil
}

// InviteMember adds a user to a workspace with the specified role.
func (s *WorkspaceService) InviteMember(ctx context.Context, workspaceID uuid.UUID, email, role string) (*domain.WorkspaceMember, error) {
	// Look up user by email.
	user, err := s.queries.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to find user")
	}

	targetUserID := uuidFromPgtype(user.ID)

	// Check if already a member.
	_, err = s.queries.GetWorkspaceMember(ctx, sqlcgen.GetWorkspaceMemberParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(targetUserID),
	})
	if err == nil {
		return nil, apperror.New(apperror.ErrConflict, "user is already a member of this workspace")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrInternal, "failed to check membership")
	}

	member, err := s.queries.CreateWorkspaceMember(ctx, sqlcgen.CreateWorkspaceMemberParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(targetUserID),
		Role:        role,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to add member")
	}

	result := memberFromSqlc(member)
	return &result, nil
}

// UpdateMemberRole changes a member's role and records an audit log entry.
func (s *WorkspaceService) UpdateMemberRole(ctx context.Context, actorID, workspaceID, memberID uuid.UUID, newRole string) (*domain.WorkspaceMember, error) {
	member, err := s.queries.GetWorkspaceMemberByID(ctx, uuidToPgtype(memberID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "member not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get member")
	}

	// Ensure the member belongs to the correct workspace.
	if uuidFromPgtype(member.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "member not found")
	}

	oldRole := member.Role

	updated, err := s.queries.UpdateWorkspaceMemberRole(ctx, sqlcgen.UpdateWorkspaceMemberRoleParams{
		ID:   member.ID,
		Role: newRole,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update member role")
	}

	// Write audit log.
	meta, _ := json.Marshal(map[string]string{
		"old_role": oldRole,
		"new_role": newRole,
	})
	_, _ = s.queries.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "update_member_role",
		EntityType:  "workspace_member",
		EntityID:    member.ID,
		Metadata:    meta,
	})

	result := memberFromSqlc(updated)
	return &result, nil
}

// RemoveMember removes a member from a workspace.
func (s *WorkspaceService) RemoveMember(ctx context.Context, workspaceID, memberID uuid.UUID) error {
	member, err := s.queries.GetWorkspaceMemberByID(ctx, uuidToPgtype(memberID))
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "member not found")
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to get member")
	}

	// Ensure the member belongs to the correct workspace.
	if uuidFromPgtype(member.WorkspaceID) != workspaceID {
		return apperror.New(apperror.ErrNotFound, "member not found")
	}

	// Prevent removing the last owner.
	if member.Role == domain.RoleOwner {
		return apperror.New(apperror.ErrValidation, "cannot remove the workspace owner")
	}

	if err := s.queries.DeleteWorkspaceMember(ctx, member.ID); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to remove member")
	}
	return nil
}

// GetWorkspaceMember implements MembershipChecker for the tenant middleware.
func (s *WorkspaceService) GetWorkspaceMember(ctx context.Context, workspaceID, userID uuid.UUID) (*domain.WorkspaceMember, error) {
	member, err := s.queries.GetWorkspaceMember(ctx, sqlcgen.GetWorkspaceMemberParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := memberFromSqlc(member)
	return &result, nil
}

// --- sqlc → domain mappers ---

func workspaceFromSqlc(w sqlcgen.Workspace) domain.Workspace {
	ws := domain.Workspace{
		ID:        uuidFromPgtype(w.ID),
		Name:      w.Name,
		Slug:      w.Slug,
		CreatedAt: w.CreatedAt.Time,
		UpdatedAt: w.UpdatedAt.Time,
	}
	if w.DeletedAt.Valid {
		t := w.DeletedAt.Time
		ws.DeletedAt = &t
	}
	return ws
}

func memberFromSqlc(m sqlcgen.WorkspaceMember) domain.WorkspaceMember {
	return domain.WorkspaceMember{
		ID:          uuidFromPgtype(m.ID),
		WorkspaceID: uuidFromPgtype(m.WorkspaceID),
		UserID:      uuidFromPgtype(m.UserID),
		Role:        m.Role,
		CreatedAt:   m.CreatedAt.Time,
		UpdatedAt:   m.UpdatedAt.Time,
	}
}
