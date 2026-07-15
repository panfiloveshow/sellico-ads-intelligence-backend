package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type campaignProductActionStub struct {
	campaignActionServicer
	updateFn func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.CampaignProductChangeInput) (wb.CampaignProductUpdateResult, error)
}

func (s campaignProductActionStub) UpdateCampaignProducts(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, input service.CampaignProductChangeInput) (wb.CampaignProductUpdateResult, error) {
	return s.updateFn(ctx, workspaceID, campaignID, actorID, input)
}

func TestCampaignActionHandlerUpdateProductsMapsRequestAndWBResult(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	actorID := uuid.New()
	stub := campaignProductActionStub{updateFn: func(_ context.Context, gotWorkspaceID, gotCampaignID, gotActorID uuid.UUID, input service.CampaignProductChangeInput) (wb.CampaignProductUpdateResult, error) {
		require.Equal(t, workspaceID, gotWorkspaceID)
		require.Equal(t, campaignID, gotCampaignID)
		require.Equal(t, actorID, gotActorID)
		require.Equal(t, []int64{111, 222}, input.Add)
		require.Equal(t, []int64{333}, input.Delete)
		return wb.CampaignProductUpdateResult{
			AdvertID: 77,
			NMs: wb.CampaignProductNMChangeResult{
				Added:   []int64{111, 222},
				Deleted: []int64{333},
			},
		}, nil
	}}
	handler := NewCampaignActionHandler(stub, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/campaigns/"+campaignID.String()+"/products", strings.NewReader(`{"add":[111,222],"delete":[333]}`))
	req = withCampaignActionIDs(req, workspaceID, campaignID, actorID)
	rec := httptest.NewRecorder()

	handler.UpdateProducts(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"data":{"advert_id":77,"added":[111,222],"deleted":[333],"sync_required":true}}`, rec.Body.String())
}

func TestCampaignActionHandlerUpdateProductsRejectsMalformedBody(t *testing.T) {
	called := false
	stub := campaignProductActionStub{updateFn: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.CampaignProductChangeInput) (wb.CampaignProductUpdateResult, error) {
		called = true
		return wb.CampaignProductUpdateResult{}, nil
	}}
	handler := NewCampaignActionHandler(stub, nil)
	campaignID := uuid.New()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/campaigns/"+campaignID.String()+"/products", strings.NewReader(`{"add":`))
	req = withCampaignActionIDs(req, uuid.New(), campaignID, uuid.New())
	rec := httptest.NewRecorder()

	handler.UpdateProducts(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, called)
}

func withCampaignActionIDs(req *http.Request, workspaceID, campaignID, actorID uuid.UUID) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", campaignID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	ctx = context.WithValue(ctx, middleware.UserIDKey, actorID)
	return req.WithContext(ctx)
}
