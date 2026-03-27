package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock ---

type mockWorkspaceService struct {
	createFn           func(ctx context.Context, userID uuid.UUID, name, slug string) (*domain.Workspace, error)
	listFn             func(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]domain.Workspace, error)
	getFn              func(ctx context.Context, workspaceID uuid.UUID) (*domain.Workspace, error)
	inviteMemberFn     func(ctx context.Context, workspaceID uuid.UUID, email, role string) (*domain.WorkspaceMember, error)
	updateMemberRoleFn func(ctx context.Context, actorID, workspaceID, memberID uuid.UUID, newRole string) (*domain.WorkspaceMember, error)
	removeMemberFn     func(ctx context.Context, workspaceID, memberID uuid.UUID) error
}

func (m *mockWorkspaceService) Create(ctx context.Context, userID uuid.UUID, name, slug string) (*domain.Workspace, error) {
	return m.createFn(ctx, userID, name, slug)
}
func (m *mockWorkspaceService) List(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]domain.Workspace, error) {
	return m.listFn(ctx, userID, limit, offset)
}
func (m *mockWorkspaceService) Get(ctx context.Context, workspaceID uuid.UUID) (*domain.Workspace, error) {
	return m.getFn(ctx, workspaceID)
}
func (m *mockWorkspaceService) InviteMember(ctx context.Context, workspaceID uuid.UUID, email, role string) (*domain.WorkspaceMember, error) {
	return m.inviteMemberFn(ctx, workspaceID, email, role)
}
func (m *mockWorkspaceService) UpdateMemberRole(ctx context.Context, actorID, workspaceID, memberID uuid.UUID, newRole string) (*domain.WorkspaceMember, error) {
	return m.updateMemberRoleFn(ctx, actorID, workspaceID, memberID, newRole)
}
func (m *mockWorkspaceService) RemoveMember(ctx context.Context, workspaceID, memberID uuid.UUID) error {
	return m.removeMemberFn(ctx, workspaceID, memberID)
}

// --- helpers ---

func decodeWorkspace(t *testing.T, data interface{}) dto.WorkspaceResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var ws dto.WorkspaceResponse
	require.NoError(t, json.Unmarshal(b, &ws))
	return ws
}

func decodeMember(t *testing.T, data interface{}) dto.WorkspaceMemberResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var m dto.WorkspaceMemberResponse
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// --- Create tests ---

