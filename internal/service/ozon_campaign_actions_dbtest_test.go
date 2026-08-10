package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

func TestOzonCampaignActions_SetProductBids_AppliedAndMirrored(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 501, "Кампания", "CAMPAIGN_STATE_RUNNING")
	testdb.OzonCampaignProduct(t, env.pool, campaignID, 555, 10)
	testdb.OzonCampaignProduct(t, env.pool, campaignID, 777, 7)

	targetCIR := 12.0
	changes, err := env.actionsSvc.SetProductBids(ctx, env.workspaceID, campaignID, []OzonBidInput{
		{SKU: 555, BidRub: 15.5},
		{SKU: 777, BidRub: 8, TargetCIR: &targetCIR},
	})
	require.NoError(t, err)
	require.Len(t, changes, 2)
	for _, change := range changes {
		assert.Equal(t, domain.OzonBidStatusApplied, change.Status)
		assert.Empty(t, change.Error)
	}

	// Audit rows: pending → applied, old bid captured from the mirror.
	var status string
	var oldBid, newBid float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT status, old_bid_rub, new_bid_rub FROM ozon_bid_changes
		 WHERE campaign_id = $1 AND sku = 555`, campaignID).Scan(&status, &oldBid, &newBid))
	assert.Equal(t, domain.OzonBidStatusApplied, status)
	assert.InDelta(t, 10.0, oldBid, 0.001)
	assert.InDelta(t, 15.5, newBid, 0.001)
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_bid_changes WHERE campaign_id = $1 AND status = 'applied' AND applied_at IS NOT NULL`,
		campaignID))
	assert.Equal(t, "manual", changes[0].Source)
	assert.Equal(t, domain.OzonBidKindCPC, changes[0].Kind)

	// Mirror updated (bid + target_cir).
	var mirrorBid, mirrorCIR float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT bid_rub, COALESCE(target_cir, 0) FROM ozon_campaign_products
		 WHERE campaign_id = $1 AND sku = 777`, campaignID).Scan(&mirrorBid, &mirrorCIR))
	assert.InDelta(t, 8.0, mirrorBid, 0.001)
	assert.InDelta(t, 12.0, mirrorCIR, 0.001)

	// The write hit Ozon with micro-ruble strings.
	body := env.fake.lastBody("/api/client/campaign/501/products")
	assert.Contains(t, body, `"sku":"555"`)
	assert.Contains(t, body, `"bid":"15500000"`)
	assert.Contains(t, body, `"targetCir":"12"`)
}

func TestOzonCampaignActions_SetProductBids_APIFailureMarksFailed(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 502, "Кампания", "CAMPAIGN_STATE_RUNNING")
	testdb.OzonCampaignProduct(t, env.pool, campaignID, 555, 10)
	env.fake.fail("/api/client/campaign/502/products", 400)

	changes, err := env.actionsSvc.SetProductBids(ctx, env.workspaceID, campaignID, []OzonBidInput{
		{SKU: 555, BidRub: 20},
	})
	require.Error(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, domain.OzonBidStatusFailed, changes[0].Status)
	assert.NotEmpty(t, changes[0].Error)

	// Audit row failed with the error text; the mirror keeps the old bid.
	var status, errText string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT status, error FROM ozon_bid_changes WHERE campaign_id = $1 AND sku = 555`,
		campaignID).Scan(&status, &errText))
	assert.Equal(t, domain.OzonBidStatusFailed, status)
	assert.Contains(t, errText, "400")

	var mirrorBid float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT bid_rub FROM ozon_campaign_products WHERE campaign_id = $1 AND sku = 555`,
		campaignID).Scan(&mirrorBid))
	assert.InDelta(t, 10.0, mirrorBid, 0.001)
}

func TestOzonCampaignActions_SetProductBids_Validation(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 503, "Кампания", "CAMPAIGN_STATE_RUNNING")

	_, err := env.actionsSvc.SetProductBids(ctx, env.workspaceID, campaignID, nil)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))

	_, err = env.actionsSvc.SetProductBids(ctx, env.workspaceID, campaignID, []OzonBidInput{{SKU: 555, BidRub: 0}})
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))

	// No audit rows must be written for rejected requests.
	assert.Equal(t, 0, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_bid_changes WHERE campaign_id = $1`, campaignID))
}

