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
	ListKeywords(ctx context.Context, sellerCabinetID uuid.UUID, search string, limit, offset int32) ([]domain.Keyword, error)
	CollectFromPhrases(ctx context.Context, workspaceID, sellerCabinetID uuid.UUID) (int, error)
	AutoCluster(ctx context.Context, workspaceID, sellerCabinetID uuid.UUID) (int, error)
	ListClusters(ctx context.Context, sellerCabinetID uuid.UUID, limit, offset int32) ([]domain.KeywordCluster, error)
}

type SemanticsHandler struct {
	svc semanticsServicer
}

func NewSemanticsHandler(svc semanticsServicer) *SemanticsHandler {
	return &SemanticsHandler{svc: svc}
}

// requireSellerCabinetID reads the mandatory seller_cabinet_id query param.
// Keywords/clusters are per-store data — a workspace with multiple cabinets
// (stores in different niches) would otherwise blend them into one pool.
func requireSellerCabinetID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil || id == nil {
		writeAppError(w, apperror.New(apperror.ErrValidation, "seller_cabinet_id is required"))
		return uuid.UUID{}, false
	}
	return *id, true
}

func (h *SemanticsHandler) ListKeywords(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.WorkspaceIDFromContext(r.Context()); !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	sellerCabinetID, ok := requireSellerCabinetID(w, r)
	if !ok {
		return
	}
	pg := pagination.Parse(r)
	search := r.URL.Query().Get("search")
	keywords, err := h.svc.ListKeywords(r.Context(), sellerCabinetID, search, int32(pg.PerPage), int32(pg.Offset()))
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
	sellerCabinetID, ok := requireSellerCabinetID(w, r)
	if !ok {
		return
	}
	count, err := h.svc.CollectFromPhrases(r.Context(), workspaceID, sellerCabinetID)
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
	sellerCabinetID, ok := requireSellerCabinetID(w, r)
	if !ok {
		return
	}
	count, err := h.svc.AutoCluster(r.Context(), workspaceID, sellerCabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]int{"clusters_created": count})
}

func (h *SemanticsHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.WorkspaceIDFromContext(r.Context()); !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	sellerCabinetID, ok := requireSellerCabinetID(w, r)
	if !ok {
		return
	}
	pg := pagination.Parse(r)
	clusters, err := h.svc.ListClusters(r.Context(), sellerCabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, clusters, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(clusters))})
}
