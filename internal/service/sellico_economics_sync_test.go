package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
)

func TestBuildEconomicsInputs(t *testing.T) {
	comm := 15.0
	tax := 6.0
	rows := []sellico.WBUnitEconomics{
		{NmID: 100, CostPrice: 349.6, CommissionPercent: &comm, TaxPercent: &tax}, // rounds to 350
		{NmID: 0, CostPrice: 200},   // dropped: no nmID
		{NmID: 101, CostPrice: 0},   // dropped: no cost
		{NmID: 102, CostPrice: 500}, // no commission/tax
	}

	got := buildEconomicsInputs(rows)
	if len(got) != 2 {
		t.Fatalf("expected 2 valid inputs, got %d", len(got))
	}

	if got[0].WBProductID != 100 || got[0].CostPrice == nil || *got[0].CostPrice != 350 {
		t.Fatalf("row 0: want nm=100 cost=350, got nm=%d cost=%v", got[0].WBProductID, got[0].CostPrice)
	}
	if got[0].CommissionPercent == nil || *got[0].CommissionPercent != 15 {
		t.Fatalf("row 0: commission not carried through: %v", got[0].CommissionPercent)
	}
	if got[0].Source != "sellico" {
		t.Fatalf("row 0: want source=sellico, got %q", got[0].Source)
	}
	if got[1].WBProductID != 102 || got[1].CommissionPercent != nil {
		t.Fatalf("row 1: want nm=102 with nil commission, got nm=%d comm=%v", got[1].WBProductID, got[1].CommissionPercent)
	}
}

func TestImportedPricePresentation(t *testing.T) {
	sppValue := 40.12
	customerValue := 946.6
	spp, customer := importedPricePresentation(sellico.WBUnitEconomics{
		SppPercent:    &sppValue,
		CustomerPrice: &customerValue,
	})

	if !spp.Valid || spp.Float64 != 40.12 {
		t.Fatalf("expected SPP 40.12, got %+v", spp)
	}
	if !customer.Valid || customer.Int64 != 947 {
		t.Fatalf("expected rounded customer price 947, got %+v", customer)
	}
}
