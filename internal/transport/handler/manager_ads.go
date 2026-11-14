package handler

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

type managerAdsServicer interface {
	Campaigns(context.Context, domain.ManagerAccess, uuid.UUID, string, string, time.Time, time.Time, int, int, bool) (*domain.ManagerAdsReport, error)
}
type ManagerAdsHandler struct{ service managerAdsServicer }

func NewManagerAdsHandler(service managerAdsServicer) *ManagerAdsHandler {
	return &ManagerAdsHandler{service: service}
}

var managerIntegrationID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func (h *ManagerAdsHandler) Campaigns(w http.ResponseWriter, r *http.Request) {
	identity, ok := middleware.ManagerAccessFromContext(r.Context())
	workspaceID, workspaceOK := middleware.WorkspaceIDFromContext(r.Context())
	principal, principalOK := middleware.PrincipalFromContext(r.Context())
	if !ok || !workspaceOK || !principalOK {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "verified manager access is required")
		return
	}
	for key, values := range r.URL.Query() {
		switch key {
		case "integration_id", "date_from", "date_to", "page", "limit", "compare":
		default:
			dto.WriteError(w, 400, "VALIDATION_ERROR", "unknown manager report parameter")
			return
		}
		if len(values) != 1 {
			dto.WriteError(w, 400, "VALIDATION_ERROR", "duplicate manager report parameter")
			return
		}
	}
	query := r.URL.Query()
	id := query.Get("integration_id")
	from, fromErr := time.Parse(time.DateOnly, query.Get("date_from"))
	to, toErr := time.Parse(time.DateOnly, query.Get("date_to"))
	page, limit := 1, 25
	var err error
	if query.Has("page") {
		page, err = strconv.Atoi(query.Get("page"))
		if err != nil {
			page = 0
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil {
			limit = 0
		}
	}
	compare := false
	if query.Has("compare") {
		if query.Get("compare") != "true" && query.Get("compare") != "false" {
			dto.WriteError(w, 400, "VALIDATION_ERROR", "compare must be true or false")
			return
		}
		compare = query.Get("compare") == "true"
	}
	if !managerIntegrationID.MatchString(id) || fromErr != nil || toErr != nil || from.After(to) || to.Sub(from) > 89*24*time.Hour || page < 1 || page > 10000 || limit < 1 || limit > 100 {
		dto.WriteError(w, 400, "VALIDATION_ERROR", "provide a store, a period of 1 to 90 days, and a valid page")
		return
	}
	if h == nil || h.service == nil {
		dto.WriteError(w, 503, "SERVICE_UNAVAILABLE", "manager ads service is not configured")
		return
	}
	report, err := h.service.Campaigns(r.Context(), identity, workspaceID, principal.Token, id, from, to, page, limit, compare)
	if errors.Is(err, sellico.ErrUnauthorized) {
		dto.WriteError(w, 401, "UNAUTHORIZED", "Sellico session has expired")
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
	dto.WriteJSON(w, http.StatusOK, report)
}
