package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockExtensionService struct {
	upsertSessionFn      func(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error)
	createContextEventFn func(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error)
	versionFn            func() string
}

func (m *mockExtensionService) UpsertSession(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error) {
	return m.upsertSessionFn(ctx, userID, workspaceID, extensionVersion)
}

func (m *mockExtensionService) CreateContextEvent(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error) {
	return m.createContextEventFn(ctx, userID, workspaceID, url, pageType)
}

func (m *mockExtensionService) Version() string {
	return m.versionFn()
}

func decodeExtensionSession(t *testing.T, data interface{}) dto.ExtensionSessionResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.ExtensionSessionResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestExtensionCreateSession_Success(t *testing.T) {
	userID := uuid.New()
	workspaceID := uuid.New()
	now := time.Now()
	mock := &mockExtensionService{
		upsertSessionFn: func(_ context.Context, gotUserID, gotWorkspaceID uuid.UUID, version string) (*domain.ExtensionSession, error) {
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, "1.0.0", version)
			return &domain.ExtensionSession{
				ID:               uuid.New(),
				UserID:           userID,
				WorkspaceID:      workspaceID,
				ExtensionVersion: version,
				LastActiveAt:     now,
				CreatedAt:        now,
			}, nil
		},
		createContextEventFn: func(context.Context, uuid.UUID, uuid.UUID, string, string) (*domain.ExtensionContextEvent, error) {
			return nil, nil
		},
		versionFn: func() string { return "1.0.0" },
	}
	h := NewExtensionHandler(mock)

	body := jsonBody(t, dto.CreateExtensionSessionRequest{ExtensionVersion: "1.0.0"})
	req := httptest.NewRequest(http.MethodPost, "/extension/sessions", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	session := decodeExtensionSession(t, resp.Data)
	assert.Equal(t, "1.0.0", session.ExtensionVersion)
}

func TestExtensionVersion(t *testing.T) {
	h := NewExtensionHandler(&mockExtensionService{
		upsertSessionFn: func(context.Context, uuid.UUID, uuid.UUID, string) (*domain.ExtensionSession, error) { return nil, nil },
		createContextEventFn: func(context.Context, uuid.UUID, uuid.UUID, string, string) (*domain.ExtensionContextEvent, error) {
			return nil, nil
		},
		versionFn: func() string { return "2.3.4" },
	})
	req := httptest.NewRequest(http.MethodGet, "/extension/version", nil)
	rec := httptest.NewRecorder()

	h.Version(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	raw, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var version dto.ExtensionVersionResponse
	require.NoError(t, json.Unmarshal(raw, &version))
	assert.Equal(t, "2.3.4", version.Version)
}
