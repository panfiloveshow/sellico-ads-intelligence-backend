package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockStrategyService struct {
	listFn func(ctx context.Context, workspaceID uuid.UUID, sellerCabinetID *uuid.UUID, limit, offset int32) ([]domain.Strategy, error)
}

func (m *mockStrategyService) Create(context.Context, uuid.UUID, domain.Strategy) (*domain.Strategy, error) {
	return nil, nil
}

func (m *mockStrategyService) Get(context.Context, uuid.UUID, uuid.UUID) (*domain.Strategy, error) {
	return nil, nil
}

func (m *mockStrategyService) List(ctx context.Context, workspaceID uuid.UUID, sellerCabinetID *uuid.UUID, limit, offset int32) ([]domain.Strategy, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, workspaceID, sellerCabinetID, limit, offset)
}

func (m *mockStrategyService) Update(context.Context, uuid.UUID, uuid.UUID, domain.Strategy) (*domain.Strategy, error) {
	return nil, nil
}

func (m *mockStrategyService) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (m *mockStrategyService) AttachBinding(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, *uuid.UUID) (*domain.StrategyBinding, error) {
	return nil, nil
}

func (m *mockStrategyService) DetachBinding(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func TestStrategyList_WithSellerCabinetFilter(t *testing.T) {
	workspaceID := uuid.New()
	sellerCabinetID := uuid.New()
	handler := NewStrategyHandler(&mockStrategyService{
		listFn: func(_ context.Context, wsID uuid.UUID, cabinetID *uuid.UUID, limit, offset int32) ([]domain.Strategy, error) {
			assert.Equal(t, workspaceID, wsID)
			if assert.NotNil(t, cabinetID) {
				assert.Equal(t, sellerCabinetID, *cabinetID)
			}
			assert.Equal(t, int32(50), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Strategy{{
				ID:              uuid.New(),
				WorkspaceID:     workspaceID,
				SellerCabinetID: sellerCabinetID,
				Name:            "ACoS guard",
				Type:            domain.StrategyTypeACoS,
				IsActive:        true,
			}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/strategies?seller_cabinet_id="+sellerCabinetID.String()+"&per_page=50", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestStrategyList_InvalidSellerCabinetID(t *testing.T) {
	handler := NewStrategyHandler(&mockStrategyService{
		listFn: func(context.Context, uuid.UUID, *uuid.UUID, int32, int32) ([]domain.Strategy, error) {
			t.Fatal("list should not be called")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/strategies?seller_cabinet_id=bad-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
