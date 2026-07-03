package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestCampaignActionAuditMetadataIncludesRealBidActionContext(t *testing.T) {
	campaignID := uuid.New()
	productID := uuid.New()
	campaign := sqlcgen.Campaign{
		ID:           uuidToPgtype(campaignID),
		WbCampaignID: 12345,
	}

	metadata := campaignActionAuditMetadata(
		campaign,
		uuidToPgtype(productID),
		98765,
		"recommendation_cluster_bid",
		320,
		360,
		"петля мебельная",
		"apply recommendation raise_bid",
		"applied",
		nil,
	)

	require.Equal(t, "recommendation_cluster_bid", metadata["action_type"])
	require.Equal(t, "applied", metadata["status"])
	require.Equal(t, campaignID, metadata["campaign_id"])
	require.Equal(t, int64(12345), metadata["wb_campaign_id"])
	require.Equal(t, productID, metadata["product_id"])
	require.Equal(t, int64(98765), metadata["wb_product_id"])
	require.Equal(t, int64(320), metadata["old_bid"])
	require.Equal(t, int64(360), metadata["new_bid"])
	require.Equal(t, "петля мебельная", metadata["norm_query"])
	require.Equal(t, "apply recommendation raise_bid", metadata["reason"])
	require.NotContains(t, metadata, "error")
}

func TestCampaignActionAuditMetadataIncludesWBError(t *testing.T) {
	campaignID := uuid.New()
	campaign := sqlcgen.Campaign{
		ID:           uuidToPgtype(campaignID),
		WbCampaignID: 12345,
	}

	metadata := campaignActionAuditMetadata(
		campaign,
		pgtype.UUID{},
		0,
		"recommendation_bid",
		320,
		360,
		"",
		"apply recommendation lower_bid",
		"failed",
		errors.New("wb api rate limited"),
	)

	require.Equal(t, "failed", metadata["status"])
	require.Equal(t, "wb api rate limited", metadata["error"])
	require.Equal(t, campaignID, metadata["campaign_id"])
	require.NotContains(t, metadata, "product_id")
	require.NotContains(t, metadata, "wb_product_id")
	require.NotContains(t, metadata, "norm_query")
}

func TestBidChangeFromSqlcMarksAppliedChangeRollbackReady(t *testing.T) {
	row := sqlcgen.BidChange{
		ID:              uuidToPgtype(uuid.New()),
		WorkspaceID:     uuidToPgtype(uuid.New()),
		SellerCabinetID: uuidToPgtype(uuid.New()),
		CampaignID:      uuidToPgtype(uuid.New()),
		Placement:       "search",
		OldBid:          320,
		NewBid:          360,
		Reason:          "manual bid change",
		Source:          "manual",
		WbStatus:        "applied",
	}

	change := bidChangeFromSqlc(row)

	require.True(t, change.CanRollback)
	require.NotNil(t, change.RollbackBid)
	require.Equal(t, 320, *change.RollbackBid)
}

func TestBidChangeFromSqlcDoesNotMarkFailedChangeRollbackReady(t *testing.T) {
	row := sqlcgen.BidChange{
		ID:              uuidToPgtype(uuid.New()),
		WorkspaceID:     uuidToPgtype(uuid.New()),
		SellerCabinetID: uuidToPgtype(uuid.New()),
		CampaignID:      uuidToPgtype(uuid.New()),
		Placement:       "search",
		OldBid:          320,
		NewBid:          360,
		Reason:          "manual bid change",
		Source:          "manual",
		WbStatus:        "failed",
	}

	change := bidChangeFromSqlc(row)

	require.False(t, change.CanRollback)
	require.Nil(t, change.RollbackBid)
}

