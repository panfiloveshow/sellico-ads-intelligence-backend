package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type extensionServicer interface {
	UpsertSession(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error)
	CreateContextEvent(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error)
	CreatePageContext(ctx context.Context, userID, workspaceID uuid.UUID, input service.CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error)
	CreateBidSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionBidSnapshotInput) (int, error)
	CreatePositionSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionPositionSnapshotInput) (int, error)
	CreateUISignals(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionUISignalInput) (int, error)
	CreateNetworkCaptures(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionNetworkCaptureInput) (int, error)
	CreateDOMRowSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []service.CreateExtensionDOMRowSnapshotInput) (int, error)
	GetSearchWidget(ctx context.Context, workspaceID uuid.UUID, query string) (*service.ExtensionSearchWidget, error)
	GetProductWidget(ctx context.Context, workspaceID, productID uuid.UUID) (*service.ExtensionProductWidget, error)
	GetProductWidgetByWBProductID(ctx context.Context, workspaceID uuid.UUID, wbProductID int64) (*service.ExtensionProductWidget, error)
	GetCampaignWidget(ctx context.Context, workspaceID, campaignID uuid.UUID) (*service.ExtensionCampaignWidget, error)
	GetCampaignWidgetByWBCampaignID(ctx context.Context, workspaceID uuid.UUID, wbCampaignID int64) (*service.ExtensionCampaignWidget, error)
	GetEvidenceSummary(ctx context.Context, workspaceID uuid.UUID) (*service.ExtensionEvidenceSummary, error)
	GetEvidenceDebug(ctx context.Context, workspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceDebug, error)
	GetEvidenceSupportReport(ctx context.Context, workspaceID uuid.UUID, input service.ExtensionEvidenceDebugInput) (*service.ExtensionEvidenceSupportReport, error)
	Version() string
}

// ExtensionHandler handles browser extension endpoints.
type ExtensionHandler struct {
	svc       extensionServicer
	jwtSecret string
}

func NewExtensionHandler(svc extensionServicer, jwtSecret ...string) *ExtensionHandler {
	secret := ""
	if len(jwtSecret) > 0 {
		secret = jwtSecret[0]
	}
	return &ExtensionHandler{svc: svc, jwtSecret: secret}
}

func (h *ExtensionHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreateExtensionSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	session, err := h.svc.UpsertSession(r.Context(), userID, workspaceID, req.ExtensionVersion)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionSessionFromDomain(*session))
}

func (h *ExtensionHandler) CreateContext(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.ExtensionContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	if _, err := h.svc.CreateContextEvent(r.Context(), userID, workspaceID, req.URL, req.PageType); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, nil)
}

func (h *ExtensionHandler) Version(w http.ResponseWriter, _ *http.Request) {
	dto.WriteJSON(w, http.StatusOK, dto.ExtensionVersionResponse{Version: h.svc.Version()})
}

func (h *ExtensionHandler) ExchangeToken(w http.ResponseWriter, r *http.Request) {
	if h.jwtSecret == "" {
		writeAppError(w, apperror.New(apperror.ErrInternal, "extension token exchange is not configured"))
		return
	}
	if claims, ok := middleware.TokenClaimsFromContext(r.Context()); ok && claims.TokenType == "extension" {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "extension token cannot be exchanged"))
		return
	}
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	ttl := 12 * time.Hour
	token, err := jwt.GenerateExtensionToken(userID, workspaceID, domain.RoleViewer, h.jwtSecret, ttl)
	if err != nil {
		writeAppError(w, apperror.New(apperror.ErrInternal, "failed to issue extension token"))
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.ExtensionTokenResponse{
		AccessToken:      token,
		TokenType:        "Bearer",
		ExpiresInSeconds: int64(ttl.Seconds()),
		WorkspaceID:      workspaceID.String(),
		Role:             domain.RoleViewer,
	})
}

