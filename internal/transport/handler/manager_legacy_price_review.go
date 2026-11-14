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
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type managerLegacyPriceReviewServicer interface {
	Context(context.Context, domain.ManagerAccess, uuid.UUID, string, string, string, string) (*domain.LegacyPriceJournalContext, error)
	Prepare(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, string, string, uuid.UUID, string, string, string) (*domain.LegacyPriceReviewOperation, error)
	Approve(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string, map[string]bool) (*domain.LegacyPriceReviewOperation, error)
	Cancel(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (*domain.LegacyPriceReviewOperation, error)
	Receipt(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.LegacyPriceReviewOperation, error)
}

type ManagerLegacyPriceReviewHandler struct {
	service managerLegacyPriceReviewServicer
}

func NewManagerLegacyPriceReviewHandler(s managerLegacyPriceReviewServicer) *ManagerLegacyPriceReviewHandler {
	return &ManagerLegacyPriceReviewHandler{s}
}

func (h *ManagerLegacyPriceReviewHandler) Context(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "context")
}
func (h *ManagerLegacyPriceReviewHandler) Prepare(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "prepare")
}
func (h *ManagerLegacyPriceReviewHandler) Receipt(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "receipt")
}
func (h *ManagerLegacyPriceReviewHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "approve")
}
func (h *ManagerLegacyPriceReviewHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, "cancel")
}

func (h *ManagerLegacyPriceReviewHandler) handle(w http.ResponseWriter, r *http.Request, mode string) {
	w.Header().Set("Cache-Control", "private, no-store")
	access, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	actor, aok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !aok || actor.UserID == uuid.Nil || actor.ExternalUserID != access.ExternalUserID {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified manager session required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "legacy price review unavailable")
		return
	}
	invalid := func() {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "exact stored review or scoped journal context required")
	}
	var result any
	var err error
	if mode == "context" {
		q := r.URL.Query()
		if len(q) < 2 || len(q) > 3 || len(q["integration_id"]) != 1 || len(q["resource_id"]) != 1 || !managerIntegrationID.MatchString(q.Get("integration_id")) || !priceExecutionResourceID.MatchString(q.Get("resource_id")) {
			invalid()
			return
		}
		for key, values := range q {
			if (key != "integration_id" && key != "resource_id" && key != "cursor") || len(values) != 1 || (key == "cursor" && (values[0] == "" || len(values[0]) > 512)) {
				invalid()
				return
			}
		}
		result, err = h.service.Context(r.Context(), access, workspace, actor.Token, q.Get("integration_id"), q.Get("resource_id"), q.Get("cursor"))
	} else {
		if len(r.URL.Query()) != 0 {
			invalid()
			return
		}
		var id uuid.UUID
		if mode != "prepare" {
			id, err = uuid.Parse(chi.URLParam(r, "id"))
			if err != nil || id == uuid.Nil {
				invalid()
				return
			}
		}
		if mode == "receipt" {
			result, err = h.service.Receipt(r.Context(), access, workspace, actor.UserID, id, actor.Token)
		} else {
			r.Body = http.MaxBytesReader(w, r.Body, 16384)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			switch mode {
			case "prepare":
				var b struct {
					IntegrationID      string `json:"integration_id"`
					ResourceID         string `json:"resource_id"`
					CommandID          string `json:"command_id"`
					ContextFingerprint string `json:"context_fingerprint"`
					Reason             string `json:"reason"`
					SupportReference   string `json:"support_reference"`
				}
				if d.Decode(&b) != nil || d.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(b.IntegrationID) || !priceExecutionResourceID.MatchString(b.ResourceID) || !priceExecutionFingerprint.MatchString(b.ContextFingerprint) {
					invalid()
					return
				}
				command, e := uuid.Parse(b.CommandID)
				if e != nil || command == uuid.Nil {
					invalid()
					return
				}
				result, err = h.service.Prepare(r.Context(), access, workspace, actor.UserID, actor.Token, b.IntegrationID, b.ResourceID, command, b.ContextFingerprint, b.Reason, b.SupportReference)
			case "approve":
				var b struct {
					Fingerprint   string          `json:"fingerprint"`
					Confirmations map[string]bool `json:"confirmations"`
				}
				if d.Decode(&b) != nil || d.Decode(&struct{}{}) != io.EOF || !priceExecutionFingerprint.MatchString(b.Fingerprint) {
					invalid()
					return
				}
				result, err = h.service.Approve(r.Context(), access, workspace, actor.UserID, id, actor.Token, b.Fingerprint, b.Confirmations)
			case "cancel":
				var b struct {
					Fingerprint string `json:"fingerprint"`
				}
				if d.Decode(&b) != nil || d.Decode(&struct{}{}) != io.EOF || !priceExecutionFingerprint.MatchString(b.Fingerprint) {
					invalid()
					return
				}
				result, err = h.service.Cancel(r.Context(), access, workspace, actor.UserID, id, actor.Token, b.Fingerprint)
			}
		}
	}
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "personal session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, 403, "FORBIDDEN", "native price account access denied")
		return
	}
	if errors.Is(err, sqlcgen.ErrLegacyPriceReviewChanged) || errors.Is(err, sqlcgen.ErrPriceExecutionAccountHeld) || errors.Is(err, sqlcgen.ErrPriceExecutionControlChanged) || errors.Is(err, sqlcgen.ErrLegacyPriceActiveWrite) || errors.Is(err, sqlcgen.ErrLegacyPriceBindingUnknown) {
		dto.WriteError(w, 409, "REVIEW_CHANGED", "review, account restriction or active price work must be checked again")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, 200, result)
}
