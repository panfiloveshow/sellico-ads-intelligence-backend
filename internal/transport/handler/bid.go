package handler

import (
	"context"
	"encoding/json"
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

type bidServicer interface {
	Create(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreateBidSnapshotInput) (*domain.BidSnapshot, error)
	ListHistory(ctx context.Context, workspaceID uuid.UUID, filter service.BidListFilter, limit, offset int32) ([]domain.BidSnapshot, error)
	GetEstimate(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.BidSnapshot, error)
}

// BidHandler handles bid endpoints.
type BidHandler struct {
	svc bidServicer
}

func NewBidHandler(svc bidServicer) *BidHandler {
	return &BidHandler{svc: svc}
}

func (h *BidHandler) Create(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreateBidSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	item, err := h.svc.Create(r.Context(), actorID, workspaceID, service.CreateBidSnapshotInput{
		PhraseID:       req.PhraseID,
		CompetitiveBid: req.CompetitiveBid,
		LeadershipBid:  req.LeadershipBid,
		CPMMin:         req.CPMMin,
		CapturedAt:     req.CapturedAt,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.BidSnapshotFromDomain(*item))
}

func (h *BidHandler) ListHistory(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	phraseID, err := parseOptionalUUIDQuery(r, "phrase_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase_id")
		return
	}
	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	pg := pagination.Parse(r)

	items, err := h.svc.ListHistory(r.Context(), workspaceID, service.BidListFilter{
		PhraseID: phraseID,
		DateFrom: &dateFrom,
		DateTo:   &dateTo,
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	respItems := make([]dto.BidSnapshotResponse, len(items))
	for i, item := range items {
		respItems[i] = dto.BidSnapshotFromDomain(item)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, respItems, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(respItems)),
	})
}

func (h *BidHandler) GetEstimate(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	phraseIDRaw := r.URL.Query().Get("phrase_id")
	if phraseIDRaw == "" {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "phrase_id is required")
		return
	}
	phraseID, err := uuid.Parse(phraseIDRaw)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase_id")
		return
	}

	estimate, err := h.svc.GetEstimate(r.Context(), workspaceID, phraseID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.BidEstimateFromDomain(*estimate))
}
