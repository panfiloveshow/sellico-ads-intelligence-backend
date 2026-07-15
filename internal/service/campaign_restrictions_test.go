package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestCampaignFromSqlcPreservesNullableCanChangeNMs(t *testing.T) {
	tests := []struct {
		name  string
		value pgtype.Bool
		want  *bool
	}{
		{name: "allowed", value: pgtype.Bool{Bool: true, Valid: true}, want: boolPointer(true)},
		{name: "forbidden", value: pgtype.Bool{Bool: false, Valid: true}, want: boolPointer(false)},
		{name: "unknown", value: pgtype.Bool{}, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			campaign := campaignFromSqlc(sqlcgen.Campaign{CanChangeNms: tt.value})
			if tt.want == nil {
				assert.Nil(t, campaign.CanChangeNMs)
				return
			}
			require.NotNil(t, campaign.CanChangeNMs)
			assert.Equal(t, *tt.want, *campaign.CanChangeNMs)
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}