func (h *ExtensionHandler) CreatePageContext(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := extensionContextIDs(w, r)
	if !ok {
		return
	}

	var req dto.CreateExtensionPageContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	result, err := h.svc.CreatePageContext(r.Context(), userID, workspaceID, service.CreateExtensionPageContextInput{
		URL:             req.URL,
		PageType:        req.PageType,
		SellerCabinetID: req.SellerCabinetID,
		CampaignID:      req.CampaignID,
		PhraseID:        req.PhraseID,
		ProductID:       req.ProductID,
		Query:           req.Query,
		Region:          req.Region,
		ActiveFilters:   req.ActiveFilters,
		Metadata:        req.Metadata,
		CapturedAt:      req.CapturedAt,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionPageContextFromDomain(*result))
}

func (h *ExtensionHandler) CreateBidSnapshots(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := extensionContextIDs(w, r)
	if !ok {
		return
	}

	var req dto.CreateExtensionBidSnapshotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	inputs := make([]service.CreateExtensionBidSnapshotInput, len(req.Items))
	for i, item := range req.Items {
		confidence := 1.0
		if item.Confidence != nil {
			confidence = *item.Confidence
		}
		inputs[i] = service.CreateExtensionBidSnapshotInput{
			SellerCabinetID: item.SellerCabinetID,
			CampaignID:      item.CampaignID,
			PhraseID:        item.PhraseID,
			Query:           item.Query,
			Region:          item.Region,
			VisibleBid:      item.VisibleBid,
			RecommendedBid:  item.RecommendedBid,
			CompetitiveBid:  item.CompetitiveBid,
			LeadershipBid:   item.LeadershipBid,
			CPMMin:          item.CPMMin,
			Confidence:      confidence,
			Metadata:        item.Metadata,
			CapturedAt:      item.CapturedAt,
		}
	}

	accepted, err := h.svc.CreateBidSnapshots(r.Context(), userID, workspaceID, inputs)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionIngestAcceptedResponse{Accepted: accepted})
}

func (h *ExtensionHandler) CreatePositionSnapshots(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := extensionContextIDs(w, r)
	if !ok {
		return
	}

	var req dto.CreateExtensionPositionSnapshotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	inputs := make([]service.CreateExtensionPositionSnapshotInput, len(req.Items))
	for i, item := range req.Items {
		confidence := 1.0
		if item.Confidence != nil {
			confidence = *item.Confidence
		}
		inputs[i] = service.CreateExtensionPositionSnapshotInput{
			SellerCabinetID: item.SellerCabinetID,
			CampaignID:      item.CampaignID,
			PhraseID:        item.PhraseID,
			ProductID:       item.ProductID,
			Query:           item.Query,
			Region:          item.Region,
			VisiblePosition: item.VisiblePosition,
			VisiblePage:     item.VisiblePage,
			PageSubtype:     item.PageSubtype,
			Confidence:      confidence,
			Metadata:        item.Metadata,
			CapturedAt:      item.CapturedAt,
		}
	}

	accepted, err := h.svc.CreatePositionSnapshots(r.Context(), userID, workspaceID, inputs)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionIngestAcceptedResponse{Accepted: accepted})
}

func (h *ExtensionHandler) CreateUISignals(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := extensionContextIDs(w, r)
	if !ok {
		return
	}

	var req dto.CreateExtensionUISignalsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	inputs := make([]service.CreateExtensionUISignalInput, len(req.Items))
	for i, item := range req.Items {
		confidence := 1.0
		if item.Confidence != nil {
			confidence = *item.Confidence
		}
		inputs[i] = service.CreateExtensionUISignalInput{
			SellerCabinetID: item.SellerCabinetID,
			CampaignID:      item.CampaignID,
			PhraseID:        item.PhraseID,
			ProductID:       item.ProductID,
			Query:           item.Query,
			Region:          item.Region,
			SignalType:      item.SignalType,
			Severity:        item.Severity,
			Title:           item.Title,
			Message:         item.Message,
			Confidence:      confidence,
			Metadata:        item.Metadata,
			CapturedAt:      item.CapturedAt,
		}
	}

	accepted, err := h.svc.CreateUISignals(r.Context(), userID, workspaceID, inputs)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionIngestAcceptedResponse{Accepted: accepted})
}

func (h *ExtensionHandler) CreateNetworkCaptures(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := extensionContextIDs(w, r)
	if !ok {
		return
	}

	var req dto.CreateExtensionNetworkCapturesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	inputs := make([]service.CreateExtensionNetworkCaptureInput, len(req.Items))
	for i, item := range req.Items {
		inputs[i] = service.CreateExtensionNetworkCaptureInput{
			SellerCabinetID: item.SellerCabinetID,
			CampaignID:      item.CampaignID,
			PhraseID:        item.PhraseID,
			ProductID:       item.ProductID,
			PageType:        item.PageType,
			EndpointKey:     item.EndpointKey,
			Query:           item.Query,
			Region:          item.Region,
			Payload:         item.Payload,
			CapturedAt:      item.CapturedAt,
		}
	}

	accepted, err := h.svc.CreateNetworkCaptures(r.Context(), userID, workspaceID, inputs)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionIngestAcceptedResponse{Accepted: accepted})
}

