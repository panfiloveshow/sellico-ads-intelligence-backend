package handler

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type semanticsServicer interface {
	ListKeywords(ctx context.Context, workspaceID uuid.UUID, search string, limit, offset int32) ([]domain.Keyword, error)
	CollectFromPhrases(ctx context.Context, workspaceID uuid.UUID) (int, error)
	AutoCluster(ctx context.Context, workspaceID uuid.UUID) (int, error)
	ListClusters(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.KeywordCluster, error)
}

type SemanticsHandler struct {
	svc semanticsServicer
}

func NewSemanticsHandler(svc semanticsServicer) *SemanticsHandler {
	return &SemanticsHandler{svc: svc}
}

func (h *SemanticsHandler) ListKeywords(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	pg := pagination.Parse(r)
	search := r.URL.Query().Get("search")
	keywords, err := h.svc.ListKeywords(r.Context(), workspaceID, search, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, keywords, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(keywords))})
}

func (h *SemanticsHandler) CollectKeywords(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	count, err := h.svc.CollectFromPhrases(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]int{"imported": count})
}

func (h *SemanticsHandler) AutoCluster(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	count, err := h.svc.AutoCluster(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]int{"clusters_created": count})
}

func (h *SemanticsHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	pg := pagination.Parse(r)
	clusters, err := h.svc.ListClusters(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, clusters, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(clusters))})
}
