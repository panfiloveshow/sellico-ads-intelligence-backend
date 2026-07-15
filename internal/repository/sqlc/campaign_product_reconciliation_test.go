package sqlcgen

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeleteStaleCampaignProductsRequiresCompleteMembershipSnapshot(t *testing.T) {
	queries := &Queries{}
	_, err := queries.DeleteStaleCampaignProducts(context.Background(), DeleteStaleCampaignProductsParams{})
	require.ErrorContains(t, err, "membership snapshot is not complete")
}

func TestDeleteStaleCampaignProductsIsTenantScopedAndSupportsAuthoritativeEmptySet(t *testing.T) {
	t.Parallel()
	require.Contains(t, campaignProductMembershipAdvisoryLock, "pg_advisory_xact_lock")
	require.Contains(t, campaignProductMembershipAdvisoryLock, "campaign-product-membership")
	require.Contains(t, deleteStaleCampaignProducts, "campaign_id = $1")
	require.Contains(t, deleteStaleCampaignProducts, "workspace_id = $2")
	require.Contains(t, deleteStaleCampaignProducts, "seller_cabinet_id = $3")
	require.Contains(t, deleteStaleCampaignProducts, "wb_campaign_id = $4")
	require.Contains(t, deleteStaleCampaignProducts, "NOT (wb_product_id = ANY($5::bigint[]))")
	require.NotContains(t, deleteStaleCampaignProducts, "can_change_nms")
}
