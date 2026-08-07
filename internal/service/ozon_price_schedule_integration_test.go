package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func TestOzonSchedule_CreateValidation(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-validate")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})
	future := time.Now().Add(2 * time.Hour)

	cases := []struct {
		name string
		in   domain.OzonPriceScheduleInput
	}{
		{"bad sku", domain.OzonPriceScheduleInput{SKU: 0, ScheduledPriceRub: 100, StartsAt: future}},
		{"bad price", domain.OzonPriceScheduleInput{SKU: 1, ScheduledPriceRub: 0, StartsAt: future}},
		{"past start", domain.OzonPriceScheduleInput{SKU: 1, ScheduledPriceRub: 100, StartsAt: time.Now().Add(-time.Hour)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateSchedule(ctx, fx.workspaceID, fx.cabinetID, tc.in)
			require.Error(t, err)
		})
	}
}

func TestOzonSchedule_CreateListCancel(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-crud")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 700, OfferID: "ART-700", PriceRub: 1000, NetPriceRub: 400, CommissionFBOPct: 20,
	})

	future := time.Now().Add(3 * time.Hour)
	end := future.Add(6 * time.Hour)
	entry, err := svc.CreateSchedule(ctx, fx.workspaceID, fx.cabinetID, domain.OzonPriceScheduleInput{
		SKU: 700, ScheduledPriceRub: 300, StartsAt: future, EndsAt: &end, // below floor -> warning
	})
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.NotEmpty(t, entry.Warning, "price below floor should warn")
	assert.EqualValues(t, 700, entry.SKU)

	list, total, err := svc.ListSchedules(ctx, fx.workspaceID, fx.cabinetID, "", 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, list, 1)

	_, pendingTotal, err := svc.ListSchedules(ctx, fx.workspaceID, fx.cabinetID, domain.OzonScheduleStatusPending, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, pendingTotal)

	require.NoError(t, svc.CancelSchedule(ctx, fx.workspaceID, entry.ID))

	t.Run("cancel non-pending rejected", func(t *testing.T) {
		err := svc.CancelSchedule(ctx, fx.workspaceID, entry.ID)
		require.Error(t, err)
	})

	t.Run("cancel missing NotFound", func(t *testing.T) {
		err := svc.CancelSchedule(ctx, fx.workspaceID, uuid.New())
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})
}

func TestOzonSchedule_ExecuteDueApplies(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-execute")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: 701, OfferID: "ART-701", PriceRub: 1000})

	// Schedule a change starting in the near future, then execute "later".
	start := time.Now().Add(time.Minute)
	entry, err := svc.CreateSchedule(ctx, fx.workspaceID, fx.cabinetID, domain.OzonPriceScheduleInput{
		SKU: 701, ScheduledPriceRub: 850, StartsAt: start,
	})
	require.NoError(t, err)

	executed, err := svc.ExecuteDueSchedules(ctx, start.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, executed)
	require.Len(t, writer.calls, 1)

	fresh, err := fx.db.Queries.GetOzonPriceScheduleEntry(ctx, uuidToPgtype(entry.ID))
	require.NoError(t, err)
	assert.Equal(t, domain.OzonScheduleStatusApplied, fresh.Status)
}

