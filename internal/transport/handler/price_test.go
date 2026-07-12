package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type stubPriceService struct {
	listCalled       bool
	heatmapCalled    bool
	changesCalled    bool
	schedulesCalled  bool
	catalogTotal     int64
	changesTotal     int64
	uploadTasksTotal int64
	schedulesTotal   int64
}

func (s *stubPriceService) ListCatalog(context.Context, uuid.UUID, *uuid.UUID, int32, int32) ([]domain.ProductCatalogItem, error) {
	s.listCalled = true
	return nil, nil
}

func (s *stubPriceService) CountCatalog(context.Context, uuid.UUID, *uuid.UUID) (int64, error) {
	return s.catalogTotal, nil
}

func (*stubPriceService) ListCabinetsScope(context.Context, uuid.UUID) ([]domain.CabinetPricesScope, error) {
	return nil, nil
}

func (*stubPriceService) SyncPrices(context.Context, uuid.UUID) (int, error) { return 0, nil }

func (*stubPriceService) ApplyManualBulk(context.Context, uuid.UUID, uuid.UUID, domain.ManualPriceBulkRequest) (*domain.PriceBulkResult, error) {
	return &domain.PriceBulkResult{}, nil
}

func (s *stubPriceService) ListChanges(context.Context, uuid.UUID, domain.PriceChangeFilter) ([]domain.PriceChange, error) {
	s.changesCalled = true
	return nil, nil
}

func (s *stubPriceService) CountChanges(context.Context, uuid.UUID, domain.PriceChangeFilter) (int64, error) {
	return s.changesTotal, nil
}

func (*stubPriceService) Rollback(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.PriceChange, error) {
	return nil, nil
}

func (*stubPriceService) ListUploadTasks(context.Context, uuid.UUID, int32, int32) ([]domain.PriceUploadTask, error) {
	return nil, nil
}

func (s *stubPriceService) CountUploadTasks(context.Context, uuid.UUID) (int64, error) {
	return s.uploadTasksTotal, nil
}

func (*stubPriceService) CreateSchedule(context.Context, uuid.UUID, uuid.UUID, domain.PriceScheduleInput) (*domain.PriceScheduleEntry, error) {
	return nil, nil
}

func (s *stubPriceService) ListSchedules(context.Context, uuid.UUID, string, int32, int32) ([]domain.PriceScheduleEntry, error) {
	s.schedulesCalled = true
	return nil, nil
}

func (s *stubPriceService) CountSchedules(context.Context, uuid.UUID, string) (int64, error) {
	return s.schedulesTotal, nil
}

func (*stubPriceService) CancelSchedule(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (*stubPriceService) ListQuarantine(context.Context, uuid.UUID) ([]domain.PriceQuarantineGood, error) {
	return nil, nil
}

func (s *stubPriceService) OrdersHeatmap(context.Context, uuid.UUID, uuid.UUID, int64, time.Time, time.Time, string) (*domain.OrdersHeatmap, error) {
	s.heatmapCalled = true
	return &domain.OrdersHeatmap{}, nil
}

func (*stubPriceService) SetPause(context.Context, uuid.UUID, uuid.UUID, *time.Time) error {
	return nil
}

func (*stubPriceService) Health(context.Context, uuid.UUID, uuid.UUID) (*domain.RepricerHealth, error) {
	return &domain.RepricerHealth{}, nil
}

func priceRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
}

func TestPriceListRejectsInvalidSellerCabinetID(t *testing.T) {
	service := &stubPriceService{}
	h := NewPriceHandler(service, nil, nil)
	rec := httptest.NewRecorder()

	h.List(rec, priceRequest(http.MethodGet, "/prices?seller_cabinet_id=bad", ""))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, service.listCalled)
}

func TestPriceListUsesRepositoryTotalForPagination(t *testing.T) {
	service := &stubPriceService{catalogTotal: 37}
	h := NewPriceHandler(service, nil, nil)
	rec := httptest.NewRecorder()

	h.List(rec, priceRequest(http.MethodGet, "/prices?page=2&per_page=10", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"total":37`)
}

func TestPriceHeatmapRejectsInvalidQuery(t *testing.T) {
	cabinetID := uuid.New().String()
	tests := []string{
		"seller_cabinet_id=bad",
		"seller_cabinet_id=" + cabinetID + "&wb_product_id=nope",
		"seller_cabinet_id=" + cabinetID + "&wb_product_id=0",
		"seller_cabinet_id=" + cabinetID + "&metric=profit",
		"seller_cabinet_id=" + cabinetID + "&date_from=2026-13-01",
		"seller_cabinet_id=" + cabinetID + "&date_to=13.07.2026",
		"seller_cabinet_id=" + cabinetID + "&date_from=2026-07-14&date_to=2026-07-13",
	}

	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			service := &stubPriceService{}
			h := NewPriceHandler(service, nil, nil)
			rec := httptest.NewRecorder()

			h.Heatmap(rec, priceRequest(http.MethodGet, "/prices/heatmap?"+query, ""))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.False(t, service.heatmapCalled)
		})
	}
}

func TestPriceHeatmapAcceptsValidatedQuery(t *testing.T) {
	service := &stubPriceService{}
	h := NewPriceHandler(service, nil, nil)
	rec := httptest.NewRecorder()
	target := "/prices/heatmap?seller_cabinet_id=" + uuid.New().String() + "&wb_product_id=123&metric=revenue&date_from=2026-07-01&date_to=2026-07-13"

	h.Heatmap(rec, priceRequest(http.MethodGet, target, ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, service.heatmapCalled)
}

func TestPriceChangesRejectInvalidFilters(t *testing.T) {
	for _, query := range []string{
		"source=other",
		"status=unknown",
		"wb_product_id=bad",
		"wb_product_id=-1",
	} {
		t.Run(query, func(t *testing.T) {
			service := &stubPriceService{}
			h := NewPriceHandler(service, nil, nil)
			rec := httptest.NewRecorder()

			h.ListChanges(rec, priceRequest(http.MethodGet, "/price-changes?"+query, ""))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.False(t, service.changesCalled)
		})
	}
}

func TestPriceSchedulesRejectInvalidStatus(t *testing.T) {
	service := &stubPriceService{}
	h := NewPriceHandler(service, nil, nil)
	rec := httptest.NewRecorder()

	h.ListSchedules(rec, priceRequest(http.MethodGet, "/price-schedules?status=unknown", ""))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, service.schedulesCalled)
}

func TestPriceCabinetEndpointsRejectNilUUID(t *testing.T) {
	h := NewPriceHandler(&stubPriceService{}, nil, nil)

	t.Run("health", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Health(rec, priceRequest(http.MethodGet, "/prices/health?seller_cabinet_id="+uuid.Nil.String(), ""))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("pause", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Pause(rec, priceRequest(http.MethodPost, "/prices/pause", `{"seller_cabinet_id":"`+uuid.Nil.String()+`","until":null}`))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("bulk scope", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Bulk(rec, priceRequest(http.MethodPost, "/prices/bulk", `{"scope":{"all":false,"seller_cabinet_id":"`+uuid.Nil.String()+`"},"adjustment":{"type":"percent","value":5}}`))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("schedule", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.CreateSchedule(rec, priceRequest(http.MethodPost, "/price-schedules", `{"seller_cabinet_id":"`+uuid.Nil.String()+`","scope_type":"all","adjustment_type":"delta_percent","adjustment_value":5,"scheduled_at":"2027-07-13T10:00:00Z"}`))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
