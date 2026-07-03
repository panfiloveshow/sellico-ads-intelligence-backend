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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type adsReadServicer interface {
	Overview(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error)
	DataHealth(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter service.OverviewFilter) (domain.AdsDataStatus, error)
	ListProductSummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter service.ProductSummaryFilter) ([]domain.ProductAdsSummary, error)
	GetProductSummary(ctx context.Context, workspaceID, productID uuid.UUID, dateFrom, dateTo time.Time) (*domain.ProductAdsSummary, error)
	ListCampaignSummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter service.CampaignSummaryFilter) ([]domain.CampaignPerformanceSummary, error)
	GetCampaignSummary(ctx context.Context, workspaceID, campaignID uuid.UUID, dateFrom, dateTo time.Time) (*domain.CampaignPerformanceSummary, error)
	ListQuerySummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter service.QuerySummaryFilter) ([]domain.QueryPerformanceSummary, error)
	GetQuerySummary(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time) (*domain.QueryPerformanceSummary, error)
	DebugNormQuery(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter service.QuerySummaryFilter) (*service.NormQueryDebugReport, error)
}

type adsRecommendationLister interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

type adsClientReportBuilder interface {
	BuildAgencyClientReport(reportDate, dateFrom, dateTo time.Time, overview domain.AdsOverview, recommendations []domain.Recommendation) string
}

func (h *AdsReadHandler) DataHealth(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}
	dateFrom, dateTo := parseDateRange(r)
	status, err := h.svc.DataHealth(r.Context(), workspaceID, dateFrom, dateTo, service.OverviewFilter{SellerCabinetID: sellerCabinetID})
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.AdsDataStatusFromDomain(status))
}

type AdsReadHandler struct {
	svc                 adsReadServicer
	recommendations     adsRecommendationLister
	clientReportBuilder adsClientReportBuilder
}

func NewAdsReadHandler(svc adsReadServicer) *AdsReadHandler {
	return &AdsReadHandler{svc: svc}
}

func (h *AdsReadHandler) WithClientReports(builder adsClientReportBuilder, recommendations adsRecommendationLister) *AdsReadHandler {
	h.clientReportBuilder = builder
	h.recommendations = recommendations
	return h
}

func (h *AdsReadHandler) Overview(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	dateFrom, dateTo := parseDateRange(r)

	var overviewFilter service.OverviewFilter
	cabinetID, _ := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if cabinetID != nil {
		overviewFilter.SellerCabinetID = cabinetID
	}

	overview, err := h.svc.Overview(r.Context(), workspaceID, dateFrom, dateTo, overviewFilter)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.AdsOverviewFromDomain(*overview))
}

