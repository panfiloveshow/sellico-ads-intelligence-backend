package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type managerAdContextServicer interface {
	CampaignBudgetContext(context.Context, domain.ManagerAccess, uuid.UUID, string, string, uuid.UUID, string) (*domain.ManagerAdCampaignBudgetContext, error)
	ProductBidContext(context.Context, domain.ManagerAccess, uuid.UUID, string, string, uuid.UUID, string, string) (*domain.ManagerAdProductBidContext, error)
	CampaignContext(context.Context, domain.ManagerAccess, uuid.UUID, string, string, uuid.UUID) (*domain.ManagerAdContext, error)
}

func (h *ManagerAdContextHandler) CampaignBudgetContext(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified manager access required")
		return
	}
	for key, values := range r.URL.Query() {
		if (key != "integration_id" && key != "period") || len(values) != 1 {
			dto.WriteError(w, 400, "VALIDATION_ERROR", "provide only one store and daily or weekly period")
			return
		}
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	integration := r.URL.Query().Get("integration_id")
	period := r.URL.Query().Get("period")
	if err != nil || id == uuid.Nil || !managerIntegrationID.MatchString(integration) || (period != "daily" && period != "weekly") {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "specific store, campaign and budget period required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "budget context unavailable")
		return
	}
	response, err := h.service.CampaignBudgetContext(r.Context(), identity, workspace, principal.Token, integration, id, period)
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "personal session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, 403, "FORBIDDEN", "store access denied")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, response)
}

type ManagerAdContextHandler struct{ service managerAdContextServicer }

func NewManagerAdContextHandler(s managerAdContextServicer) *ManagerAdContextHandler {
	return &ManagerAdContextHandler{s}
}

func (h *ManagerAdContextHandler) Context(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, workspaceOK := middleware.WorkspaceIDFromContext(r.Context())
	principal, principalOK := middleware.PrincipalFromContext(r.Context())
	if !ok || !workspaceOK || !principalOK {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified manager access is required")
		return
	}
	for key, values := range r.URL.Query() {
		if key != "integration_id" || len(values) != 1 {
			dto.WriteError(w, 400, "VALIDATION_ERROR", "provide only one integration_id")
			return
		}
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	integration := r.URL.Query().Get("integration_id")
	if err != nil || id == uuid.Nil || !managerIntegrationID.MatchString(integration) {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "specific store and campaign required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "native advertising context unavailable")
		return
	}
	report, err := h.service.CampaignContext(r.Context(), identity, workspace, principal.Token, integration, id)
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "Sellico session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, 403, "FORBIDDEN", "store access denied")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, report)
}

func (h *ManagerAdContextHandler) ProductBidContext(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified manager access required")
		return
	}
	for key, values := range r.URL.Query() {
		if (key != "integration_id" && key != "product_id" && key != "placement") || len(values) != 1 {
			dto.WriteError(w, 400, "VALIDATION_ERROR", "specific store, product and placement required")
			return
		}
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	integration := r.URL.Query().Get("integration_id")
	product := r.URL.Query().Get("product_id")
	placement := r.URL.Query().Get("placement")
	if err != nil || id == uuid.Nil || !managerIntegrationID.MatchString(integration) || !managerIntegrationID.MatchString(product) || (placement != "search" && placement != "recommendations" && placement != "campaign_product") {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "specific store, campaign, product and placement required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "bid context unavailable")
		return
	}
	response, err := h.service.ProductBidContext(r.Context(), identity, workspace, principal.Token, integration, id, product, placement)
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "personal session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, 403, "FORBIDDEN", "store access denied")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, response)
}
