package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSettingsService struct {
	getSettingsFn    func(ctx context.Context, workspaceID uuid.UUID) (*domain.WorkspaceSettings, error)
	updateSettingsFn func(ctx context.Context, actorID, workspaceID uuid.UUID, input domain.WorkspaceSettings) (*domain.WorkspaceSettings, error)
	getThresholdsFn  func(ctx context.Context, workspaceID uuid.UUID) (*domain.RecommendationThresholds, error)
}

func (m *mockSettingsService) GetSettings(ctx context.Context, workspaceID uuid.UUID) (*domain.WorkspaceSettings, error) {
	return m.getSettingsFn(ctx, workspaceID)
}
func (m *mockSettingsService) UpdateSettings(ctx context.Context, actorID, workspaceID uuid.UUID, input domain.WorkspaceSettings) (*domain.WorkspaceSettings, error) {
	return m.updateSettingsFn(ctx, actorID, workspaceID, input)
}
func (m *mockSettingsService) GetThresholds(ctx context.Context, workspaceID uuid.UUID) (*domain.RecommendationThresholds, error) {
	return m.getThresholdsFn(ctx, workspaceID)
}

func TestWorkspaceSettingsHandler_GetSettings(t *testing.T) {
	wsID := uuid.New()
	expected := &domain.WorkspaceSettings{
		RecommendationThresholds: &domain.RecommendationThresholds{
			CampaignHighImpressions: 2000,
		},
	}

	svc := &mockSettingsService{
		getSettingsFn: func(_ context.Context, id uuid.UUID) (*domain.WorkspaceSettings, error) {
			assert.Equal(t, wsID, id)
			return expected, nil
		},
	}

	h := NewWorkspaceSettingsHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, wsID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetSettings(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "2000")
}

func TestWorkspaceSettingsHandler_UpdateSettings(t *testing.T) {
	wsID := uuid.New()
	userID := uuid.New()
	input := domain.WorkspaceSettings{
		Notifications: &domain.NotificationSettings{
			Telegram: &domain.TelegramSettings{
				BotToken: "test-token",
				ChatID:   "-100123",
				Enabled:  true,
			},
		},
	}

	svc := &mockSettingsService{
		updateSettingsFn: func(_ context.Context, actor, ws uuid.UUID, in domain.WorkspaceSettings) (*domain.WorkspaceSettings, error) {
			assert.Equal(t, userID, actor)
			assert.Equal(t, wsID, ws)
			assert.True(t, in.Notifications.Telegram.Enabled)
			return &in, nil
		},
	}

	h := NewWorkspaceSettingsHandler(svc)
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, wsID)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.UpdateSettings(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkspaceSettingsHandler_GetSettings_MissingWorkspace(t *testing.T) {
	h := NewWorkspaceSettingsHandler(&mockSettingsService{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()
	h.GetSettings(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWorkspaceSettingsHandler_GetThresholds(t *testing.T) {
	wsID := uuid.New()
	defaults := domain.DefaultThresholds()

	svc := &mockSettingsService{
		getThresholdsFn: func(_ context.Context, id uuid.UUID) (*domain.RecommendationThresholds, error) {
			return &defaults, nil
		},
	}

	h := NewWorkspaceSettingsHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/thresholds", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, wsID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetThresholds(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
