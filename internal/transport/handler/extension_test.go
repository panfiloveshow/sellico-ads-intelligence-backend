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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockExtensionService struct {
	upsertSessionFn           func(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error)
	createContextEventFn      func(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error)
	createPageContextFn       func(ctx context.Context, userID, workspaceID uuid.UUID, input service.CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error)
	createBidSnapshotsFn      func(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionBidSnapshotInput) (int, error)
	createPositionSnapshotsFn func(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionPositionSnapshotInput) (int, error)
	createUISignalsFn         func(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionUISignalInput) (int, error)
	createNetworkCapturesFn   func(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionNetworkCaptureInput) (int, error)
	searchWidgetFn            func(ctx context.Context, workspaceID uuid.UUID, query string) (*service.ExtensionSearchWidget, error)
	productWidgetFn           func(ctx context.Context, workspaceID, productID uuid.UUID) (*service.ExtensionProductWidget, error)
	productWidgetByWBIDFn     func(ctx context.Context, workspaceID uuid.UUID, wbProductID int64) (*service.ExtensionProductWidget, error)
	campaignWidgetFn          func(ctx context.Context, workspaceID, campaignID uuid.UUID) (*service.ExtensionCampaignWidget, error)
	campaignWidgetByWBIDFn    func(ctx context.Context, workspaceID uuid.UUID, wbCampaignID int64) (*service.ExtensionCampaignWidget, error)
	versionFn                 func() string
}

func (m *mockExtensionService) UpsertSession(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error) {
	return m.upsertSessionFn(ctx, userID, workspaceID, extensionVersion)
}

func (m *mockExtensionService) CreateContextEvent(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error) {
	return m.createContextEventFn(ctx, userID, workspaceID, url, pageType)
}

func (m *mockExtensionService) CreatePageContext(ctx context.Context, userID, workspaceID uuid.UUID, input service.CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error) {
	return m.createPageContextFn(ctx, userID, workspaceID, input)
}

func (m *mockExtensionService) CreateBidSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionBidSnapshotInput) (int, error) {
	return m.createBidSnapshotsFn(ctx, userID, workspaceID, inputs)
}

func (m *mockExtensionService) CreatePositionSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionPositionSnapshotInput) (int, error) {
	return m.createPositionSnapshotsFn(ctx, userID, workspaceID, inputs)
}

func (m *mockExtensionService) CreateUISignals(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionUISignalInput) (int, error) {
	return m.createUISignalsFn(ctx, userID, workspaceID, inputs)
}

func (m *mockExtensionService) CreateNetworkCaptures(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionNetworkCaptureInput) (int, error) {
	return m.createNetworkCapturesFn(ctx, userID, workspaceID, inputs)
}

func (m *mockExtensionService) GetSearchWidget(ctx context.Context, workspaceID uuid.UUID, query string) (*service.ExtensionSearchWidget, error) {
	return m.searchWidgetFn(ctx, workspaceID, query)
}

func (m *mockExtensionService) GetProductWidget(ctx context.Context, workspaceID, productID uuid.UUID) (*service.ExtensionProductWidget, error) {
	return m.productWidgetFn(ctx, workspaceID, productID)
}

func (m *mockExtensionService) GetProductWidgetByWBProductID(ctx context.Context, workspaceID uuid.UUID, wbProductID int64) (*service.ExtensionProductWidget, error) {
	return m.productWidgetByWBIDFn(ctx, workspaceID, wbProductID)
}

func (m *mockExtensionService) GetCampaignWidget(ctx context.Context, workspaceID, campaignID uuid.UUID) (*service.ExtensionCampaignWidget, error) {
	return m.campaignWidgetFn(ctx, workspaceID, campaignID)
}

