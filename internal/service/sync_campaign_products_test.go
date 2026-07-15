package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestPartialCampaignMembershipDoesNotTouchLocalLinks(t *testing.T) {
	service := &SyncService{}
	linked, err := service.upsertCampaignProductLinks(
		context.Background(), uuid.New(), uuid.New(), sqlcgen.Campaign{}, []int64{111}, nil, false,
	)
	require.NoError(t, err)
	require.Zero(t, linked)
}

func TestNormalizeCampaignMembershipNMIDsKeepsOnlyUniquePositiveRealIDs(t *testing.T) {
	require.Equal(t, []int64{111, 222}, normalizeCampaignMembershipNMIDs([]int64{0, 111, -3, 111, 222}))
	require.Empty(t, normalizeCampaignMembershipNMIDs(nil))
}
