package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type phraseServicer interface {
	Get(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.Phrase, error)
	GetStats(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.PhraseStat, error)
	ListBids(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.BidSnapshot, error)
}

// PhraseHandler handles phrase endpoints.
type PhraseHandler struct {
	svc phraseServicer
}

func NewPhraseHandler(svc phraseServicer) *PhraseHandler {
	return &PhraseHandler{svc: svc}
}

func (h *PhraseHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	phraseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase id")
		return
	}

	phrase, err := h.svc.Get(r.Context(), workspaceID, phraseID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.PhraseFromDomain(*phrase))
}

func (h *PhraseHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	phraseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase id")
		return
	}

	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	pg := pagination.Parse(r)
	stats, err := h.svc.GetStats(r.Context(), workspaceID, phraseID, dateFrom, dateTo, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.PhraseStatResponse, len(stats))
	for i, stat := range stats {
		items[i] = dto.PhraseStatFromDomain(stat)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

func (h *PhraseHandler) ListBids(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	phraseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase id")
		return
	}

	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	pg := pagination.Parse(r)
	bids, err := h.svc.ListBids(r.Context(), workspaceID, phraseID, dateFrom, dateTo, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.BidSnapshotResponse, len(bids))
	for i, bid := range bids {
		items[i] = dto.BidSnapshotFromDomain(bid)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}