func TestOzonCampaignActions_ListBidChanges_ByCampaignWithoutCabinet(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 504, "Кампания", "CAMPAIGN_STATE_RUNNING")
	otherCampaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 505, "Другая", "CAMPAIGN_STATE_RUNNING")
	testdb.OzonCampaignProduct(t, env.pool, campaignID, 555, 10)
	testdb.OzonCampaignProduct(t, env.pool, otherCampaignID, 777, 5)
	testdb.OzonProduct(t, env.pool, env.cabinetID, 9001, 555, "ART-1", "Товар первый")

	_, err := env.actionsSvc.SetProductBids(ctx, env.workspaceID, campaignID, []OzonBidInput{{SKU: 555, BidRub: 11}})
	require.NoError(t, err)
	_, err = env.actionsSvc.SetProductBids(ctx, env.workspaceID, otherCampaignID, []OzonBidInput{{SKU: 777, BidRub: 6}})
	require.NoError(t, err)

	// campaign_id alone resolves the cabinet (campaign detail page contract).
	changes, total, err := env.actionsSvc.ListBidChanges(ctx, env.workspaceID, uuid.Nil, &campaignID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, changes, 1)
	require.NotNil(t, changes[0].SKU)
	assert.Equal(t, int64(555), *changes[0].SKU)
	assert.Equal(t, "Товар первый", changes[0].Name) // enriched from ozon_products

	// Cabinet-wide listing sees both campaigns' rows.
	all, allTotal, err := env.actionsSvc.ListBidChanges(ctx, env.workspaceID, env.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), allTotal)
	assert.Len(t, all, 2)

	// Neither cabinet nor campaign → validation error.
	_, _, err = env.actionsSvc.ListBidChanges(ctx, env.workspaceID, uuid.Nil, nil, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))

	// Tenancy: a foreign workspace can list nothing through either path.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, _, err = env.actionsSvc.ListBidChanges(ctx, otherWorkspace, env.cabinetID, nil, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	_, _, err = env.actionsSvc.ListBidChanges(ctx, otherWorkspace, uuid.Nil, &campaignID, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonCampaignActions_CPOEnableDisable_AuditedAndMirrored(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	require.NoError(t, env.actionsSvc.EnableCPO(ctx, env.workspaceID, env.cabinetID, []int64{555, 777}))

	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_bid_changes
		 WHERE seller_cabinet_id = $1 AND kind = 'cpo' AND status = 'applied'`, env.cabinetID))
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_cpo_products WHERE seller_cabinet_id = $1 AND enabled`, env.cabinetID))
	assert.Contains(t, env.fake.lastBody("/api/client/search_promo/product/enable"), `"555"`)

	require.NoError(t, env.actionsSvc.DisableCPO(ctx, env.workspaceID, env.cabinetID, []int64{777}))
	var enabled bool
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT enabled FROM ozon_cpo_products WHERE seller_cabinet_id = $1 AND sku = 777`,
		env.cabinetID).Scan(&enabled))
	assert.False(t, enabled)

	// API failure → audit rows failed, mirror untouched.
	env.fake.fail("/api/client/search_promo/product/enable", 400)
	err := env.actionsSvc.EnableCPO(ctx, env.workspaceID, env.cabinetID, []int64{888})
	require.Error(t, err)
	var failedStatus string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT status FROM ozon_bid_changes WHERE seller_cabinet_id = $1 AND sku = 888`,
		env.cabinetID).Scan(&failedStatus))
	assert.Equal(t, domain.OzonBidStatusFailed, failedStatus)
	assert.Equal(t, 0, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_cpo_products WHERE seller_cabinet_id = $1 AND sku = 888`, env.cabinetID))
}

func TestOzonCampaignActions_SetCPOBids_AuditedAndMirrored(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	testdb.OzonCPOProduct(t, env.pool, env.cabinetID, 555, true, 5)

	require.NoError(t, env.actionsSvc.SetCPOBids(ctx, env.workspaceID, env.cabinetID, []OzonCPOBidInput{
		{SKU: 555, BidRub: 7.5},
	}))

	var status string
	var oldBid, newBid float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT status, old_bid_rub, new_bid_rub FROM ozon_bid_changes
		 WHERE seller_cabinet_id = $1 AND sku = 555 AND kind = 'cpo'`,
		env.cabinetID).Scan(&status, &oldBid, &newBid))
	assert.Equal(t, domain.OzonBidStatusApplied, status)
	assert.InDelta(t, 5.0, oldBid, 0.001)
	assert.InDelta(t, 7.5, newBid, 0.001)

	var mirrorBid float64
	var bidKind string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT bid, bid_kind FROM ozon_cpo_products WHERE seller_cabinet_id = $1 AND sku = 555`,
		env.cabinetID).Scan(&mirrorBid, &bidKind))
	assert.InDelta(t, 7.5, mirrorBid, 0.001)
	assert.Equal(t, "fixed_rub", bidKind)

	// CPO bids ride the plain-ruble surface (no micro conversion).
	assert.Contains(t, env.fake.lastBody("/api/client/campaign/search_promo/v2/bids/set"), `"bid":7.5`)
}

func TestOzonCampaignActions_CampaignLifecycleAndBudget(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 506, "Кампания", "CAMPAIGN_STATE_INACTIVE")

	require.NoError(t, env.actionsSvc.ActivateCampaign(ctx, env.workspaceID, campaignID))
	assert.Equal(t, 1, env.fake.callCount("/api/client/campaign/506/activate"))
	var state string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT state FROM ozon_campaigns WHERE id = $1`, campaignID).Scan(&state))
	assert.Equal(t, "CAMPAIGN_STATE_RUNNING", state)

	require.NoError(t, env.actionsSvc.DeactivateCampaign(ctx, env.workspaceID, campaignID))
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT state FROM ozon_campaigns WHERE id = $1`, campaignID).Scan(&state))
	assert.Equal(t, "CAMPAIGN_STATE_INACTIVE", state)

	daily := int64(3000)
	require.NoError(t, env.actionsSvc.UpdateBudget(ctx, env.workspaceID, campaignID, &daily, nil))
	assert.Contains(t, env.fake.lastBody("/api/client/campaign/506"), `"dailyBudget":"3000000000"`)
	var dailyMirror int64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT daily_budget_rub FROM ozon_campaigns WHERE id = $1`, campaignID).Scan(&dailyMirror))
	assert.Equal(t, int64(3000), dailyMirror)

	// Validation: empty patch and negative budgets never reach the API.
	err := env.actionsSvc.UpdateBudget(ctx, env.workspaceID, campaignID, nil, nil)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))
	negative := int64(-1)
	err = env.actionsSvc.UpdateBudget(ctx, env.workspaceID, campaignID, &negative, nil)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))

	// Tenancy: a foreign workspace cannot touch the campaign.
	otherWorkspace := testdb.Workspace(t, env.pool)
	err = env.actionsSvc.ActivateCampaign(ctx, otherWorkspace, campaignID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonCampaignActions_GetCompetitiveBids(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 507, "Кампания", "CAMPAIGN_STATE_RUNNING")

	// No products yet → empty result without touching the API.
	bids, err := env.actionsSvc.GetCompetitiveBids(ctx, env.workspaceID, campaignID)
	require.NoError(t, err)
	assert.Empty(t, bids)
	assert.Equal(t, 0, env.fake.callCount("/api/client/campaign/507/products/bids/competitive"))

	testdb.OzonCampaignProduct(t, env.pool, campaignID, 555, 10)
	env.fake.setJSON("/api/client/campaign/507/products/bids/competitive", map[string]any{
		"bids": []map[string]any{{"sku": "555", "bid": "15000000"}},
	})
	bids, err = env.actionsSvc.GetCompetitiveBids(ctx, env.workspaceID, campaignID)
	require.NoError(t, err)
	require.Len(t, bids, 1)
	assert.Equal(t, int64(555), bids[0].SKU)
	assert.InDelta(t, 15.0, bids[0].BidRub, 0.001) // micro-rubles converted

	// Upstream refusal maps to the readable Ozon API error, not a 500.
	env.fake.fail("/api/client/campaign/507/products/bids/competitive", 400)
	_, err = env.actionsSvc.GetCompetitiveBids(ctx, env.workspaceID, campaignID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrOzonAPIError))
}

