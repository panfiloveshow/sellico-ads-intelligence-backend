package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type adAdvancedHTTPStub struct {
	attrCalls       int
	prepareCalls    int
	receiptCalls    int
	approveCalls    int
	cancelCalls     int
	workspace, user uuid.UUID
}

func (s *adAdvancedHTTPStub) Attribution(_ context.Context, identity domain.ManagerAccess, w uuid.UUID, _, id string, from, to time.Time, camp *uuid.UUID) (*domain.ManagerAdAttributionReport, error) {
	s.attrCalls++
	s.workspace = w
	return &domain.ManagerAdAttributionReport{
		WorkspaceID:          identity.ExternalWorkspaceID,
		IntegrationID:        id,
		IntegrationName:      "WB Store",
		Marketplace:          "wb",
		CampaignID:           camp,
		Period:               domain.ManagerAdsPeriod{DateFrom: from.Format(time.DateOnly), DateTo: to.Format(time.DateOnly), Timezone: "Europe/Moscow"},
		Currency:             "RUB",
		RetrievedAt:          time.Now().UTC(),
		TotalSpendRub:        "1200.00",
		AttributedOrders:     6,
		AttributedRevenueRub: "18000.00",
		UnattributedSpendRub: "200.00",
		AttributionVerified:  true,
		ObservedDays:         2,
		MissingDaysCount:     0,
		Strategies:           []domain.ManagerAdStrategyBreakdown{},
	}, nil
}

func (s *adAdvancedHTTPStub) PrepareResolution(_ context.Context, identity domain.ManagerAccess, w, u uuid.UUID, _ string, opID uuid.UUID, integrationID, market string, campID uuid.UUID, reason, support string) (*domain.ManagerAdManualResolution, error) {
	s.prepareCalls++
	s.workspace = w
	s.user = u
	now := time.Now().UTC()
	return &domain.ManagerAdManualResolution{
		ID:                  uuid.New(),
		Fingerprint:         strings.Repeat("b", 64),
		ExternalWorkspaceID: identity.ExternalWorkspaceID,
		Status:              "awaiting_confirmation",
		OperationID:         opID,
		OperationType:       "ad_change",
		IntegrationID:       integrationID,
		IntegrationName:     "WB Store",
		Marketplace:         "wb",
		CampaignID:          campID,
		ExternalID:          "100500",
		Reason:              reason,
		SupportReference:    support,
		CreatedAt:           now,
		ExpiresAt:           now.Add(5 * time.Minute),
		ControlHoldActive:   true,
		ApprovalAvailable:   true,
	}, nil
}

func (s *adAdvancedHTTPStub) ResolutionReceipt(_ context.Context, identity domain.ManagerAccess, _, _, opID, resID uuid.UUID, _ string) (*domain.ManagerAdManualResolution, error) {
	s.receiptCalls++
	now := time.Now().UTC()
	return &domain.ManagerAdManualResolution{
		ID:                  resID,
		Fingerprint:         strings.Repeat("b", 64),
		ExternalWorkspaceID: identity.ExternalWorkspaceID,
		Status:              "awaiting_confirmation",
		OperationID:         opID,
		OperationType:       "ad_change",
		IntegrationID:       "28",
		IntegrationName:     "WB Store",
		Marketplace:         "wb",
		CampaignID:          uuid.New(),
		ExternalID:          "100500",
		Reason:              "Проверил статус кампании в кабинете WB",
		SupportReference:    "WB Ad Cabinet #100500 checked at 12:00",
		CreatedAt:           now,
		ExpiresAt:           now.Add(5 * time.Minute),
		ControlHoldActive:   true,
	}, nil
}

func (s *adAdvancedHTTPStub) ApproveResolution(_ context.Context, identity domain.ManagerAccess, _, _, opID, resID uuid.UUID, _, fp string, _ map[string]bool) (*domain.ManagerAdManualResolution, error) {
	s.approveCalls++
	now := time.Now().UTC()
	return &domain.ManagerAdManualResolution{
		ID:                  resID,
		Fingerprint:         fp,
		ExternalWorkspaceID: identity.ExternalWorkspaceID,
		Status:              "resolved_manually",
		OperationID:         opID,
		OperationType:       "ad_change",
		IntegrationID:       "28",
		IntegrationName:     "WB Store",
		Marketplace:         "wb",
		CampaignID:          uuid.New(),
		ExternalID:          "100500",
		Reason:              "Проверил статус кампании в кабинете WB",
		SupportReference:    "WB Ad Cabinet #100500 checked at 12:00",
		CreatedAt:           now.Add(-time.Minute),
		ExpiresAt:           now.Add(4 * time.Minute),
		ResolvedAt:          &now,
		ControlHoldActive:   false,
	}, nil
}