func (h *ExtensionHandler) CreateDOMRowSnapshots(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, ok := extensionContextIDs(w, r)
	if !ok {
		return
	}

	var req dto.CreateExtensionDOMRowSnapshotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	inputs := make([]service.CreateExtensionDOMRowSnapshotInput, len(req.Items))
	for i, item := range req.Items {
		confidence := 0.65
		if item.Confidence != nil {
			confidence = *item.Confidence
		}
		inputs[i] = service.CreateExtensionDOMRowSnapshotInput{
			SellerCabinetID: item.SellerCabinetID,
			CampaignID:      item.CampaignID,
			PhraseID:        item.PhraseID,
			ProductID:       item.ProductID,
			PageType:        item.PageType,
			TableRole:       item.TableRole,
			RowKey:          item.RowKey,
			Query:           item.Query,
			Region:          item.Region,
			VisibleText:     item.VisibleText,
			Cells:           item.Cells,
			Metadata:        item.Metadata,
			Confidence:      confidence,
			CapturedAt:      item.CapturedAt,
		}
	}

	accepted, err := h.svc.CreateDOMRowSnapshots(r.Context(), userID, workspaceID, inputs)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionIngestAcceptedResponse{Accepted: accepted})
}

func (h *ExtensionHandler) SearchWidget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "query is required")
		return
	}

	widget, err := h.svc.GetSearchWidget(r.Context(), workspaceID, query)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ExtensionSearchWidgetFromService(*widget))
}

func (h *ExtensionHandler) ProductWidget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var (
		widget *service.ExtensionProductWidget
		err    error
	)
	if rawWbID := r.URL.Query().Get("wb_product_id"); rawWbID != "" {
		var wbProductID int64
		if _, scanErr := fmt.Sscan(rawWbID, &wbProductID); scanErr != nil || wbProductID <= 0 {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid wb_product_id")
			return
		}
		widget, err = h.svc.GetProductWidgetByWBProductID(r.Context(), workspaceID, wbProductID)
	} else {
		productID, parseErr := uuid.Parse(r.URL.Query().Get("product_id"))
		if parseErr != nil {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
			return
		}
		widget, err = h.svc.GetProductWidget(r.Context(), workspaceID, productID)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ExtensionProductWidgetFromService(*widget))
}

func (h *ExtensionHandler) CampaignWidget(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var (
		widget *service.ExtensionCampaignWidget
		err    error
	)
	if rawWbID := r.URL.Query().Get("wb_campaign_id"); rawWbID != "" {
		var wbCampaignID int64
		if _, scanErr := fmt.Sscan(rawWbID, &wbCampaignID); scanErr != nil || wbCampaignID <= 0 {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid wb_campaign_id")
			return
		}
		widget, err = h.svc.GetCampaignWidgetByWBCampaignID(r.Context(), workspaceID, wbCampaignID)
	} else {
		campaignID, parseErr := uuid.Parse(r.URL.Query().Get("campaign_id"))
		if parseErr != nil {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign_id")
			return
		}
		widget, err = h.svc.GetCampaignWidget(r.Context(), workspaceID, campaignID)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ExtensionCampaignWidgetFromService(*widget))
}

func (h *ExtensionHandler) EvidenceSummary(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	summary, err := h.svc.GetEvidenceSummary(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ExtensionEvidenceSummaryFromService(*summary))
}

func (h *ExtensionHandler) EvidenceDebug(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	input, ok := parseExtensionEvidenceDebugInput(w, r)
	if !ok {
		return
	}
	debug, err := h.svc.GetEvidenceDebug(r.Context(), workspaceID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ExtensionEvidenceDebugFromService(*debug))
}

func (h *ExtensionHandler) EvidenceSupportReport(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	input, ok := parseExtensionEvidenceDebugInput(w, r)
	if !ok {
		return
	}
	report, err := h.svc.GetEvidenceSupportReport(r.Context(), workspaceID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ExtensionEvidenceSupportReportFromService(*report))
}

func parseExtensionEvidenceDebugInput(w http.ResponseWriter, r *http.Request) (service.ExtensionEvidenceDebugInput, bool) {
	query := r.URL.Query()
	input := service.ExtensionEvidenceDebugInput{
		Scope: query.Get("scope"),
		Query: query.Get("query"),
		Limit: 20,
	}
	if rawLimit := query.Get("limit"); rawLimit != "" {
		var limit int32
		if _, err := fmt.Sscan(rawLimit, &limit); err != nil || limit <= 0 {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid limit")
			return input, false
		}
		input.Limit = limit
	}
	if raw := query.Get("campaign_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign_id")
			return input, false
		}
		input.CampaignID = &id
	}
	if raw := query.Get("product_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
			return input, false
		}
		input.ProductID = &id
	}
	if raw := query.Get("phrase_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase_id")
			return input, false
		}
		input.PhraseID = &id
	}
	return input, true
}

func extensionContextIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return uuid.Nil, uuid.Nil, false
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return uuid.Nil, uuid.Nil, false
	}
	return userID, workspaceID, true
}