func TestOzonCampaignActions_ListCPOProducts_RefreshesMirror(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	testdb.OzonProduct(t, env.pool, env.cabinetID, 9001, 555, "ART-1", "Товар первый")
	env.fake.setJSON("/api/client/campaign/search_promo/v2/products", map[string]any{
		"products": []map[string]any{
			{"sku": 555, "bid": 7, "enabled": true},
			{"sku": "777", "searchPromoStatus": "SEARCH_PROMO_STATUS_ENABLED"},
			{"sku": 888, "isEnabled": false},
		},
	})

	products, total, err := env.actionsSvc.ListCPOProducts(ctx, env.workspaceID, env.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, products, 3)

	bySKU := map[int64]int{}
	for i, product := range products {
		bySKU[product.SKU] = i
	}
	withBid := products[bySKU[555]]
	assert.True(t, withBid.Enabled)
	require.NotNil(t, withBid.Bid)
	assert.InDelta(t, 7.0, *withBid.Bid, 0.001)
	assert.Equal(t, "fixed_rub", withBid.BidKind)
	assert.Equal(t, "Товар первый", withBid.Name) // enriched from ozon_products
	assert.True(t, products[bySKU[777]].Enabled)  // status-string variant
	assert.False(t, products[bySKU[888]].Enabled) // isEnabled variant

	// API failure serves the stale mirror instead of erroring.
	env.fake.fail("/api/client/campaign/search_promo/v2/products", 400)
	products, total, err = env.actionsSvc.ListCPOProducts(ctx, env.workspaceID, env.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, products, 3)
}