func (s *adAdvancedHTTPStub) CancelResolution(_ context.Context, identity domain.ManagerAccess, _, _, opID, resID uuid.UUID, _, fp string) (*domain.ManagerAdManualResolution, error) {
	s.cancelCalls++
	now := time.Now().UTC()
	return &domain.ManagerAdManualResolution{
		ID:                  resID,
		Fingerprint:         fp,
		ExternalWorkspaceID: identity.ExternalWorkspaceID,
		Status:              "cancelled",
		OperationID:         opID,
		OperationType:       "ad_change",
		IntegrationID:       "28",
		IntegrationName:     "WB Store",
		Marketplace:         "wb",
		CampaignID:          uuid.New(),
		ExternalID:          "100500",
		Reason:              "Проверил статус кампании в кабинете WB",
		SupportReference:    "WB Ad Cabinet #100500 checked at 12:00",
		CreatedAt:           now.Add(-time.Minute),
		ExpiresAt:           now.Add(4 * time.Minute),
		ControlHoldActive:   true,
	}, nil
}

func TestManagerAdAdvancedHTTPAttributionAndManualResolutionRoutes(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "marketing.manage", "integrations.view"}}
	svc := &adAdvancedHTTPStub{}
	router := NewRouter(RouterDeps{
		Authenticator:            auth,
		WorkspaceResolver:        auth,
		ManagerAccessVerifier:    verifier,
		ManagerAdAdvancedHandler: handler.NewManagerAdAdvancedHandler(svc),
	})

	req := func(method, url, body string, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		if authorized {
			r.Header.Set("Authorization", "Bearer personal-token")
			r.Header.Set("X-Sellico-Workspace", "3")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	attrURL := "/api/v1/manager/ads/attribution?integration_id=28&date_from=2026-10-01&date_to=2026-10-02"
	require.Equal(t, http.StatusOK, req(http.MethodGet, attrURL, "", true).Code)
	assert.Equal(t, 1, svc.attrCalls)
	require.Equal(t, http.StatusBadRequest, req(http.MethodGet, attrURL+"&campaign_id=bad", "", true).Code)
	require.Equal(t, http.StatusUnauthorized, req(http.MethodGet, attrURL, "", false).Code)

	opID := "019b1234-1111-7111-8111-111111111111"
	resID := "019b1234-2222-7222-8222-222222222222"
	prepURL := "/api/v1/manager/ads/operations/" + opID + "/resolution/prepare"
	prepBody := `{"integration_id":"28","marketplace":"wildberries","campaign_id":"019b1234-3333-7333-8333-333333333333","reason":"Таймаут ответа WB, проверил статус в кабинете","support_reference":"WB кабинет кампания 100500 на паузе"}`
	require.Equal(t, http.StatusOK, req(http.MethodPost, prepURL, prepBody, true).Code)
	assert.Equal(t, 1, svc.prepareCalls)
	assert.Equal(t, auth.user, svc.user)

	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, http.StatusForbidden, req(http.MethodPost, prepURL, prepBody, true).Code)
	require.Equal(t, http.StatusOK, req(http.MethodGet, "/api/v1/manager/ads/operations/"+opID+"/resolution/"+resID, "", true).Code)

	verifier.permissions = append(verifier.permissions, "marketing.manage")
	approveBody := `{"fingerprint":"` + strings.Repeat("b", 64) + `","confirmations":{"ad_cabinet_state_checked":true,"ad_no_pending_mutation":true,"ad_late_effect_risk_accepted":true,"ad_hold_release_understood":true}}`
	require.Equal(t, http.StatusOK, req(http.MethodPost, "/api/v1/manager/ads/operations/"+opID+"/resolution/"+resID+"/approve", approveBody, true).Code)
	assert.Equal(t, 1, svc.approveCalls)

	cancelBody := `{"fingerprint":"` + strings.Repeat("b", 64) + `"}`
	require.Equal(t, http.StatusOK, req(http.MethodPost, "/api/v1/manager/ads/operations/"+opID+"/resolution/"+resID+"/cancel", cancelBody, true).Code)
	assert.Equal(t, 1, svc.cancelCalls)
}
