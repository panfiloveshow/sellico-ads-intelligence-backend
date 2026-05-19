package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type campaignActionServicer interface {
	StartCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	PauseCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	StopCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	SetBid(ctx context.Context, workspaceID, campaignID uuid.UUID, actorID uuid.UUID, placement string, newBid int) (*domain.BidChange, error)
	SetClusterBid(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, nmID int64, normQuery string, newBid int) error
	SetClusterMinus(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, nmID int64, normQuery string) error
	DepositBudget(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, amount int64) error
	GetMinimumBids(ctx context.Context, workspaceID, campaignID uuid.UUID, nmIDs []int) ([]wb.WBMinimumBidDTO, error)
	ListBidHistory(ctx context.Context, workspaceID, campaignID uuid.UUID, limit, offset int32) ([]domain.BidChange, error)
	ApplyRecommendation(ctx context.Context, workspaceID, recommendationID, actorID uuid.UUID) (*domain.BidChange, error)
}

type campaignPhraseServicer interface {
	ListMinusPhrases(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]domain.CampaignPhrase, error)
	AddMinusPhrase(ctx context.Context, workspaceID, campaignID uuid.UUID, phrase string) (*domain.CampaignPhrase, error)
	DeleteMinusPhrase(ctx context.Context, workspaceID, phraseID uuid.UUID) error
	ListPlusPhrases(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]domain.CampaignPhrase, error)
	AddPlusPhrase(ctx context.Context, workspaceID, campaignID uuid.UUID, phrase string) (*domain.CampaignPhrase, error)
	DeletePlusPhrase(ctx context.Context, workspaceID, phraseID uuid.UUID) error
}

// CampaignActionHandler handles campaign control and bid management.
type CampaignActionHandler struct {
	actions campaignActionServicer
	phrases campaignPhraseServicer
}

func NewCampaignActionHandler(actions campaignActionServicer, phrases campaignPhraseServicer) *CampaignActionHandler {
	return &CampaignActionHandler{actions: actions, phrases: phrases}
}

func (h *CampaignActionHandler) Start(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if err := h.actions.StartCampaign(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (h *CampaignActionHandler) Pause(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if err := h.actions.PauseCampaign(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (h *CampaignActionHandler) Stop(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if err := h.actions.StopCampaign(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

type setBidRequest struct {
	Placement string `json:"placement"`
	NewBid    int    `json:"new_bid"`
}

func (r setBidRequest) validate() map[string]string {
	errors := map[string]string{}
	switch strings.TrimSpace(r.Placement) {
	case "search", "recommendations", "combined":
	default:
		errors["placement"] = "must be one of: search, recommendations, combined"
	}
	if r.NewBid <= 0 {
		errors["new_bid"] = "must be positive"
	}
	return errors
}

func (h *CampaignActionHandler) SetBid(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}

	var req setBidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid body")
		return
	}
	if validationErrors := req.validate(); len(validationErrors) > 0 {
		dto.WriteValidationError(w, validationErrors)
		return
	}

	change, err := h.actions.SetBid(r.Context(), workspaceID, campaignID, actorID, req.Placement, req.NewBid)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, change)
}

type setClusterBidRequest struct {
	NMID      int64  `json:"nm_id"`
	NormQuery string `json:"norm_query"`
	NewBid    int    `json:"new_bid"`
}

func (h *CampaignActionHandler) SetClusterBid(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}
	var req setClusterBidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid body")
		return
	}
	if err := h.actions.SetClusterBid(r.Context(), workspaceID, campaignID, actorID, req.NMID, req.NormQuery, req.NewBid); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

type setClusterMinusRequest struct {
	NMID      int64  `json:"nm_id"`
	NormQuery string `json:"norm_query"`
}

func (h *CampaignActionHandler) SetClusterMinus(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}
	var req setClusterMinusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid body")
		return
	}
	if err := h.actions.SetClusterMinus(r.Context(), workspaceID, campaignID, actorID, req.NMID, req.NormQuery); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

type depositBudgetRequest struct {
	Amount int64 `json:"amount"`
}

func (h *CampaignActionHandler) DepositBudget(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}
	var req depositBudgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid body")
		return
	}
	if err := h.actions.DepositBudget(r.Context(), workspaceID, campaignID, actorID, req.Amount); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "deposited"})
}

func (h *CampaignActionHandler) MinimumBids(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	values := r.URL.Query()["nm_id"]
	nmIDs := make([]int, 0, len(values))
	for _, value := range values {
		var parsed int
		if _, scanErr := fmt.Sscanf(value, "%d", &parsed); scanErr == nil && parsed > 0 {
			nmIDs = append(nmIDs, parsed)
		}
	}
	bids, err := h.actions.GetMinimumBids(r.Context(), workspaceID, campaignID, nmIDs)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, bids)
}

func (h *CampaignActionHandler) BidHistory(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	pg := pagination.Parse(r)
	changes, err := h.actions.ListBidHistory(r.Context(), workspaceID, campaignID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, changes, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(changes)),
	})
}

func (h *CampaignActionHandler) ApplyRecommendation(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	recID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid recommendation id")
		return
	}
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "missing user"))
		return
	}

	change, err := h.actions.ApplyRecommendation(r.Context(), workspaceID, recID, actorID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, change)
}

// --- Phrase endpoints ---

func (h *CampaignActionHandler) ListMinusPhrases(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	phrases, err := h.phrases.ListMinusPhrases(r.Context(), workspaceID, campaignID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, phrases)
}

type addPhraseRequest struct {
	Phrase string `json:"phrase"`
}

func (h *CampaignActionHandler) AddMinusPhrase(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var req addPhraseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phrase == "" {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "phrase is required")
		return
	}
	phrase, err := h.phrases.AddMinusPhrase(r.Context(), workspaceID, campaignID, req.Phrase)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, phrase)
}

func (h *CampaignActionHandler) DeleteMinusPhrase(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	phraseID, err := uuid.Parse(chi.URLParam(r, "phraseId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid phrase id")
		return
	}
	if err := h.phrases.DeleteMinusPhrase(r.Context(), workspaceID, phraseID); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *CampaignActionHandler) ListPlusPhrases(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	phrases, err := h.phrases.ListPlusPhrases(r.Context(), workspaceID, campaignID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, phrases)
}

func (h *CampaignActionHandler) AddPlusPhrase(w http.ResponseWriter, r *http.Request) {
	workspaceID, campaignID, err := h.extractIDs(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var req addPhraseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Phrase == "" {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "phrase is required")
		return
	}
	phrase, err := h.phrases.AddPlusPhrase(r.Context(), workspaceID, campaignID, req.Phrase)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, phrase)
}

func (h *CampaignActionHandler) DeletePlusPhrase(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	phraseID, err := uuid.Parse(chi.URLParam(r, "phraseId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid phrase id")
		return
	}
	if err := h.phrases.DeletePlusPhrase(r.Context(), workspaceID, phraseID); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *CampaignActionHandler) extractIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, apperror.New(apperror.ErrValidation, "missing workspace id")
	}
	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, apperror.New(apperror.ErrValidation, "invalid campaign id")
	}
	return workspaceID, campaignID, nil
}
