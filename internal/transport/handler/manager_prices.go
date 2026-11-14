package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type ManagerPriceHandler struct{ executor *service.ManagerPriceExecutor }

func NewManagerPriceHandler(executor *service.ManagerPriceExecutor) *ManagerPriceHandler {
	return &ManagerPriceHandler{executor: executor}
}

func (h *ManagerPriceHandler) Apply(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var command service.ApprovedPriceCommand
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "one exact approved price command is required")
		return
	}
	if h == nil || h.executor == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "manager price executor unavailable")
		return
	}
	receipt, err := h.executor.Apply(r.Context(), access, workspace, principal.UserID, principal.Token, command)
	// A durable native receipt is authoritative even when transport lost its
	// response. Raw vendor errors cannot turn an unknown outcome into failure.
	if receipt != nil {
		dto.WriteJSON(w, 200, receipt)
		return
	}
	if err != nil {
		dto.WriteError(w, 409, "PRICE_NOT_CONFIRMED", "price command was not confirmed; read its operation before any further action")
		return
	}
	dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "manager price receipt unavailable")
}

func (h *ManagerPriceHandler) Receipt(w http.ResponseWriter, r *http.Request) {
	access, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok || principal.ExternalUserID != access.ExternalUserID {
		dto.WriteError(w, 401, "UNAUTHORIZED", "verified personal manager session required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil || len(r.URL.Query()) != 0 {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "exact operation ID required")
		return
	}
	if h == nil || h.executor == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "manager price executor unavailable")
		return
	}
	receipt, err := h.executor.Receipt(r.Context(), access, workspace, principal.UserID, id)
	if err != nil {
		dto.WriteError(w, 404, "NOT_FOUND", "manager price receipt not found or unavailable")
		return
	}
	dto.WriteJSON(w, 200, receipt)
}
