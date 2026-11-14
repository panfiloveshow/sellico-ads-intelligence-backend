package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

func TestOzonPriceReadOnlyReconciliationHandler(t *testing.T) {
	ws, id := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name, raw string
		upstream  error
		status    int
	}{
		{"unknown remains unknown", id.String(), nil, 200},
		{"invalid id", "invalid", nil, 400},
		{"foreign or missing row", id.String(), apperror.ErrNotFound, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
				reconcilePriceFn: func(_ context.Context, actualWS, actualID uuid.UUID) (*domain.OzonPriceChange, error) {
					reads++
					require.Equal(t, ws, actualWS)
					require.Equal(t, id, actualID)
					return &domain.OzonPriceChange{ID: id, Status: "submit_unknown"}, tc.upstream
				},
				applyManualFn: func(context.Context, uuid.UUID, uuid.UUID, []service.OzonManualPriceInput, uuid.UUID) ([]domain.OzonPriceChange, error) {
					t.Fatal("reconciliation attempted a price write")
					return nil, nil
				},
			}, nil)
			r := ozonReq(t, http.MethodPost, "/ozon/price-changes/"+tc.raw+"/reconcile", "", ws)
			r = withURLParam(r, "id", tc.raw)
			w := httptest.NewRecorder()
			h.ReconcilePriceChange(w, r)
			require.Equal(t, tc.status, w.Code)
			if tc.status == 200 {
				require.Contains(t, w.Body.String(), `"status":"submit_unknown"`)
				require.Equal(t, 1, reads)
			}
			if tc.status == 400 {
				require.Zero(t, reads)
			}
		})
	}
}

func TestOzonPriceRollbackReturnsUncertainRecordedRow(t *testing.T) {
	ws, original, reverse := uuid.New(), uuid.New(), uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
		rollbackFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.OzonPriceChange, error) {
			return &domain.OzonPriceChange{ID: reverse, Status: "submit_unknown"}, errors.New("response lost")
		},
	}, nil)
	r := withURLParam(ozonReq(t, http.MethodPost, "/ozon/price-changes/"+original.String()+"/rollback", "", ws), "id", original.String())
	w := httptest.NewRecorder()
	h.RollbackPriceChange(w, r)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), reverse.String())
	require.Contains(t, w.Body.String(), `"status":"submit_unknown"`)
}
