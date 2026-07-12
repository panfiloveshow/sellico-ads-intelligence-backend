package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestTargetNmIDsDoesNotExpandUnresolvedBindings(t *testing.T) {
	cabinetID := uuid.New()
	missingProductID := uuid.New()
	svc := &RepricerService{}
	data := &repricerData{
		pricesByNm: map[int64]domain.ProductPrice{
			1: {SellerCabinetID: cabinetID, WBProductID: 1},
			2: {SellerCabinetID: cabinetID, WBProductID: 2},
		},
		nmByProductID: map[uuid.UUID]int64{},
	}
	strategy := domain.Strategy{
		SellerCabinetID: cabinetID,
		Bindings:        []domain.StrategyBinding{{ProductID: &missingProductID}},
	}

	assert.Empty(t, svc.targetNmIDs(strategy, data))
}

func TestConservativeCommissionUsesHighestRealCandidate(t *testing.T) {
	commission, ok := conservativeCommission(sqlcgen.WBCommissionTariff{
		KGVPBooking:     pgtype.Float8{Float64: 12, Valid: true},
		KGVPMarketplace: pgtype.Float8{Float64: 18.5, Valid: true},
	})
	assert.True(t, ok)
	assert.Equal(t, 18.5, commission)
}