func (h *AdsReadHandler) ClientAuditReport(w http.ResponseWriter, r *http.Request) {
	if h.clientReportBuilder == nil || h.recommendations == nil {
		dto.WriteError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "client audit report is not configured")
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	overview, err := h.svc.Overview(r.Context(), workspaceID, dateFrom, dateTo, service.OverviewFilter{
		SellerCabinetID: sellerCabinetID,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	recommendations, err := h.recommendations.List(r.Context(), workspaceID, service.RecommendationListFilter{
		Status: domain.RecommendationStatusActive,
	}, 1000, 0)
	if err != nil {
		writeAppError(w, err)
		return
	}
	recommendations = filterRecommendationsBySellerCabinet(recommendations, sellerCabinetID)

	generatedAt := time.Now().UTC()
	report := h.clientReportBuilder.BuildAgencyClientReport(generatedAt, dateFrom, dateTo, *overview, recommendations)
	dto.WriteJSON(w, http.StatusOK, dto.AdsClientAuditReportResponse{
		ReportType:      "client_wb_ads_audit",
		GeneratedAt:     generatedAt,
		DateFrom:        formatDateForResponse(dateFrom),
		DateTo:          formatDateForResponse(dateTo),
		Report:          report,
		DataStatus:      dto.AdsDataStatusFromDomain(overview.DataStatus),
		Recommendations: len(recommendations),
		AttentionItems:  len(overview.Attention),
		ActiveCampaigns: overview.Totals.ActiveCampaigns,
		Campaigns:       overview.Totals.Campaigns,
		Products:        overview.Totals.Products,
		Queries:         overview.Totals.Queries,
	})
}

func (h *AdsReadHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	rows, err := h.svc.ListProductSummaries(r.Context(), workspaceID, dateFrom, dateTo, service.ProductSummaryFilter{
		SellerCabinetID: sellerCabinetID,
		Title:           r.URL.Query().Get("title"),
		View:            r.URL.Query().Get("view"),
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	pg := pagination.Parse(r)
	paged := paginateSlice(rows, pg.PerPage, pg.Offset())
	items := make([]dto.ProductAdsSummaryResponse, len(paged))
	for i, row := range paged {
		items[i] = dto.ProductAdsSummaryFromDomain(row)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(rows)),
	})
}

func (h *AdsReadHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	product, err := h.svc.GetProductSummary(r.Context(), workspaceID, productID, dateFrom, dateTo)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.ProductAdsSummaryFromDomain(*product))
}

func (h *AdsReadHandler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}
	productID, err := parseOptionalUUIDQuery(r, "product_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	rows, err := h.svc.ListCampaignSummaries(r.Context(), workspaceID, dateFrom, dateTo, service.CampaignSummaryFilter{
		SellerCabinetID: sellerCabinetID,
		Status:          r.URL.Query().Get("status"),
		Name:            r.URL.Query().Get("name"),
		ProductID:       productID,
		View:            r.URL.Query().Get("view"),
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	pg := pagination.Parse(r)
	paged := paginateSlice(rows, pg.PerPage, pg.Offset())
	items := make([]dto.CampaignPerformanceSummaryResponse, len(paged))
	for i, row := range paged {
		items[i] = dto.CampaignPerformanceSummaryFromDomain(row)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(rows)),
	})
}

func (h *AdsReadHandler) GetCampaign(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	campaignID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	campaign, err := h.svc.GetCampaignSummary(r.Context(), workspaceID, campaignID, dateFrom, dateTo)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.CampaignPerformanceSummaryFromDomain(*campaign))
}

func (h *AdsReadHandler) ListQueries(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}
	campaignID, err := parseOptionalUUIDQuery(r, "campaign_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign_id")
		return
	}
	productID, err := parseOptionalUUIDQuery(r, "product_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	rows, err := h.svc.ListQuerySummaries(r.Context(), workspaceID, dateFrom, dateTo, service.QuerySummaryFilter{
		SellerCabinetID: sellerCabinetID,
		CampaignID:      campaignID,
		ProductID:       productID,
		Search:          r.URL.Query().Get("search"),
		View:            r.URL.Query().Get("view"),
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	pg := pagination.Parse(r)
	paged := paginateSlice(rows, pg.PerPage, pg.Offset())
	items := make([]dto.QueryPerformanceSummaryResponse, len(paged))
	for i, row := range paged {
		items[i] = dto.QueryPerformanceSummaryFromDomain(row)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(rows)),
	})
}

func (h *AdsReadHandler) GetQuery(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	phraseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid query id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	query, err := h.svc.GetQuerySummary(r.Context(), workspaceID, phraseID, dateFrom, dateTo)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.QueryPerformanceSummaryFromDomain(*query))
}

func (h *AdsReadHandler) DebugNormQuery(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}

	dateFrom, dateTo := parseDateRange(r)
	report, err := h.svc.DebugNormQuery(r.Context(), workspaceID, dateFrom, dateTo, service.QuerySummaryFilter{
		SellerCabinetID: sellerCabinetID,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, report)
}

func paginateSlice[T any](items []T, perPage, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := offset + perPage
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

func formatDateForResponse(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02")
}

func filterRecommendationsBySellerCabinet(items []domain.Recommendation, sellerCabinetID *uuid.UUID) []domain.Recommendation {
	if sellerCabinetID == nil {
		return items
	}
	filtered := make([]domain.Recommendation, 0, len(items))
	for _, item := range items {
		if item.SellerCabinetID == nil || *item.SellerCabinetID != *sellerCabinetID {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}