func TestBidChangeFromSqlcExposesDecisionContextFromStoredMetric(t *testing.T) {
	row := sqlcgen.BidChange{
		ID:              uuidToPgtype(uuid.New()),
		WorkspaceID:     uuidToPgtype(uuid.New()),
		SellerCabinetID: uuidToPgtype(uuid.New()),
		CampaignID:      uuidToPgtype(uuid.New()),
		Placement:       "search",
		OldBid:          320,
		NewBid:          360,
		Reason:          "ACoS 8.0% well below target 15.0%, increasing bid",
		Source:          domain.BidSourceStrategy,
		Acos:            pgtype.Float8{Float64: 8, Valid: true},
		WbStatus:        "applied",
	}

	change := bidChangeFromSqlc(row)

	require.NotNil(t, change.DecisionContext)
	require.Equal(t, "autopilot", change.DecisionContext.ActorType)
	require.Equal(t, "acos", change.DecisionContext.PrimaryMetric)
	require.NotNil(t, change.DecisionContext.PrimaryMetricValue)
	require.Equal(t, float64(8), *change.DecisionContext.PrimaryMetricValue)
	require.Equal(t, "metric_evidence", change.DecisionContext.DataMode)
	require.Empty(t, change.DecisionContext.MissingEvidence)
}

func TestBidChangeFromSqlcReportsMissingDecisionMetric(t *testing.T) {
	row := sqlcgen.BidChange{
		ID:              uuidToPgtype(uuid.New()),
		WorkspaceID:     uuidToPgtype(uuid.New()),
		SellerCabinetID: uuidToPgtype(uuid.New()),
		CampaignID:      uuidToPgtype(uuid.New()),
		Placement:       "search",
		OldBid:          320,
		NewBid:          360,
		Source:          domain.BidSourceManual,
		WbStatus:        "applied",
	}

	change := bidChangeFromSqlc(row)

	require.NotNil(t, change.DecisionContext)
	require.Equal(t, "user", change.DecisionContext.ActorType)
	require.Equal(t, "reason_only", change.DecisionContext.DataMode)
	require.Contains(t, change.DecisionContext.MissingEvidence, "primary_metric")
	require.Contains(t, change.DecisionContext.MissingEvidence, "reason")
}

func TestValidateBidChangeRollbackTargetRequiresAppliedCampaignBid(t *testing.T) {
	campaignID := uuid.New()

	err := validateBidChangeRollbackTarget(sqlcgen.BidChange{
		CampaignID: uuidToPgtype(campaignID),
		OldBid:     320,
		NewBid:     360,
		WbStatus:   "failed",
	}, campaignID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "applied")

	err = validateBidChangeRollbackTarget(sqlcgen.BidChange{
		CampaignID: uuidToPgtype(uuid.New()),
		OldBid:     320,
		NewBid:     360,
		WbStatus:   "applied",
	}, campaignID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not belong")
}

func TestValidateBidChangeRollbackTargetAllowsAppliedCampaignBidWithPreviousBid(t *testing.T) {
	campaignID := uuid.New()

	err := validateBidChangeRollbackTarget(sqlcgen.BidChange{
		CampaignID: uuidToPgtype(campaignID),
		OldBid:     320,
		NewBid:     360,
		WbStatus:   "applied",
	}, campaignID)

	require.NoError(t, err)
}

func TestValidateBidChangeRollbackTargetAllowsAppliedClusterBidWithPhraseTarget(t *testing.T) {
	campaignID := uuid.New()

	err := validateBidChangeRollbackTarget(sqlcgen.BidChange{
		CampaignID: uuidToPgtype(campaignID),
		PhraseID:   uuidToPgtype(uuid.New()),
		OldBid:     320,
		NewBid:     360,
		WbStatus:   "applied",
	}, campaignID)

	require.NoError(t, err)
}

func TestCurrentClusterBidForRollbackRequiresSyncedRealBid(t *testing.T) {
	current, ok := currentClusterBidForRollback(sqlcgen.Phrase{
		CurrentBid: pgtype.Int8{Int64: 360, Valid: true},
	})

	require.True(t, ok)
	require.Equal(t, 360, current)

	_, ok = currentClusterBidForRollback(sqlcgen.Phrase{})
	require.False(t, ok)
}
