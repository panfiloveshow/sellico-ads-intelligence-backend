package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type productEconomicsServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.ProductEconomics, error)
	Import(ctx context.Context, actorID, workspaceID uuid.UUID, rows []domain.ProductEconomicsInput) (*domain.ProductEconomicsImportResult, error)
}

type ProductEconomicsHandler struct {
	service productEconomicsServicer
}

func NewProductEconomicsHandler(service productEconomicsServicer) *ProductEconomicsHandler {
	return &ProductEconomicsHandler{service: service}
}

func (h *ProductEconomicsHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	pg := pagination.Parse(r)
	items, err := h.service.List(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

type productEconomicsImportRequest struct {
	Rows []domain.ProductEconomicsInput `json:"rows"`
}

func (h *ProductEconomicsHandler) Import(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	actorID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user")
		return
	}
	rows, err := parseProductEconomicsImport(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	result, err := h.service.Import(r.Context(), actorID, workspaceID, rows)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, result)
}

func parseProductEconomicsImport(r *http.Request) ([]domain.ProductEconomicsInput, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/csv") || strings.Contains(contentType, "application/csv") {
		return parseProductEconomicsCSV(r.Body)
	}
	var request productEconomicsImportRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return nil, fmt.Errorf("invalid request body")
	}
	if len(request.Rows) == 0 {
		return nil, fmt.Errorf("rows are required")
	}
	return request.Rows, nil
}

func parseProductEconomicsCSV(body io.Reader) ([]domain.ProductEconomicsInput, error) {
	reader := csv.NewReader(body)
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid csv")
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("csv header and at least one row are required")
	}
	header := make(map[string]int, len(records[0]))
	for i, column := range records[0] {
		header[strings.TrimSpace(column)] = i
	}
	if _, ok := header["wb_product_id"]; !ok {
		return nil, fmt.Errorf("csv must include wb_product_id")
	}
	rows := make([]domain.ProductEconomicsInput, 0, len(records)-1)
	for _, record := range records[1:] {
		rows = append(rows, domain.ProductEconomicsInput{
			WBProductID:         csvInt64(record, header, "wb_product_id"),
			CostPrice:           csvOptionalInt64(record, header, "cost_price"),
			LogisticsCost:       csvOptionalInt64(record, header, "logistics_cost"),
			OtherCosts:          csvOptionalInt64(record, header, "other_costs"),
			TaxRatePercent:      csvOptionalFloat64(record, header, "tax_rate_percent"),
			CommissionPercent:   csvOptionalFloat64(record, header, "commission_percent"),
			TargetMarginPercent: csvOptionalFloat64(record, header, "target_margin_percent"),
			MaxAllowedDRR:       csvOptionalFloat64(record, header, "max_allowed_drr"),
			Source:              csvString(record, header, "source"),
			EffectiveAt:         csvOptionalDate(record, header, "effective_at"),
		})
	}
	return rows, nil
}

func csvString(record []string, header map[string]int, key string) string {
	index, ok := header[key]
	if !ok || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func csvInt64(record []string, header map[string]int, key string) int64 {
	value, _ := strconv.ParseInt(csvString(record, header, key), 10, 64)
	return value
}

func csvOptionalInt64(record []string, header map[string]int, key string) *int64 {
	raw := csvString(record, header, key)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

func csvOptionalFloat64(record []string, header map[string]int, key string) *float64 {
	raw := csvString(record, header, key)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &value
}

func csvOptionalDate(record []string, header map[string]int, key string) *time.Time {
	raw := csvString(record, header, key)
	if raw == "" {
		return nil
	}
	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil
	}
	return &value
}
