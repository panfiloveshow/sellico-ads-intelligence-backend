package handler

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type auditLogServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.AuditLogListFilter, limit, offset int32) ([]domain.AuditLog, error)
}

// AuditLogHandler handles audit log endpoints.
type AuditLogHandler struct {
	svc auditLogServicer
}

func NewAuditLogHandler(svc auditLogServicer) *AuditLogHandler {
	return &AuditLogHandler{svc: svc}
}

func (h *AuditLogHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	userID, err := parseOptionalUUIDQuery(r, "user_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid user_id")
		return
	}
	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	pg := pagination.Parse(r)
	logs, err := h.svc.List(r.Context(), workspaceID, service.AuditLogListFilter{
		Action:     r.URL.Query().Get("action"),
		EntityType: r.URL.Query().Get("entity_type"),
		UserID:     userID,
		DateFrom:   &dateFrom,
		DateTo:     &dateTo,
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.AuditLogResponse, len(logs))
	for i, log := range logs {
		items[i] = dto.AuditLogFromDomain(log)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}
