package handler

import (
	"context"
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
)

type readinessChecker func(context.Context) error

// HealthHandler serves live and readiness probes.
type HealthHandler struct {
	ready readinessChecker
}

func NewHealthHandler(ready readinessChecker) *HealthHandler {
	return &HealthHandler{ready: ready}
}

func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	if h.ready != nil {
		if err := h.ready(r.Context()); err != nil {
			dto.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", err.Error())
			return
		}
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
