package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

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

type strategyServicer interface {
	Create(ctx context.Context, workspaceID uuid.UUID, input domain.Strategy) (*domain.Strategy, error)
	Get(ctx context.Context, workspaceID, strategyID uuid.UUID) (*domain.Strategy, error)
	List(ctx context.Context, workspaceID uuid.UUID, sellerCabinetID *uuid.UUID, limit, offset int32) ([]domain.Strategy, error)
	ListShadowDecisions(ctx context.Context, workspaceID, strategyID uuid.UUID, limit, offset int32) ([]domain.BidDecisionObservation, error)
	Update(ctx context.Context, workspaceID, strategyID uuid.UUID, input domain.Strategy) (*domain.Strategy, error)
	Delete(ctx context.Context, workspaceID, strategyID uuid.UUID) error
	AttachBinding(ctx context.Context, workspaceID, strategyID uuid.UUID, campaignID, productID *uuid.UUID) (*domain.StrategyBinding, error)
	DetachBinding(ctx context.Context, workspaceID, bindingID uuid.UUID) error
	Activity(ctx context.Context, workspaceID, strategyID uuid.UUID) (*domain.StrategyActivity, error)
	ListEvaluationRuns(ctx context.Context, workspaceID, strategyID uuid.UUID, limit, offset int32) ([]domain.StrategyEvaluationRun, error)
	GetEvaluationRun(ctx context.Context, workspaceID, strategyID, runID uuid.UUID) (*domain.StrategyEvaluationRun, error)
	UpdateBindingRollout(ctx context.Context, workspaceID, strategyID, bindingID, actorID uuid.UUID, input service.StrategyRolloutUpdate) (*domain.StrategyBindingRollout, error)
	UpdateStrategyRollout(ctx context.Context, workspaceID, strategyID, actorID uuid.UUID, input service.StrategyRolloutUpdate) ([]domain.StrategyBindingRollout, error)
}

type StrategyHandler struct {
	svc        strategyServicer
	runTrigger func(context.Context, uuid.UUID) error
}

func (h *StrategyHandler) WithRunTrigger(trigger func(context.Context, uuid.UUID) error) *StrategyHandler {
	h.runTrigger = trigger
	return h
}

func NewStrategyHandler(svc strategyServicer) *StrategyHandler {
	return &StrategyHandler{svc: svc}
}

func validateStrategyInput(input domain.Strategy) map[string]string {
	errors := map[string]string{}
	if strings.TrimSpace(input.Name) == "" {
		errors["name"] = "is required"
	}
	if input.SellerCabinetID == uuid.Nil {
		errors["seller_cabinet_id"] = "is required"
	}
	switch input.Type {
	case domain.StrategyTypeACoS,
		domain.StrategyTypeROAS,
		domain.StrategyTypeAntiSliv,
		domain.StrategyTypeDayparting,
		domain.StrategyTypeSearchPlaybook:
	default:
		errors["type"] = "must be one of: acos, roas, anti_sliv, dayparting, search_playbook"
	}
	params := input.Params
	if params.MinBid < 0 {
		errors["params.min_bid"] = "must be non-negative"
	}
	if params.MaxBid < 0 {
		errors["params.max_bid"] = "must be non-negative"
	}
	if params.MaxCPC < 0 {
		errors["params.max_cpc"] = "must be non-negative"
	}
	if params.MaxCPO < 0 {
		errors["params.max_cpo"] = "must be non-negative"
	}
	if params.MaxACoS < 0 || params.MaxACoS > 1000 {
		errors["params.max_acos"] = "must be between 0 and 1000"
	}
	if params.AutomationLevel < 0 || params.AutomationLevel > 4 {
		errors["params.automation_level"] = "must be between 1 and 4"
	}
	if params.MinBid > 0 && params.MaxBid > 0 && params.MinBid > params.MaxBid {
		errors["params.min_bid"] = "must be less than or equal to max_bid"
	}
	if params.MaxChangePercent < 0 || params.MaxChangePercent > 100 {
		errors["params.max_change_percent"] = "must be between 0 and 100"
	}
	if params.LookbackDays < 0 {
		errors["params.lookback_days"] = "must be non-negative"
	}
	if params.MinClicks < 0 {
		errors["params.min_clicks"] = "must be non-negative"
	}
	if params.MinStockForIncrease < 0 {
		errors["params.min_stock_for_increase"] = "must be non-negative"
	}
	if params.CooldownMinutes < 0 {
		errors["params.cooldown_minutes"] = "must be non-negative"
	}
	if params.MaxChangesPerDay < 0 {
		errors["params.max_changes_per_day"] = "must be non-negative"
	}
	if params.MaxDataAgeHours < 0 {
		errors["params.max_data_age_hours"] = "must be non-negative"
	}
	switch input.Type {
	case domain.StrategyTypeACoS:
		if params.TargetACoS <= 0 || params.TargetACoS > 1000 {
			errors["params.target_acos"] = "must be greater than 0 and at most 1000"
		}
	case domain.StrategyTypeROAS:
		if params.TargetROAS <= 0 || params.TargetROAS > 1000 {
			errors["params.target_roas"] = "must be greater than 0 and at most 1000"
		}
	case domain.StrategyTypeAntiSliv:
		if params.MaxACoS <= 0 {
			errors["params.max_acos"] = "must be greater than 0"
		}
	}
	return errors
}

