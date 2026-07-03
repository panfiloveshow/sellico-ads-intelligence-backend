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
	createDOMRowSnapshotsFn   func(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionDOMRowSnapshotInput) (int, error)
	searchWidgetFn            func(ctx context.Context, workspaceID uuid.UUID, query string) (*service.ExtensionSearchWidget, error)
	productWidgetFn           func(ctx context.Context, workspaceID, productID uuid.UUID) (*service.ExtensionProductWidget, error)
	productWidgetByWBIDFn     func(ctx context.Context, workspaceID uuid.UUID, wbProductID int64) (*service.ExtensionProductWidget, error)
	campaignWidgetFn          func(ctx context.Context, workspaceID, campaignID uuid.UUID) (*service.ExtensionCampaignWidget, error)
	campaignWidgetByWBIDFn    func(ctx context.Context, workspaceID uuid.UUID, wbCampaignID int64) (*service.ExtensionCampaignWidget, error)
	evidenceSummaryFn         func(ctx context.Context, workspaceID uuid.UUID) (*service.ExtensionEvidenceSummary, error)
	evidenceDebugFn           func(ctx context.Context, workspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceDebug, error)
	evidenceSupportReportFn   func(ctx context.Context, workspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceSupportReport, error)
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

func (m *mockExtensionService) CreateDOMRowSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionDOMRowSnapshotInput) (int, error) {
	if m.createDOMRowSnapshotsFn == nil {
		return len(inputs), nil
	}
	return m.createDOMRowSnapshotsFn(ctx, userID, workspaceID, inputs)
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

func (m *mockExtensionService) GetEvidenceSummary(ctx context.Context, workspaceID uuid.UUID) (*service.ExtensionEvidenceSummary, error) {
	if m.evidenceSummaryFn == nil {
		return &service.ExtensionEvidenceSummary{WorkspaceID: workspaceID, GeneratedAt: time.Now().UTC()}, nil
	}
	return m.evidenceSummaryFn(ctx, workspaceID)
}

func (m *mockExtensionService) GetEvidenceDebug(ctx context.Context, workspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceDebug, error) {
	if m.evidenceDebugFn == nil {
		return &service.ExtensionEvidenceDebug{WorkspaceID: workspaceID, Scope: input.Scope, GeneratedAt: time.Now().UTC()}, nil
	}
	return m.evidenceDebugFn(ctx, workspaceID, input)
}

func (m *mockExtensionService) GetEvidenceSupportReport(ctx context.Context, workspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceSupportReport, error) {
	if m.evidenceSupportReportFn == nil {
		return &service.ExtensionEvidenceSupportReport{WorkspaceID: workspaceID, Scope: input.Scope, GeneratedAt: time.Now().UTC()}, nil
	}
	return m.evidenceSupportReportFn(ctx, workspaceID, input)
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

func TestExtensionCreateDOMRowSnapshots_Success(t *testing.T) {
	userID := uuid.New()
	workspaceID := uuid.New()
	mock := &mockExtensionService{
		createDOMRowSnapshotsFn: func(_ context.Context, gotUserID, gotWorkspaceID uuid.UUID, inputs []service.CreateExtensionDOMRowSnapshotInput) (int, error) {
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, workspaceID, gotWorkspaceID)
			require.Len(t, inputs, 1)
			assert.Equal(t, "campaign", inputs[0].PageType)
			assert.Equal(t, "campaigns", inputs[0].TableRole)
			assert.Equal(t, "campaign-123", inputs[0].RowKey)
			assert.Contains(t, inputs[0].VisibleText, "Кампания")
			return len(inputs), nil
		},
		versionFn: func() string { return "1.0.0" },
	}
	h := NewExtensionHandler(mock)

	body := jsonBody(t, dto.CreateExtensionDOMRowSnapshotsRequest{
		Items: []dto.CreateExtensionDOMRowSnapshotItemRequest{
			{
				PageType:    "campaign",
				TableRole:   "campaigns",
				RowKey:      "campaign-123",
				VisibleText: "Кампания 123 Расход 450",
				Cells:       json.RawMessage(`[{"index":0,"text":"Кампания 123"}]`),
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/extension/dom-row-snapshots", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreateDOMRowSnapshots(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	raw, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var accepted dto.ExtensionIngestAcceptedResponse
	require.NoError(t, json.Unmarshal(raw, &accepted))
	assert.Equal(t, 1, accepted.Accepted)
}

func TestExtensionEvidenceDebug_CampaignScope(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	mock := &mockExtensionService{
		evidenceDebugFn: func(_ context.Context, gotWorkspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceDebug, error) {
			assert.Equal(t, workspaceID, gotWorkspaceID)
			require.NotNil(t, input.CampaignID)
			assert.Equal(t, campaignID, *input.CampaignID)
			assert.Equal(t, "campaign", input.Scope)
			assert.Equal(t, int32(5), input.Limit)
			return &service.ExtensionEvidenceDebug{
				WorkspaceID:      gotWorkspaceID,
				Scope:            "campaign",
				CampaignID:       input.CampaignID,
				GeneratedAt:      now,
				LatestCapturedAt: &now,
				Counts: service.ExtensionEvidenceDebugCounts{
					NetworkCaptures: 1,
					BidSnapshots:    1,
				},
				DataStatus: service.ExtensionWidgetDataStatus{
					Source:             domain.SourceExtension,
					FreshnessState:     "fresh",
					Confidence:         0.8,
					Coverage:           "partial",
					ConfirmedInCabinet: true,
				},
				NetworkCaptures: []domain.ExtensionNetworkCapture{
					{
						ID:          uuid.New(),
						WorkspaceID: gotWorkspaceID,
						PageType:    "campaign",
						EndpointKey: "wb.query.bids",
						Payload:     json.RawMessage(`{"url":"https://cmp.wildberries.ru/adv/v0/normquery/get-bids"}`),
						CapturedAt:  now,
						CreatedAt:   now,
					},
				},
			}, nil
		},
		versionFn: func() string { return "1.0.0" },
	}
	h := NewExtensionHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/extension/evidence-debug?scope=campaign&campaign_id="+campaignID.String()+"&limit=5", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.EvidenceDebug(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	raw, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var got dto.ExtensionEvidenceDebugResponse
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "campaign", got.Scope)
	require.NotNil(t, got.CampaignID)
	assert.Equal(t, campaignID, *got.CampaignID)
	assert.Equal(t, 1, got.Counts.NetworkCaptures)
	require.Len(t, got.NetworkCaptures, 1)
	assert.Equal(t, "wb.query.bids", got.NetworkCaptures[0].EndpointKey)
}

func TestExtensionEvidenceSupportReport_CampaignScope(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	mock := &mockExtensionService{
		evidenceSupportReportFn: func(_ context.Context, gotWorkspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceSupportReport, error) {
			assert.Equal(t, workspaceID, gotWorkspaceID)
			require.NotNil(t, input.CampaignID)
			assert.Equal(t, campaignID, *input.CampaignID)
			assert.Equal(t, "campaign", input.Scope)
			return &service.ExtensionEvidenceSupportReport{
				WorkspaceID:      gotWorkspaceID,
				Scope:            "campaign",
				CampaignID:       input.CampaignID,
				GeneratedAt:      now,
				LatestCapturedAt: &now,
				Summary: service.ExtensionEvidenceSupportSummary{
					SourceLabel:        "Данные кабинета WB",
					Readiness:          "partial",
					CapturedSignals:    2,
					MissingSignals:     4,
					ConfirmedInCabinet: true,
					FreshnessState:     "fresh",
					Coverage:           "partial",
				},
				Sections: []service.ExtensionEvidenceSupportSection{
					{
						ID:               "network_captures",
						Title:            "Разрешенные ответы WB",
						Status:           "ready",
						Detail:           "Есть сохраненные реальные данные кабинета для проверки.",
						EvidenceCount:    1,
						LatestCapturedAt: &now,
					},
				},
				Checklist: []service.ExtensionEvidenceSupportChecklistItem{
					{
						ID:         "network_capture",
						Label:      "Сохранены разрешенные ответы WB",
						Done:       true,
						Detail:     "Ответы WB сохранены.",
						ActionPath: "refresh",
					},
				},
				Issues: []service.ExtensionWidgetIssue{
					{
						Stage:      "freshness",
						Severity:   "warning",
							Message:    "Нужно обновить сохраненные данные",
						ActionPath: "refresh",
					},
				},
				NextActions: []service.ExtensionWidgetAction{
					{
						ID:         "open_campaign",
						Label:      "Открыть кампанию WB",
						ActionPath: "wb://campaign/123",
						Tone:       "primary",
					},
				},
			}, nil
		},
		versionFn: func() string { return "1.0.0" },
	}
	h := NewExtensionHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/extension/evidence-debug/report?scope=campaign&campaign_id="+campaignID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.EvidenceSupportReport(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	raw, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var got dto.ExtensionEvidenceSupportReportResponse
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "campaign", got.Scope)
	require.NotNil(t, got.CampaignID)
	assert.Equal(t, campaignID, *got.CampaignID)
	assert.Equal(t, "partial", got.Summary.Readiness)
	require.Len(t, got.Sections, 1)
	assert.Equal(t, "network_captures", got.Sections[0].ID)
	require.Len(t, got.Checklist, 1)
	assert.True(t, got.Checklist[0].Done)
	require.Len(t, got.Issues, 1)
	assert.Equal(t, "Нужно обновить сохраненные данные", got.Issues[0].Message)
	assert.Equal(t, "refresh", got.Issues[0].ActionPath)
	require.Len(t, got.NextActions, 1)
	assert.Equal(t, "open_campaign", got.NextActions[0].ID)
	assert.Equal(t, "wb://campaign/123", got.NextActions[0].ActionPath)
	assert.Equal(t, "primary", got.NextActions[0].Tone)
}

func TestExtensionEvidenceDebug_InvalidProductID(t *testing.T) {
	h := NewExtensionHandler(&mockExtensionService{versionFn: func() string { return "1.0.0" }})
	req := httptest.NewRequest(http.MethodGet, "/extension/evidence-debug?scope=product&product_id=bad", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.EvidenceDebug(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
