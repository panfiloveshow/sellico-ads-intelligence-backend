package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestValidateCampaignProductMutationCapabilityFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		capability pgtype.Bool
		wantErr    bool
		message    string
	}{
		{name: "unknown", capability: pgtype.Bool{}, wantErr: true, message: "unknown"},
		{name: "forbidden", capability: pgtype.Bool{Bool: false, Valid: true}, wantErr: true, message: "does not allow"},
		{name: "allowed", capability: pgtype.Bool{Bool: true, Valid: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCampaignProductMutationCapability(sqlcgen.Campaign{CanChangeNms: tt.capability})
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, apperror.Is(err, apperror.ErrValidation))
			require.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestCampaignProductResultForAdvertSelectsMatchingCampaign(t *testing.T) {
	response := wb.CampaignProductUpdatesResponse{NMs: []wb.CampaignProductUpdateResult{
		{AdvertID: 11, NMs: wb.CampaignProductNMChangeResult{Added: []int64{111}}},
		{AdvertID: 22, NMs: wb.CampaignProductNMChangeResult{Deleted: []int64{222}}},
	}}

	result := campaignProductResultForAdvert(response, 22)
	require.Equal(t, int64(22), result.AdvertID)
	require.Equal(t, []int64{222}, result.NMs.Deleted)
}

func TestCampaignProductEndpointRateLimitUsesRetryAfterAndSafeFallback(t *testing.T) {
	require.Equal(t, time.Second, wbEndpointFallbackDelay(wbEndpointCampaignProducts))

	retryAfter := 37 * time.Second
	next, seconds := wbRateLimitWindowFromError(wbEndpointCampaignProducts, &wb.APIError{
		StatusCode: 429,
		RetryAfter: retryAfter,
		Message:    "rate limited",
	})
	require.Equal(t, 37, seconds)
	require.WithinDuration(t, time.Now().UTC().Add(retryAfter), next, time.Second)
}