func TestOzonSchedule_ExecuteRevert(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-revert")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	// Mirror at 1000; schedule a sale to 800 that ends soon and auto-reverts to
	// the current price (no explicit revert price).
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: 720, OfferID: "ART-720", PriceRub: 1000})
	start := time.Now().Add(time.Minute)
	end := start.Add(2 * time.Minute)
	entry, err := svc.CreateSchedule(ctx, fx.workspaceID, fx.cabinetID, domain.OzonPriceScheduleInput{
		SKU: 720, ScheduledPriceRub: 800, StartsAt: start, EndsAt: &end,
	})
	require.NoError(t, err)
	require.NotNil(t, entry.RevertPriceRub)

	// Apply the sale.
	_, err = svc.ExecuteDueSchedules(ctx, start.Add(time.Minute))
	require.NoError(t, err)
	applied, err := fx.db.Queries.GetOzonPriceScheduleEntry(ctx, uuidToPgtype(entry.ID))
	require.NoError(t, err)
	require.Equal(t, domain.OzonScheduleStatusApplied, applied.Status)

	// After ends_at, the revert fires.
	executed, err := svc.ExecuteDueSchedules(ctx, end.Add(time.Minute))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, executed, 1)
	reverted, err := fx.db.Queries.GetOzonPriceScheduleEntry(ctx, uuidToPgtype(entry.ID))
	require.NoError(t, err)
	assert.Equal(t, domain.OzonScheduleStatusReverted, reverted.Status)
}

func TestOzonSchedule_ExecutePausedDefers(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-paused")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: 702, OfferID: "ART-702", PriceRub: 1000})
	start := time.Now().Add(time.Minute)
	_, err := svc.CreateSchedule(ctx, fx.workspaceID, fx.cabinetID, domain.OzonPriceScheduleInput{
		SKU: 702, ScheduledPriceRub: 850, StartsAt: start,
	})
	require.NoError(t, err)
	setCabinetPaused(t, fx.db, fx.cabinetID, time.Now().Add(2*time.Hour))

	executed, err := svc.ExecuteDueSchedules(ctx, start.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 0, executed)
	assert.Empty(t, writer.calls)
}

func TestOzonSchedule_SetPauseAndHealth(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-health")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 703, OfferID: "ART-703", PriceRub: 100, NetPriceRub: 400, CommissionFBOPct: 20, // below floor
	})

	t.Run("pause bounds", func(t *testing.T) {
		_, err := svc.SetPause(ctx, fx.workspaceID, fx.cabinetID, -1)
		require.Error(t, err)
		_, err = svc.SetPause(ctx, fx.workspaceID, fx.cabinetID, 100000)
		require.Error(t, err)
	})

	until, err := svc.SetPause(ctx, fx.workspaceID, fx.cabinetID, 5)
	require.NoError(t, err)
	require.NotNil(t, until)

	health, err := svc.Health(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	require.NotNil(t, health)
	require.NotNil(t, health.PausedUntil)
	assert.GreaterOrEqual(t, health.ProductsBelowFloor, 1)

	// Unpause.
	until, err = svc.SetPause(ctx, fx.workspaceID, fx.cabinetID, 0)
	require.NoError(t, err)
	assert.Nil(t, until)
}

func TestOzonCredentials_DecryptGate(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-creds-decrypt")
	defer cleanup()
	ctx := context.Background()

	cabSvc := &SellerCabinetService{queries: fx.db.Queries, encryptionKey: ozonTestKey()}

	// SellerCabinetService.decryptOzonCredentials: ozon cabinet decrypts.
	sync := newSyncService(fx.db)
	cab, err := sync.loadOzonCabinet(ctx, fx.cabinetID)
	require.NoError(t, err)
	creds, err := cabSvc.decryptOzonCredentials(*cab)
	require.NoError(t, err)
	assert.Equal(t, "seller-client", creds.ClientID)

	// A WB cabinet is rejected by decryptOzonCredentials' marketplace gate.
	_, err = cabSvc.decryptOzonCredentials(domain.SellerCabinet{Marketplace: domain.MarketplaceWB})
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))

	// credentials() helper on the sync service.
	credsFromSync, err := sync.credentials(*cab)
	require.NoError(t, err)
	assert.Equal(t, "seller-key", credsFromSync.APIKey)

	// A WB cabinet is rejected by the marketplace gate in loadOzonCabinet.
	wb := seedWBCabinet(t, fx.db, fx.workspaceID)
	_, err = sync.loadOzonCabinet(ctx, wb)
	require.Error(t, err)
}