func TestOzonCampaignActions_ListCPOProducts_PersistsEnrichedFields(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	env.fake.setJSON("/api/client/campaign/search_promo/v2/products", map[string]any{
		"products": []map[string]any{
			{
				"sku":             "2011312046",
				"sourceSku":       "A61",
				"title":           "Товар А61",
				"imageUrl":        "https://cdn/x.jpg",
				"price":           "752",
				"bid":             0,
				"bidPrice":        "0",
				"visibilityIndex": "10+",
			},
		},
	})

	products, total, err := env.actionsSvc.ListCPOProducts(ctx, env.workspaceID, env.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, products, 1)
	p := products[0]
	assert.Equal(t, int64(2011312046), p.SKU)
	assert.True(t, p.Enabled)
	assert.Equal(t, "A61", p.OfferID) // sourceSku → offer_id (no ozon_products row)
	assert.Equal(t, "Товар А61", p.Name)
	assert.Equal(t, "https://cdn/x.jpg", p.ImageURL)
	assert.Equal(t, "10+", p.VisibilityIndex)
	require.NotNil(t, p.PriceRub)
	assert.InDelta(t, 752.0, *p.PriceRub, 0.001)

	// Enrichment survives an API failure: the mirror still carries the fields.
	env.fake.fail("/api/client/campaign/search_promo/v2/products", 400)
	stale, _, err := env.actionsSvc.ListCPOProducts(ctx, env.workspaceID, env.cabinetID, 50, 0)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "A61", stale[0].OfferID)
	assert.Equal(t, "https://cdn/x.jpg", stale[0].ImageURL)
	require.NotNil(t, stale[0].PriceRub)
	assert.InDelta(t, 752.0, *stale[0].PriceRub, 0.001)

	// ozon_products mapping wins over the CPO API title/sourceSku when present.
	testdb.OzonProduct(t, env.pool, env.cabinetID, 9001, 2011312046, "ART-MAP", "Имя из каталога")
	env.fake.setJSON("/api/client/campaign/search_promo/v2/products", map[string]any{
		"products": []map[string]any{{"sku": "2011312046", "sourceSku": "A61", "title": "Товар А61"}},
	})
	mapped, _, err := env.actionsSvc.ListCPOProducts(ctx, env.workspaceID, env.cabinetID, 50, 0)
	require.NoError(t, err)
	require.Len(t, mapped, 1)
	assert.Equal(t, "Имя из каталога", mapped[0].Name)
	assert.Equal(t, "ART-MAP", mapped[0].OfferID)
}

func TestOzonCampaignActions_GetBidLimits_CachedPassthrough(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	env.fake.setJSON("/api/client/limits/list", map[string]any{"limits": []map[string]any{{"type": "CPC"}}})

	payload, err := env.actionsSvc.GetBidLimits(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.Contains(t, string(payload), "CPC")

	// Second read is served from the per-cabinet cache.
	_, err = env.actionsSvc.GetBidLimits(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.Equal(t, 1, env.fake.callCount("/api/client/limits/list"))

	// Tenancy still applies to cache hits.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, err = env.actionsSvc.GetBidLimits(ctx, otherWorkspace, env.cabinetID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}
