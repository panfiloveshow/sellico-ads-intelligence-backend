package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type managerAdExecutionControlServicer interface {
	Context(context.Context, domain.ManagerAccess, uuid.UUID, string, string, uuid.UUID) (*domain.AdExecutionControlContext, error)
	Prepare(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, string, uuid.UUID, string, string, string) (*domain.AdExecutionControlOperation, error)
	Approve(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string, map[string]bool) (*domain.AdExecutionControlOperation, error)
	Cancel(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (*domain.AdExecutionControlOperation, error)
	Receipt(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.AdExecutionControlOperation, error)
}
type ManagerAdExecutionControlHandler struct {
	service managerAdExecutionControlServicer
}

func NewManagerAdExecutionControlHandler(s managerAdExecutionControlServicer) *ManagerAdExecutionControlHandler {
	return &ManagerAdExecutionControlHandler{s}
}
func (h *ManagerAdExecutionControlHandler) Context(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "context")
}
func (h *ManagerAdExecutionControlHandler) Prepare(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "prepare")
}
func (h *ManagerAdExecutionControlHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "approve")
}
func (h *ManagerAdExecutionControlHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "cancel")
}
func (h *ManagerAdExecutionControlHandler) Receipt(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "receipt")
}

var adExecutionFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (h *ManagerAdExecutionControlHandler) handle(w http.ResponseWriter, r *http.Request, mode string) {
	w.Header().Set("Cache-Control", "private, no-store")
	access, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok || principal.UserID == uuid.Nil || principal.ExternalUserID != access.ExternalUserID {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified personal manager session required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "advertising account controls unavailable")
		return
	}
	invalid := func(message string) { dto.WriteError(w, 400, "VALIDATION_ERROR", message) }
	var response any
	var err error
	if mode == "context" {
		q := r.URL.Query()
		if len(q) != 2 || len(q["integration_id"]) != 1 || len(q["campaign_id"]) != 1 || !managerIntegrationID.MatchString(q.Get("integration_id")) {
			invalid("one store and one proof campaign required")
			return
		}
		campaign, parseErr := uuid.Parse(q.Get("campaign_id"))
		if parseErr != nil || campaign == uuid.Nil {
			invalid("specific proof campaign required")
			return
		}
		response, err = h.service.Context(r.Context(), access, workspace, principal.Token, q.Get("integration_id"), campaign)
	} else {
		if len(r.URL.Query()) != 0 {
			invalid("query parameters are not supported")
			return
		}
		if mode == "receipt" {
			id, parseErr := uuid.Parse(chi.URLParam(r, "id"))
			if parseErr != nil || id == uuid.Nil {
				invalid("specific control proposal required")
				return
			}
			response, err = h.service.Receipt(r.Context(), access, workspace, principal.UserID, id, principal.Token)
		} else {
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if mode == "prepare" {
				var body struct {
					IntegrationID      string    `json:"integration_id"`
					CampaignID         uuid.UUID `json:"campaign_id"`
					TargetMode         string    `json:"target_mode"`
					Reason             string    `json:"reason"`
					ContextFingerprint string    `json:"context_fingerprint"`
				}
				if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(body.IntegrationID) || body.CampaignID == uuid.Nil || (body.TargetMode != "stopped" && body.TargetMode != "manual_only") || !adExecutionFingerprint.MatchString(body.ContextFingerprint) || body.Reason == "" {
					invalid("exact account context, target mode and personal reason required")
					return
				}
				response, err = h.service.Prepare(r.Context(), access, workspace, principal.UserID, principal.Token, body.IntegrationID, body.CampaignID, body.TargetMode, body.Reason, body.ContextFingerprint)
			} else {
				id, parseErr := uuid.Parse(chi.URLParam(r, "id"))
				if parseErr != nil || id == uuid.Nil {
					invalid("specific control proposal required")
					return
				}
				if mode == "approve" {
					var body struct {
						Fingerprint   string          `json:"fingerprint"`
						Confirmations map[string]bool `json:"confirmations"`
					}
					if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !adExecutionFingerprint.MatchString(body.Fingerprint) {
						invalid("exact proposal fingerprint and personal checks required")
						return
					}
					response, err = h.service.Approve(r.Context(), access, workspace, principal.UserID, id, principal.Token, body.Fingerprint, body.Confirmations)
				} else {
					var body struct {
						Fingerprint string `json:"fingerprint"`
					}
					if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !adExecutionFingerprint.MatchString(body.Fingerprint) {
						invalid("exact proposal fingerprint required")
						return
					}
					response, err = h.service.Cancel(r.Context(), access, workspace, principal.UserID, id, principal.Token, body.Fingerprint)
				}
			}
		}
	}
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "personal session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, 403, "FORBIDDEN", "advertising account access denied")
		return
	}
	if errors.Is(err, sqlcgen.ErrAdExecutionControlChanged) || errors.Is(err, sqlcgen.ErrAdResourceHeld) || errors.Is(err, sqlcgen.ErrAdExecutionStopped) {
		dto.WriteError(w, 409, "CONTROL_CHANGED", "account control changed or is currently being verified")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, response)
}