func (m *mockExtensionService) GetCampaignWidgetByWBCampaignID(ctx context.Context, workspaceID uuid.UUID, wbCampaignID int64) (*service.ExtensionCampaignWidget, error) {
	return m.campaignWidgetByWBIDFn(ctx, workspaceID, wbCampaignID)
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
		createPageContextFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error) {
			return nil, nil
		},
		createBidSnapshotsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionBidSnapshotInput) (int, error) {
			return 0, nil
		},
		createPositionSnapshotsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionPositionSnapshotInput) (int, error) {
			return 0, nil
		},
		createUISignalsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionUISignalInput) (int, error) {
			return 0, nil
		},
		createNetworkCapturesFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionNetworkCaptureInput) (int, error) {
			return 0, nil
		},
		searchWidgetFn:  func(context.Context, uuid.UUID, string) (*service.ExtensionSearchWidget, error) { return nil, nil },
		productWidgetFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExtensionProductWidget, error) { return nil, nil },
		productWidgetByWBIDFn: func(context.Context, uuid.UUID, int64) (*service.ExtensionProductWidget, error) {
			return nil, nil
		},
		campaignWidgetFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExtensionCampaignWidget, error) { return nil, nil },
		campaignWidgetByWBIDFn: func(context.Context, uuid.UUID, int64) (*service.ExtensionCampaignWidget, error) {
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
		createPageContextFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error) {
			return nil, nil
		},
		createBidSnapshotsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionBidSnapshotInput) (int, error) {
			return 0, nil
		},
		createPositionSnapshotsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionPositionSnapshotInput) (int, error) {
			return 0, nil
		},
		createUISignalsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionUISignalInput) (int, error) {
			return 0, nil
		},
		createNetworkCapturesFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionNetworkCaptureInput) (int, error) {
			return 0, nil
		},
		searchWidgetFn:  func(context.Context, uuid.UUID, string) (*service.ExtensionSearchWidget, error) { return nil, nil },
		productWidgetFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExtensionProductWidget, error) { return nil, nil },
		productWidgetByWBIDFn: func(context.Context, uuid.UUID, int64) (*service.ExtensionProductWidget, error) {
			return nil, nil
		},
		campaignWidgetFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExtensionCampaignWidget, error) { return nil, nil },
		campaignWidgetByWBIDFn: func(context.Context, uuid.UUID, int64) (*service.ExtensionCampaignWidget, error) {
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

func TestExtensionCreatePageContext_Success(t *testing.T) {
	userID := uuid.New()
	workspaceID := uuid.New()
	now := time.Now().UTC()
	mock := &mockExtensionService{
		upsertSessionFn: func(context.Context, uuid.UUID, uuid.UUID, string) (*domain.ExtensionSession, error) { return nil, nil },
		createContextEventFn: func(context.Context, uuid.UUID, uuid.UUID, string, string) (*domain.ExtensionContextEvent, error) {
			return nil, nil
		},
		createPageContextFn: func(_ context.Context, gotUserID, gotWorkspaceID uuid.UUID, input service.CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error) {
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, "campaign", input.PageType)
			return &domain.ExtensionPageContext{
				ID:          uuid.New(),
				SessionID:   uuid.New(),
				WorkspaceID: workspaceID,
				UserID:      userID,
				URL:         "https://seller.wildberries.ru/campaign",
				PageType:    "campaign",
				CapturedAt:  now,
				CreatedAt:   now,
			}, nil
		},
		createBidSnapshotsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionBidSnapshotInput) (int, error) {
			return 0, nil
		},
		createPositionSnapshotsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionPositionSnapshotInput) (int, error) {
			return 0, nil
		},
		createUISignalsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionUISignalInput) (int, error) {
			return 0, nil
		},
		createNetworkCapturesFn: func(context.Context, uuid.UUID, uuid.UUID, []service.CreateExtensionNetworkCaptureInput) (int, error) {
			return 0, nil
		},
		searchWidgetFn:  func(context.Context, uuid.UUID, string) (*service.ExtensionSearchWidget, error) { return nil, nil },
		productWidgetFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExtensionProductWidget, error) { return nil, nil },
		productWidgetByWBIDFn: func(context.Context, uuid.UUID, int64) (*service.ExtensionProductWidget, error) {
			return nil, nil
		},
		campaignWidgetFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExtensionCampaignWidget, error) { return nil, nil },
		campaignWidgetByWBIDFn: func(context.Context, uuid.UUID, int64) (*service.ExtensionCampaignWidget, error) {
			return nil, nil
		},
		versionFn: func() string { return "1.0.0" },
	}
	h := NewExtensionHandler(mock)

	body := jsonBody(t, dto.CreateExtensionPageContextRequest{
		URL:      "https://seller.wildberries.ru/campaign",
		PageType: "campaign",
	})
	req := httptest.NewRequest(http.MethodPost, "/extension/page-context", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreatePageContext(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}
