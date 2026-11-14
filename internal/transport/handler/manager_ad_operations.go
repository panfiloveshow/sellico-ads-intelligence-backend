package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type managerAdOperationsServicer interface {
	PrepareBudget(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, string, uuid.UUID, string, domain.ManagerAdBudgetTarget, *domain.ManagerAdBudgetConstraints) (*domain.ManagerAdOperation, error)
	PrepareBid(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, string, uuid.UUID, string, string, string, domain.ManagerAdBidConstraints) (*domain.ManagerAdOperation, error)
	Prepare(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, string, uuid.UUID, string) (*domain.ManagerAdOperation, error)
	Approve(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string, map[string]bool) (*domain.ManagerAdOperation, error)
	Cancel(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (*domain.ManagerAdOperation, error)
	Receipt(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.ManagerAdOperation, error)
}
type ManagerAdOperationsHandler struct{ service managerAdOperationsServicer }

func NewManagerAdOperationsHandler(s managerAdOperationsServicer) *ManagerAdOperationsHandler {
	return &ManagerAdOperationsHandler{s}
}
func (h *ManagerAdOperationsHandler) PrepareBid(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "prepare_bid")
}
func (h *ManagerAdOperationsHandler) PrepareBudget(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "prepare_budget")
}
func (h *ManagerAdOperationsHandler) Prepare(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "prepare")
}
func (h *ManagerAdOperationsHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "approve")
}
func (h *ManagerAdOperationsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "cancel")
}
func (h *ManagerAdOperationsHandler) Receipt(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "receipt")
}
func (h *ManagerAdOperationsHandler) handle(w http.ResponseWriter, r *http.Request, mode string) {
	access, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok || principal.ExternalUserID != access.ExternalUserID {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified personal manager session required")
		return
	}
	if len(r.URL.Query()) != 0 {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "query parameters are not supported")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "advertising operations unavailable")
		return
	}
	var id uuid.UUID
	var err error
	if mode != "prepare" && mode != "prepare_bid" && mode != "prepare_budget" {
		id, err = uuid.Parse(chi.URLParam(r, "id"))
		if err != nil || id == uuid.Nil {
			dto.WriteError(w, 400, "VALIDATION_ERROR", "specific operation required")
			return
		}
	}
	var response *domain.ManagerAdOperation
	if mode == "receipt" {
		response, err = h.service.Receipt(r.Context(), access, workspace, principal.UserID, id, principal.Token)
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if mode == "prepare_budget" {
			var body struct {
				IntegrationID string    `json:"integration_id"`
				CampaignID    uuid.UUID `json:"campaign_id"`
				Period        string    `json:"period"`
				Target        struct {
					Enabled          *bool   `json:"enabled"`
					AmountRub        *string `json:"amount_rub"`
					CarryOverEnabled *bool   `json:"carry_over_enabled"`
				} `json:"target"`
				Constraints *domain.ManagerAdBudgetConstraints `json:"constraints"`
			}
			if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(body.IntegrationID) || body.CampaignID == uuid.Nil || (body.Period != "daily" && body.Period != "weekly") || body.Target.Enabled == nil {
				dto.WriteError(w, 400, "VALIDATION_ERROR", "one exact budget target with explicit enabled flag and period required")
				return
			}
			if *body.Target.Enabled {
				if body.Target.AmountRub == nil || *body.Target.AmountRub == "" || body.Constraints == nil || body.Constraints.MaxBudgetRub == "" || body.Constraints.MaxAbsoluteChangeRub == "" {
					dto.WriteError(w, 400, "VALIDATION_ERROR", "amount and two explicit personal nominal budget limits required")
					return
				}
			} else if body.Target.AmountRub != nil || body.Target.CarryOverEnabled != nil || body.Constraints != nil {
				dto.WriteError(w, 400, "VALIDATION_ERROR", "removing a budget limit must not contain hidden amounts or numeric caps")
				return
			}
			response, err = h.service.PrepareBudget(r.Context(), access, workspace, principal.UserID, principal.Token, body.IntegrationID, body.CampaignID, body.Period, domain.ManagerAdBudgetTarget{Enabled: *body.Target.Enabled, AmountRub: body.Target.AmountRub, CarryOverEnabled: body.Target.CarryOverEnabled}, body.Constraints)
		} else if mode == "prepare_bid" {
			var body struct {
				IntegrationID string                         `json:"integration_id"`
				CampaignID    uuid.UUID                      `json:"campaign_id"`
				ProductID     string                         `json:"product_id"`
				Placement     string                         `json:"placement"`
				TargetBidRub  string                         `json:"target_bid_rub"`
				Constraints   domain.ManagerAdBidConstraints `json:"constraints"`
			}
			if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(body.IntegrationID) || body.CampaignID == uuid.Nil || !managerIntegrationID.MatchString(body.ProductID) || body.TargetBidRub == "" || body.Constraints.MaxBidRub == "" || body.Constraints.MaxChangePercent == "" || (body.Placement != "search" && body.Placement != "recommendations" && body.Placement != "campaign_product") {
				dto.WriteError(w, 400, "VALIDATION_ERROR", "one exact product bid and explicit personal constraints required")
				return
			}
			response, err = h.service.PrepareBid(r.Context(), access, workspace, principal.UserID, principal.Token, body.IntegrationID, body.CampaignID, body.ProductID, body.Placement, body.TargetBidRub, body.Constraints)
		} else if mode == "prepare" {
			var body struct {
				IntegrationID string    `json:"integration_id"`
				CampaignID    uuid.UUID `json:"campaign_id"`
				Action        string    `json:"action"`
			}
			if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(body.IntegrationID) || body.CampaignID == uuid.Nil || (body.Action != "pause" && body.Action != "resume" && body.Action != "handover" && body.Action != "launch") {
				dto.WriteError(w, 400, "VALIDATION_ERROR", "one exact advertising control action is required")
				return
			}
			response, err = h.service.Prepare(r.Context(), access, workspace, principal.UserID, principal.Token, body.IntegrationID, body.CampaignID, body.Action)
		} else if mode == "approve" {
			var body struct {
				Fingerprint   string          `json:"fingerprint"`
				Confirmations map[string]bool `json:"confirmations"`
			}
			if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(body.Fingerprint) != 64 {
				dto.WriteError(w, 400, "VALIDATION_ERROR", "exact proposal fingerprint and personal checks required")
				return
			}
			response, err = h.service.Approve(r.Context(), access, workspace, principal.UserID, id, principal.Token, body.Fingerprint, body.Confirmations)
		} else {
			var body struct {
				Fingerprint string `json:"fingerprint"`
			}
			if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(body.Fingerprint) != 64 {
				dto.WriteError(w, 400, "VALIDATION_ERROR", "exact proposal fingerprint required")
				return
			}
			response, err = h.service.Cancel(r.Context(), access, workspace, principal.UserID, id, principal.Token, body.Fingerprint)
		}
	}
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "personal session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, 403, "FORBIDDEN", "advertising access denied")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, response)
}
