package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

var managerAdResolutionFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

type managerAdAdvancedServicer interface {
	Attribution(context.Context, domain.ManagerAccess, uuid.UUID, string, string, time.Time, time.Time, *uuid.UUID) (*domain.ManagerAdAttributionReport, error)
	PrepareResolution(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, string, uuid.UUID, string, string, uuid.UUID, string, string) (*domain.ManagerAdManualResolution, error)
	ResolutionReceipt(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.ManagerAdManualResolution, error)
	ApproveResolution(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, string, map[string]bool) (*domain.ManagerAdManualResolution, error)
	CancelResolution(context.Context, domain.ManagerAccess, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, string) (*domain.ManagerAdManualResolution, error)
}

type ManagerAdAdvancedHandler struct {
	service managerAdAdvancedServicer
}

func NewManagerAdAdvancedHandler(s managerAdAdvancedServicer) *ManagerAdAdvancedHandler {
	return &ManagerAdAdvancedHandler{service: s}
}

func (h *ManagerAdAdvancedHandler) Attribution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	identity, ok := middleware.ManagerAccessFromContext(r.Context())
	workspaceID, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok || principal.ExternalUserID != identity.ExternalUserID {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "verified manager access is required")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "manager ad attribution is not configured")
		return
	}
	for key, values := range r.URL.Query() {
		switch key {
		case "integration_id", "date_from", "date_to", "campaign_id":
		default:
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "unknown attribution query parameter")
			return
		}
		if len(values) != 1 {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "duplicate attribution query parameter")
			return
		}
	}
	q := r.URL.Query()
	integrationID := q.Get("integration_id")
	from, fromErr := time.Parse(time.DateOnly, q.Get("date_from"))
	to, toErr := time.Parse(time.DateOnly, q.Get("date_to"))
	if !managerIntegrationID.MatchString(integrationID) || fromErr != nil || toErr != nil || from.After(to) || to.Sub(from) > 89*24*time.Hour {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "provide a valid store and a period of 1 to 90 days")
		return
	}
	var campaignID *uuid.UUID
	if q.Has("campaign_id") {
		parsed, err := uuid.Parse(q.Get("campaign_id"))
		if err != nil || parsed == uuid.Nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "campaign_id must be a valid UUID")
			return
		}
		campaignID = &parsed
	}
	report, err := h.service.Attribution(r.Context(), identity, workspaceID, principal.Token, integrationID, from, to, campaignID)
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sellico session has expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, http.StatusForbidden, "FORBIDDEN", "store access denied")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, report)
}

func (h *ManagerAdAdvancedHandler) PrepareResolution(w http.ResponseWriter, r *http.Request) {
	h.handleResolution(w, r, "prepare")
}

func (h *ManagerAdAdvancedHandler) ResolutionReceipt(w http.ResponseWriter, r *http.Request) {
	h.handleResolution(w, r, "receipt")
}

func (h *ManagerAdAdvancedHandler) ApproveResolution(w http.ResponseWriter, r *http.Request) {
	h.handleResolution(w, r, "approve")
}

func (h *ManagerAdAdvancedHandler) CancelResolution(w http.ResponseWriter, r *http.Request) {
	h.handleResolution(w, r, "cancel")
}

func (h *ManagerAdAdvancedHandler) handleResolution(w http.ResponseWriter, r *http.Request, mode string) {
	w.Header().Set("Cache-Control", "private, no-store")
	access, ok := middleware.ManagerAccessFromContext(r.Context())
	workspace, wok := middleware.WorkspaceIDFromContext(r.Context())
	principal, pok := middleware.PrincipalFromContext(r.Context())
	if !ok || !wok || !pok || principal.UserID == uuid.Nil || principal.ExternalUserID != access.ExternalUserID {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "verified personal manager session required")
		return
	}
	if len(r.URL.Query()) != 0 {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "query parameters are not supported")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "advertising manual resolution unavailable")
		return
	}
	opID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || opID == uuid.Nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "specific advertising operation ID required")
		return
	}
	var resID uuid.UUID
	if mode != "prepare" {
		resID, err = uuid.Parse(chi.URLParam(r, "resolutionId"))
		if err != nil || resID == uuid.Nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "specific resolution ID required")
			return
		}
	}

	var response *domain.ManagerAdManualResolution
	if mode == "receipt" {
		response, err = h.service.ResolutionReceipt(r.Context(), access, workspace, principal.UserID, opID, resID, principal.Token)
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		switch mode {
		case "prepare":
			var body struct {
				IntegrationID    string `json:"integration_id"`
				Marketplace      string `json:"marketplace"`
				CampaignID       string `json:"campaign_id"`
				Reason           string `json:"reason"`
				SupportReference string `json:"support_reference"`
			}
			if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || !managerIntegrationID.MatchString(body.IntegrationID) {
				dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "exact store, campaign, reason and support reference required")
				return
			}
			campID, parseErr := uuid.Parse(body.CampaignID)
			if parseErr != nil || campID == uuid.Nil {
				dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "campaign_id must be a valid UUID")
				return
			}
			response, err = h.service.PrepareResolution(r.Context(), access, workspace, principal.UserID, principal.Token, opID, body.IntegrationID, body.Marketplace, campID, body.Reason, body.SupportReference)
		case "approve":
			var body struct {
				Fingerprint   string          `json:"fingerprint"`
				Confirmations map[string]bool `json:"confirmations"`
			}
			if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || !managerAdResolutionFingerprint.MatchString(body.Fingerprint) {
				dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "exact resolution fingerprint and personal confirmations required")
				return
			}
			response, err = h.service.ApproveResolution(r.Context(), access, workspace, principal.UserID, opID, resID, principal.Token, body.Fingerprint, body.Confirmations)
		case "cancel":
			var body struct {
				Fingerprint string `json:"fingerprint"`
			}
			if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || !managerAdResolutionFingerprint.MatchString(body.Fingerprint) {
				dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "exact resolution fingerprint required")
				return
			}
			response, err = h.service.CancelResolution(r.Context(), access, workspace, principal.UserID, opID, resID, principal.Token, body.Fingerprint)
		}
	}

	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "personal session expired")
		return
	}
	if errors.Is(err, sellico.ErrManagerScopeDenied) {
		dto.WriteError(w, http.StatusForbidden, "FORBIDDEN", "advertising access denied")
		return
	}
	if errors.Is(err, sqlcgen.ErrManagerAdResolutionChanged) || errors.Is(err, sqlcgen.ErrAdResourceHeld) {
		dto.WriteError(w, http.StatusConflict, "RESOLUTION_CHANGED", "resolution or operation state changed")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, response)
}
