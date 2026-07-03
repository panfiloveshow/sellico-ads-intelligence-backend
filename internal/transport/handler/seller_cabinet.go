package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// sellerCabinetServicer is the interface the SellerCabinetHandler depends on.
type sellerCabinetServicer interface {
	Create(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error)
	List(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, filter service.SellerCabinetListFilter, limit, offset int32) ([]domain.SellerCabinet, error)
	Get(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error)
	ListCampaigns(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Campaign, error)
	ListProducts(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Product, error)
	GetCommunicationReputation(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, nmID int64, isAnswered bool, take int) (*service.SellerCabinetCommunicationReputation, error)
	Delete(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) error
	TriggerSellerCabinetSync(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*service.SyncTriggerResult, error)
}

// SellerCabinetHandler handles seller cabinet HTTP endpoints.
type SellerCabinetHandler struct {
	svc sellerCabinetServicer
}

// NewSellerCabinetHandler creates a new SellerCabinetHandler.
func NewSellerCabinetHandler(svc sellerCabinetServicer) *SellerCabinetHandler {
	return &SellerCabinetHandler{svc: svc}
}

// Create handles POST /seller-cabinets.
func (h *SellerCabinetHandler) Create(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreateSellerCabinetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}

	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	sc, err := h.svc.Create(r.Context(), workspaceID, req.Name, req.APIToken)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.SellerCabinetFromDomain(*sc))
}

// List handles GET /seller-cabinets.
func (h *SellerCabinetHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	pg := pagination.Parse(r)

	cabinets, err := h.svc.List(r.Context(), principal.Token, workspaceRef, workspaceID, service.SellerCabinetListFilter{
		Status: r.URL.Query().Get("status"),
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.SellerCabinetResponse, len(cabinets))
	for i, sc := range cabinets {
		items[i] = dto.SellerCabinetFromDomain(sc)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// Get handles GET /seller-cabinets/{id}.
func (h *SellerCabinetHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	sc, err := h.svc.Get(r.Context(), principal.Token, workspaceRef, workspaceID, chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.SellerCabinetFromDomain(*sc))
}

// ListCampaigns handles GET /seller-cabinets/{id}/campaigns.
func (h *SellerCabinetHandler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	pg := pagination.Parse(r)
	campaigns, err := h.svc.ListCampaigns(r.Context(), principal.Token, workspaceRef, workspaceID, chi.URLParam(r, "id"), int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.CampaignResponse, len(campaigns))
	for i, campaign := range campaigns {
		items[i] = dto.CampaignFromDomain(campaign)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// ListProducts handles GET /seller-cabinets/{id}/products.
func (h *SellerCabinetHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	pg := pagination.Parse(r)
	products, err := h.svc.ListProducts(r.Context(), principal.Token, workspaceRef, workspaceID, chi.URLParam(r, "id"), int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.ProductResponse, len(products))
	for i, product := range products {
		items[i] = dto.ProductFromDomain(product)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// CommunicationReputation handles GET /seller-cabinets/{id}/communication/reputation.
func (h *SellerCabinetHandler) CommunicationReputation(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	nmID, err := strconv.ParseInt(r.URL.Query().Get("nm_id"), 10, 64)
	if err != nil || nmID <= 0 {
		writeAppError(w, apperror.New(apperror.ErrValidation, "nm_id is required"))
		return
	}

	take := 20
	if rawTake := r.URL.Query().Get("take"); rawTake != "" {
		parsed, parseErr := strconv.Atoi(rawTake)
		if parseErr != nil || parsed <= 0 || parsed > 100 {
			writeAppError(w, apperror.New(apperror.ErrValidation, "take must be between 1 and 100"))
			return
		}
		take = parsed
	}

	isAnswered := false
	if rawAnswered := r.URL.Query().Get("is_answered"); rawAnswered != "" {
		parsed, parseErr := strconv.ParseBool(rawAnswered)
		if parseErr != nil {
			writeAppError(w, apperror.New(apperror.ErrValidation, "is_answered must be true or false"))
			return
		}
		isAnswered = parsed
	}

	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	report, err := h.svc.GetCommunicationReputation(r.Context(), principal.Token, workspaceRef, workspaceID, chi.URLParam(r, "id"), nmID, isAnswered, take)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.SellerCabinetCommunicationReputationFromService(*report))
}

// Delete handles DELETE /seller-cabinets/{id}.
func (h *SellerCabinetHandler) Delete(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}

	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	if err := h.svc.Delete(r.Context(), userID, principal.Token, workspaceRef, workspaceID, chi.URLParam(r, "id")); err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, nil)
}

// TriggerSync handles POST /seller-cabinets/{id}/sync.
func (h *SellerCabinetHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}

	principal, _ := middleware.PrincipalFromContext(r.Context())
	workspaceRef, _ := middleware.WorkspaceRefFromContext(r.Context())

	result, err := h.svc.TriggerSellerCabinetSync(r.Context(), userID, principal.Token, workspaceRef, workspaceID, chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusAccepted, dto.SyncTriggerResponse{
		TaskType:    result.TaskType,
		Status:      result.Status,
		WorkspaceID: result.WorkspaceID,
		CabinetID:   result.CabinetID,
		JobRunID:    result.JobRunID,
	})
}