func TestCreateWorkspace_Success(t *testing.T) {
	now := time.Now()
	wsID := uuid.New()
	userID := uuid.New()

	mock := &mockWorkspaceService{
		createFn: func(_ context.Context, uid uuid.UUID, name, slug string) (*domain.Workspace, error) {
			assert.Equal(t, userID, uid)
			assert.Equal(t, "My Workspace", name)
			assert.Equal(t, "my-workspace", slug)
			return &domain.Workspace{
				ID:        wsID,
				Name:      "My Workspace",
				Slug:      "my-workspace",
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		},
	}
	h := NewWorkspaceHandler(mock)

	body := jsonBody(t, dto.CreateWorkspaceRequest{Name: "My Workspace", Slug: "my-workspace"})
	req := httptest.NewRequest(http.MethodPost, "/workspaces", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	ws := decodeWorkspace(t, resp.Data)
	assert.Equal(t, wsID, ws.ID)
	assert.Equal(t, "My Workspace", ws.Name)
	assert.Equal(t, "my-workspace", ws.Slug)
}

func TestCreateWorkspace_ValidationError(t *testing.T) {
	h := NewWorkspaceHandler(&mockWorkspaceService{})

	body := jsonBody(t, dto.CreateWorkspaceRequest{})
	req := httptest.NewRequest(http.MethodPost, "/workspaces", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
	fields := make(map[string]bool)
	for _, e := range resp.Errors {
		fields[e.Field] = true
	}
	assert.True(t, fields["name"], "expected validation error for name")
	assert.True(t, fields["slug"], "expected validation error for slug")
}

func TestCreateWorkspace_DuplicateSlug(t *testing.T) {
	mock := &mockWorkspaceService{
		createFn: func(_ context.Context, _ uuid.UUID, _, _ string) (*domain.Workspace, error) {
			return nil, apperror.New(apperror.ErrConflict, "slug already taken")
		},
	}
	h := NewWorkspaceHandler(mock)

	body := jsonBody(t, dto.CreateWorkspaceRequest{Name: "Dup", Slug: "dup-slug"})
	req := httptest.NewRequest(http.MethodPost, "/workspaces", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "CONFLICT", resp.Errors[0].Code)
}

func TestCreateWorkspace_NoAuth(t *testing.T) {
	h := NewWorkspaceHandler(&mockWorkspaceService{})

	body := jsonBody(t, dto.CreateWorkspaceRequest{Name: "WS", Slug: "ws"})
	req := httptest.NewRequest(http.MethodPost, "/workspaces", body)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", resp.Errors[0].Code)
}

// --- List tests ---

func TestListWorkspaces_Success(t *testing.T) {
	now := time.Now()
	userID := uuid.New()
	ws1 := domain.Workspace{ID: uuid.New(), Name: "WS1", Slug: "ws1", CreatedAt: now, UpdatedAt: now}
	ws2 := domain.Workspace{ID: uuid.New(), Name: "WS2", Slug: "ws2", CreatedAt: now, UpdatedAt: now}

	mock := &mockWorkspaceService{
		listFn: func(_ context.Context, uid uuid.UUID, limit, offset int32) ([]domain.Workspace, error) {
			assert.Equal(t, userID, uid)
			return []domain.Workspace{ws1, ws2}, nil
		},
	}
	h := NewWorkspaceHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	assert.NotNil(t, resp.Meta)

	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	assert.Len(t, items, 2)
}

func TestListWorkspaces_Empty(t *testing.T) {
	mock := &mockWorkspaceService{
		listFn: func(_ context.Context, _ uuid.UUID, _, _ int32) ([]domain.Workspace, error) {
			return []domain.Workspace{}, nil
		},
	}
	h := NewWorkspaceHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)

	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	assert.Len(t, items, 0)
}

// --- InviteMember tests ---

func TestInviteMember_Success(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()
	memberID := uuid.New()
	memberUserID := uuid.New()

	mock := &mockWorkspaceService{
		inviteMemberFn: func(_ context.Context, wsID uuid.UUID, email, role string) (*domain.WorkspaceMember, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, "invite@example.com", email)
			assert.Equal(t, "analyst", role)
			return &domain.WorkspaceMember{
				ID:          memberID,
				WorkspaceID: workspaceID,
				UserID:      memberUserID,
				Role:        "analyst",
				CreatedAt:   now,
				UpdatedAt:   now,
			}, nil
		},
	}
	h := NewWorkspaceHandler(mock)

	body := jsonBody(t, dto.InviteMemberRequest{Email: "invite@example.com", Role: "analyst"})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceID.String()+"/members", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.InviteMember(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	m := decodeMember(t, resp.Data)
	assert.Equal(t, memberID, m.ID)
	assert.Equal(t, "analyst", m.Role)
}

func TestInviteMember_ValidationError(t *testing.T) {
	h := NewWorkspaceHandler(&mockWorkspaceService{})

	body := jsonBody(t, dto.InviteMemberRequest{})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/x/members", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.InviteMember(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
	fields := make(map[string]bool)
	for _, e := range resp.Errors {
		fields[e.Field] = true
	}
	assert.True(t, fields["email"], "expected validation error for email")
	assert.True(t, fields["role"], "expected validation error for role")
}

func TestInviteMember_UserNotFound(t *testing.T) {
	mock := &mockWorkspaceService{
		inviteMemberFn: func(_ context.Context, _ uuid.UUID, _, _ string) (*domain.WorkspaceMember, error) {
			return nil, apperror.New(apperror.ErrNotFound, "user not found")
		},
	}
	h := NewWorkspaceHandler(mock)

	body := jsonBody(t, dto.InviteMemberRequest{Email: "nobody@example.com", Role: "viewer"})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/x/members", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.InviteMember(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "NOT_FOUND", resp.Errors[0].Code)
}

// --- UpdateMemberRole tests ---

func TestUpdateMemberRole_Success(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	memberUserID := uuid.New()

	mock := &mockWorkspaceService{
		updateMemberRoleFn: func(_ context.Context, actorID, wsID, mID uuid.UUID, newRole string) (*domain.WorkspaceMember, error) {
			assert.Equal(t, userID, actorID)
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, memberID, mID)
			assert.Equal(t, "manager", newRole)
			return &domain.WorkspaceMember{
				ID:          memberID,
				WorkspaceID: workspaceID,
				UserID:      memberUserID,
				Role:        "manager",
				CreatedAt:   now,
				UpdatedAt:   now,
			}, nil
		},
	}
	h := NewWorkspaceHandler(mock)

	body := jsonBody(t, dto.UpdateMemberRoleRequest{Role: "manager"})
	req := httptest.NewRequest(http.MethodPatch, "/workspaces/"+workspaceID.String()+"/members/"+memberID.String(), body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("memberId", memberID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateMemberRole(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	m := decodeMember(t, resp.Data)
	assert.Equal(t, memberID, m.ID)
	assert.Equal(t, "manager", m.Role)
}

func TestUpdateMemberRole_InvalidMemberId(t *testing.T) {
	h := NewWorkspaceHandler(&mockWorkspaceService{})

	body := jsonBody(t, dto.UpdateMemberRoleRequest{Role: "manager"})
	req := httptest.NewRequest(http.MethodPatch, "/workspaces/x/members/bad-uuid", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.UserIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("memberId", "bad-uuid")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateMemberRole(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "VALIDATION_ERROR", resp.Errors[0].Code)
}

func TestUpdateMemberRole_ValidationError(t *testing.T) {
	h := NewWorkspaceHandler(&mockWorkspaceService{})
	memberID := uuid.New()

	body := jsonBody(t, dto.UpdateMemberRoleRequest{Role: "superadmin"})
	req := httptest.NewRequest(http.MethodPatch, "/workspaces/x/members/"+memberID.String(), body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.UserIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("memberId", memberID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateMemberRole(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
	fields := make(map[string]bool)
	for _, e := range resp.Errors {
		fields[e.Field] = true
	}
	assert.True(t, fields["role"], "expected validation error for role")
}

// --- RemoveMember tests ---

func TestRemoveMember_Success(t *testing.T) {
	workspaceID := uuid.New()
	memberID := uuid.New()

	mock := &mockWorkspaceService{
		removeMemberFn: func(_ context.Context, wsID, mID uuid.UUID) error {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, memberID, mID)
			return nil
		},
	}
	h := NewWorkspaceHandler(mock)

	req := httptest.NewRequest(http.MethodDelete, "/workspaces/"+workspaceID.String()+"/members/"+memberID.String(), nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("memberId", memberID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.RemoveMember(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
}

func TestRemoveMember_NotFound(t *testing.T) {
	mock := &mockWorkspaceService{
		removeMemberFn: func(_ context.Context, _, _ uuid.UUID) error {
			return apperror.New(apperror.ErrNotFound, "member not found")
		},
	}
	h := NewWorkspaceHandler(mock)
	memberID := uuid.New()

	req := httptest.NewRequest(http.MethodDelete, "/workspaces/x/members/"+memberID.String(), nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("memberId", memberID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.RemoveMember(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "NOT_FOUND", resp.Errors[0].Code)
}