func (h *StrategyHandler) Create(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var input domain.Strategy
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	if validationErrors := validateStrategyInput(input); len(validationErrors) > 0 {
		dto.WriteValidationError(w, validationErrors)
		return
	}

	strategy, err := h.svc.Create(r.Context(), workspaceID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, strategy)
}

func (h *StrategyHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	strategy, err := h.svc.Get(r.Context(), workspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, strategy)
}

func (h *StrategyHandler) ListShadowDecisions(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	pg := pagination.Parse(r)
	limit, offset := pg.SQLLimitOffset()
	items, err := h.svc.ListShadowDecisions(r.Context(), workspaceID, strategyID, limit, offset)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items)),
	})
}

func (h *StrategyHandler) Activity(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	activity, err := h.svc.Activity(r.Context(), workspaceID, strategyID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, activity)
}

func (h *StrategyHandler) ListEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	pg := pagination.Parse(r)
	// #nosec G115 -- pagination.Parse bounds both values to math.MaxInt32.
	items, err := h.svc.ListEvaluationRuns(r.Context(), workspaceID, strategyID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items))})
}

func (h *StrategyHandler) GetEvaluationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	runID, err := uuid.Parse(chi.URLParam(r, "runId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid run id")
		return
	}
	run, err := h.svc.GetEvaluationRun(r.Context(), workspaceID, strategyID, runID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, run)
}

func (h *StrategyHandler) TriggerRun(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	if _, err := h.svc.Get(r.Context(), workspaceID, strategyID); err != nil {
		writeAppError(w, err)
		return
	}
	if h.runTrigger == nil {
		writeAppError(w, apperror.New(apperror.ErrInternal, "strategy run trigger is unavailable"))
		return
	}
	if err := h.runTrigger(r.Context(), workspaceID); err != nil {
		writeAppError(w, apperror.New(apperror.ErrInternal, "failed to enqueue strategy run"))
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "enqueued", "strategy_id": strategyID})
}

func decodeStrategyRollout(w http.ResponseWriter, r *http.Request) (service.StrategyRolloutUpdate, bool) {
	var input service.StrategyRolloutUpdate
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return input, false
	}
	return input, true
}

func requestActorID(r *http.Request) (uuid.UUID, bool) {
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	return actorID, ok && actorID != uuid.Nil
}

func (h *StrategyHandler) UpdateRollout(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	actorID, ok := requestActorID(r)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}
	input, ok := decodeStrategyRollout(w, r)
	if !ok {
		return
	}
	rollouts, err := h.svc.UpdateStrategyRollout(r.Context(), workspaceID, strategyID, actorID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, rollouts)
}

func (h *StrategyHandler) UpdateBindingRollout(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	strategyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}
	bindingID, err := uuid.Parse(chi.URLParam(r, "bindingId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid binding id")
		return
	}
	actorID, ok := requestActorID(r)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}
	input, ok := decodeStrategyRollout(w, r)
	if !ok {
		return
	}
	rollout, err := h.svc.UpdateBindingRollout(r.Context(), workspaceID, strategyID, bindingID, actorID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, rollout)
}

func (h *StrategyHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	pg := pagination.Parse(r)
	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}
	strategies, err := h.svc.List(r.Context(), workspaceID, sellerCabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, strategies, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(strategies)),
	})
}

func (h *StrategyHandler) Update(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	var input domain.Strategy
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	if validationErrors := validateStrategyInput(input); len(validationErrors) > 0 {
		dto.WriteValidationError(w, validationErrors)
		return
	}

	strategy, err := h.svc.Update(r.Context(), workspaceID, id, input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, strategy)
}

func (h *StrategyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	if err := h.svc.Delete(r.Context(), workspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type attachRequest struct {
	CampaignID *uuid.UUID `json:"campaign_id"`
	ProductID  *uuid.UUID `json:"product_id"`
}

func (h *StrategyHandler) Attach(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	var req attachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	binding, err := h.svc.AttachBinding(r.Context(), workspaceID, id, req.CampaignID, req.ProductID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, binding)
}

func (h *StrategyHandler) Detach(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	bindingID, err := uuid.Parse(chi.URLParam(r, "bindingId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid binding id")
		return
	}

	if err := h.svc.DetachBinding(r.Context(), workspaceID, bindingID); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
