package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestBidChangeExportRowsUsesStoredAuditFields(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	campaignID := uuid.New()
	productID := uuid.New()
	phraseID := uuid.New()
	strategyID := uuid.New()
	recommendationID := uuid.New()
	createdAt := time.Date(2026, 5, 28, 10, 30, 0, 0, time.UTC)

	rows := bidChangeExportRows([]sqlcgen.BidChange{{
		ID:               uuidToPgtype(uuid.New()),
		WorkspaceID:      uuidToPgtype(workspaceID),
		SellerCabinetID:  uuidToPgtype(cabinetID),
		CampaignID:       uuidToPgtype(campaignID),
		ProductID:        uuidToPgtype(productID),
		PhraseID:         uuidToPgtype(phraseID),
		StrategyID:       uuidToPgtype(strategyID),
		RecommendationID: uuidToPgtype(recommendationID),
		Placement:        "search",
		OldBid:           300,
		NewBid:           360,
		Reason:           "ACoS ниже цели",
		Source:           domain.BidSourceStrategy,
		Acos:             pgtype.Float8{Float64: 7.8, Valid: true},
		Roas:             pgtype.Float8{Float64: 12.3, Valid: true},
		WbStatus:         "applied",
		CreatedAt:        pgtype.Timestamptz{Time: createdAt, Valid: true},
	}})

	if len(rows) != 1 {
		t.Fatalf("expected one bid change export row, got %d", len(rows))
	}
	row := rows[0]
	if row[1] != workspaceID.String() || row[2] != cabinetID.String() || row[3] != campaignID.String() {
		t.Fatalf("expected real workspace/cabinet/campaign ids, got %+v", row)
	}
	if row[4] != productID.String() || row[5] != phraseID.String() || row[6] != strategyID.String() || row[7] != recommendationID.String() {
		t.Fatalf("expected real product/phrase/strategy/recommendation ids, got %+v", row)
	}
	if row[8] != "search" || row[9] != "300" || row[10] != "360" || row[11] != "ACoS ниже цели" {
		t.Fatalf("expected stored bid change details, got %+v", row)
	}
	if row[13] != "7.80" || row[14] != "12.30" || row[15] != "applied" || row[16] != "2026-05-28T10:30:00Z" {
		t.Fatalf("expected stored metrics/status/timestamp, got %+v", row)
	}
}

func TestFilterBidChangesByDateUsesCreatedAt(t *testing.T) {
	inRangeID := uuid.New()
	outOfRangeID := uuid.New()
	filtersRaw := json.RawMessage(`{"date_from":"2026-05-28","date_to":"2026-05-28"}`)
	filters, err := parseExportFilters(filtersRaw)
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}

	rows := filterBidChangesByDate([]sqlcgen.BidChange{
		{
			ID:        uuidToPgtype(outOfRangeID),
			CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 27, 23, 59, 0, 0, time.UTC), Valid: true},
		},
		{
			ID:        uuidToPgtype(inRangeID),
			CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 28, 18, 0, 0, 0, time.UTC), Valid: true},
		},
	}, filters)

	if len(rows) != 1 || uuidFromPgtype(rows[0].ID) != inRangeID {
		t.Fatalf("expected only created_at within selected export day, got %+v", rows)
	}
}
