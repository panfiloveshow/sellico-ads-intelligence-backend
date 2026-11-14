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

type managerPriceExecutionControlServicer interface {
	Context(context.Context, domain.ManagerAccess, uuid.UUID, string, string, string) (*domain.PriceExecutionControlContext, error)
	Prepare(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, string, string, string, string, string) (*domain.PriceExecutionControlOperation, error)
	Approve(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string, map[string]bool) (*domain.PriceExecutionControlOperation, error)
	Cancel(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (*domain.PriceExecutionControlOperation, error)
	Receipt(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.PriceExecutionControlOperation, error)
}
type ManagerPriceExecutionControlHandler struct {
	service managerPriceExecutionControlServicer
}

func NewManagerPriceExecutionControlHandler(s managerPriceExecutionControlServicer) *ManagerPriceExecutionControlHandler {
	return &ManagerPriceExecutionControlHandler{s}
}
func (h *ManagerPriceExecutionControlHandler) Context(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "context")
}
func (h *ManagerPriceExecutionControlHandler) Prepare(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "prepare")
}
func (h *ManagerPriceExecutionControlHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "approve")
}
func (h *ManagerPriceExecutionControlHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "cancel")
}
func (h *ManagerPriceExecutionControlHandler) Receipt(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "receipt")
}

var priceExecutionResourceID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var priceExecutionFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (h *ManagerPriceExecutionControlHandler) handle(w http.ResponseWriter, r *http.Request, mode string) {
	w.Header().Set("Cache-Control", "private, no-store")
	access, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok || principal.UserID == uuid.Nil || principal.ExternalUserID != access.ExternalUserID {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified personal manager session required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "price account controls unavailable")
		return
	}
	invalid := func(message string) { dto.WriteError(w, 400, "VALIDATION_ERROR", message) }
	var response any
	var err error
	if mode == "context" {
		q := r.URL.Query()
		if len(q) != 2 || len(q["integration_id"]) != 1 || len(q["resource_id"]) != 1 || !managerIntegrationID.MatchString(q.Get("integration_id")) {
			invalid("one store and one proof product required")
			return
		}
		resource := q.Get("resource_id")
		if !priceExecutionResourceID.MatchString(resource) {
			invalid("specific native price product required")
			return
		}
		response, err = h.service.Context(r.Context(), access, workspace, principal.Token, q.Get("integration_id"), resource)
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
					IntegrationID      string `json:"integration_id"`
					ResourceID         string `json:"resource_id"`
					TargetMode         string `json:"target_mode"`
					Reason             string `json:"reason"`
					ContextFingerprint string `json:"context_fingerprint"`
				}
				if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(body.IntegrationID) || !priceExecutionResourceID.MatchString(body.ResourceID) || (body.TargetMode != "stopped" && body.TargetMode != "manual_only") || !priceExecutionFingerprint.MatchString(body.ContextFingerprint) || body.Reason == "" {
					invalid("exact account context, target mode and personal reason required")
					return
				}
				response, err = h.service.Prepare(r.Context(), access, workspace, principal.UserID, principal.Token, body.IntegrationID, body.ResourceID, body.TargetMode, body.Reason, body.ContextFingerprint)
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
					if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !priceExecutionFingerprint.MatchString(body.Fingerprint) {
						invalid("exact proposal fingerprint and personal checks required")
						return
					}
					response, err = h.service.Approve(r.Context(), access, workspace, principal.UserID, id, principal.Token, body.Fingerprint, body.Confirmations)
				} else {
					var body struct {
						Fingerprint string `json:"fingerprint"`
					}
					if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !priceExecutionFingerprint.MatchString(body.Fingerprint) {
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
		dto.WriteError(w, 403, "FORBIDDEN", "price account access denied")
		return
	}
	if errors.Is(err, sqlcgen.ErrPriceExecutionControlChanged) || errors.Is(err, sqlcgen.ErrPriceExecutionAccountHeld) || errors.Is(err, sqlcgen.ErrPriceExecutionStopped) {
		dto.WriteError(w, 409, "CONTROL_CHANGED", "account control changed or is currently being verified")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, response)
}
