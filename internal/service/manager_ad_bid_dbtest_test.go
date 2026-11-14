package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestManagerAdProductBidExactPersonalChange(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			placement := "search"
			target := "12.35"
			if market == "ozon" {
				placement = "campaign_product"
				target = "12.350001"
			}
			op, err := e.svc.PrepareBid(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "202", placement, target, domain.ManagerAdBidConstraints{MaxBidRub: "20", MaxChangePercent: "100"})
			require.NoError(t, err)
			require.Equal(t, "set_bid", op.Action)
			require.Equal(t, target, op.BidPlan.TargetBidRub)
			require.Equal(t, int32(0), e.api.writes.Load())
			_, err = e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token", op.Fingerprint, map[string]bool{"campaign_checked": true, "pause_impact_checked": true})
			require.Error(t, err)
			current, err := e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token", op.Fingerprint, map[string]bool{"bid_target_checked": true, "bid_spend_checked": true, "vendor_limits_checked": true})
			require.NoError(t, err)
			require.Equal(t, "succeeded", current.Status)
			require.True(t, current.Result.FieldsVerified)
			require.True(t, current.Result.BidVerified)
			require.Equal(t, target, *current.Result.BidRub)
			require.Equal(t, int32(1), e.api.writes.Load())
			e.assertLegacyBlocked(t)
		})
	}
}

func (e *pauseEnv) prepareBid(t *testing.T, value string) *domain.ManagerAdOperation {
	t.Helper()
	placement := "search"
	if e.api.market == "ozon" {
		placement = "campaign_product"
	}
	o, err := e.svc.PrepareBid(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "202", placement, value, domain.ManagerAdBidConstraints{MaxBidRub: "20", MaxChangePercent: "100"})
	require.NoError(t, err)
	return o
}
func (e *pauseEnv) approveBid(o *domain.ManagerAdOperation) (*domain.ManagerAdOperation, error) {
	return e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, map[string]bool{"bid_target_checked": true, "bid_spend_checked": true, "vendor_limits_checked": true})
}
func TestManagerAdProductBidDefiniteUnknownAndLostResults(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		for _, mode := range []string{"reject", "unknown", "lost", "pending"} {
			t.Run(market+"/"+mode, func(t *testing.T) {
				e := newPauseEnv(t, market, mode)
				op := e.prepareBid(t, "12.35")
				current, err := e.approveBid(op)
				require.NoError(t, err)
				require.Equal(t, int32(1), e.api.writes.Load())
				switch mode {
				case "reject":
					require.Equal(t, "failed", current.Status)
				case "unknown":
					require.Equal(t, "unknown", current.Status)
				case "pending":
					require.Equal(t, "verifying", current.Status)
				case "lost":
					require.Equal(t, "succeeded", current.Status)
					require.True(t, current.Result.BidVerified)
					require.False(t, current.Result.SubmissionAcknowledged)
				}
				_, err = e.approveBid(op)
				require.NoError(t, err)
				require.Equal(t, int32(1), e.api.writes.Load())
				if mode == "unknown" || mode == "pending" {
					e.assertLegacyBlocked(t)
					e.api.mu.Lock()
					if market == "wb" {
						e.api.bidWB = 1235
					} else {
						e.api.bidOzon = "12350000"
					}
					e.api.mu.Unlock()
					current, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token")
					require.NoError(t, err)
					require.Equal(t, "succeeded", current.Status)
					require.True(t, current.Result.BidVerified)
					require.Equal(t, "12.35", *current.Result.BidRub)
					require.Equal(t, int32(1), e.api.writes.Load())
				}
			})
		}
	}
}
func TestManagerAdProductBidRestoresPreviousDecisionOnRejection(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	first, err := e.approveBid(e.prepareBid(t, "12.35"))
	require.NoError(t, err)
	require.Equal(t, "succeeded", first.Status)
	next := e.prepareBid(t, "12.36")
	require.NotNil(t, next.PreviousOperationID)
	require.Equal(t, first.ID, *next.PreviousOperationID)
	e.api.mu.Lock()
	e.api.mode = "reject"
	e.api.mu.Unlock()
	failed, err := e.approveBid(next)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	original, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, first.ID, "personal-token")
	require.NoError(t, err)
	require.True(t, original.Result.ControlHoldActive)
	require.Nil(t, original.Result.ControlTransferredTo)
	e.assertLegacyBlocked(t)
}
func TestManagerAdProductBidFreshRightsBidAndMinimumUnderHold(t *testing.T) {
	for _, kind := range []string{"rights", "bid", "minimum"} {
		t.Run(kind, func(t *testing.T) {
			e := newPauseEnv(t, "ozon", "ok")
			op := e.prepareBid(t, "12.35")
			underHold := e.authority.calls.Load() + 2
			if kind == "rights" {
				e.authority.revokeAt = underHold
			} else {
				e.authority.hook = func(n int32) {
					if n == underHold {
						e.api.mu.Lock()
						defer e.api.mu.Unlock()
						if kind == "bid" {
							e.api.bidOzon = "12341000"
						} else {
							e.api.minimumOzon = "6000000"
						}
					}
				}
			}
			rejected, err := e.approveBid(op)
			require.NoError(t, err)
			require.Equal(t, "failed", rejected.Status)
			require.Equal(t, int32(0), e.api.writes.Load())
			command, err := e.q.ReadNativeAdCommand(context.Background(), op.ID)
			require.NoError(t, err)
			require.False(t, command.StartedAt.Valid)
		})
	}
}
func TestManagerAdProductBidExplicitConstraintsAndExactPrecision(t *testing.T) {
	for _, tc := range []struct{ value, max, pct string }{{"0.5", "20", "100"}, {"12.35", "12", "100"}, {"12.35", "20", "0"}, {"12.34", "20", "100"}, {"12.350001", "20", "100"}, {"12.35", "", "100"}, {"12.35", "20", ""}, {"12.35", "20", "1001"}} {
		t.Run(tc.value+"/"+tc.max+"/"+tc.pct, func(t *testing.T) {
			e := newPauseEnv(t, "wb", "ok")
			_, err := e.svc.PrepareBid(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "202", "search", tc.value, domain.ManagerAdBidConstraints{MaxBidRub: tc.max, MaxChangePercent: tc.pct})
			require.Error(t, err)
			require.Zero(t, e.api.writes.Load())
		})
	}
}

func TestManagerAdProductBidReadContextDoesNotNeedMoneyLimitsOrWrite(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			placement := "search"
			if market == "ozon" {
				placement = "campaign_product"
			}
			report, err := e.svc.context.ProductBidContext(context.Background(), managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign, "202", placement)
			require.NoError(t, err)
			require.Equal(t, "12.34", report.Bid.CurrentBidRub)
			require.True(t, report.Partial)
			require.False(t, report.ExecutionAllowed)
			require.Nil(t, report.Bid.MaximumBidRub)
			require.Zero(t, e.api.writes.Load())
		})
	}
}

func TestManagerAdProductBidPublicWireContracts(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			placement := "search"
			target := "12.35"
			if market == "ozon" {
				placement = "campaign_product"
				target = "12.350001"
			}
			contextView, err := e.svc.context.ProductBidContext(context.Background(), managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign, "202", placement)
			require.NoError(t, err)
			prepared := e.prepareBid(t, target)
			finished, err := e.approveBid(prepared)
			require.NoError(t, err)
			require.Equal(t, "succeeded", finished.Status)
			if dir := os.Getenv("SELYA_AD_BID_TEST_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				for name, value := range map[string]any{"context": contextView, "prepared": prepared, "finished": finished} {
					raw, err := json.Marshal(map[string]any{"data": value})
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(dir, market+"-"+name+".json"), raw, 0600))
				}
			}
		})
	}
}

func TestManagerAdProductBidSpecificWBPlacementPreservesPeer(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	op, err := e.svc.PrepareBid(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "202", "recommendations", "56.79", domain.ManagerAdBidConstraints{MaxBidRub: "60", MaxChangePercent: "100"})
	require.NoError(t, err)
	final, err := e.approveBid(op)
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.Equal(t, "56.79", *final.Result.BidRub)
	e.api.mu.Lock()
	search := e.api.bidWB
	recommendations := e.api.bidRecommendations
	e.api.mu.Unlock()
	require.Equal(t, int64(1234), search)
	require.Equal(t, int64(5679), recommendations)
	require.Equal(t, int32(1), e.api.writes.Load())
}
func TestManagerAdProductBidUnexpectedPeerChangeCannotBeSuccess(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	op := e.prepareBid(t, "12.35")
	observation := e.authority.calls.Load() + 3
	e.authority.hook = func(n int32) {
		if n == observation {
			e.api.mu.Lock()
			e.api.bidRecommendations++
			e.api.mu.Unlock()
		}
	}
	final, err := e.approveBid(op)
	require.NoError(t, err)
	require.Equal(t, "verifying", final.Status)
	require.Nil(t, final.Result)
	require.Equal(t, int32(1), e.api.writes.Load())
	e.assertLegacyBlocked(t)
}
func TestManagerAdProductBidConcurrentApprovalSendsOnce(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	op := e.prepareBid(t, "12.35")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.approveBid(op) }()
	}
	wg.Wait()
	final, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.Equal(t, int32(1), e.api.writes.Load())
}
func TestManagerAdProductBidHistoryCannotBeDowngraded(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000073_manager_ad_product_bids.down.sql")
	require.NoError(t, err)
	e := newPauseEnv(t, "ozon", "ok")
	_ = e.prepareBid(t, "12.35")
	_, err = e.pool.Exec(context.Background(), string(raw))
	require.Error(t, err)
}

func TestManagerAdProductBidOzonMirrorPreservesExactAmountAndOtherFields(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `INSERT INTO ozon_campaign_products(campaign_id,sku,bid_rub,target_cir,top_position,updated_at) VALUES($1,202,12.34,7.89,3,'2020-01-01T00:00:00Z'),($1,303,99.99,1.23,4,'2020-01-01T00:00:00Z')`, e.campaign)
	require.NoError(t, err)
	final, err := e.approveBid(e.prepareBid(t, "12.350001"))
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.True(t, *final.Result.MirrorUpdated)
	var bid string
	var selectedOthersUnchanged, peerUnchanged bool
	err = e.pool.QueryRow(ctx, `SELECT bid_rub::text,target_cir=7.89 AND top_position=3 AND updated_at='2020-01-01T00:00:00Z' FROM ozon_campaign_products WHERE campaign_id=$1 AND sku=202`, e.campaign).Scan(&bid, &selectedOthersUnchanged)
	require.NoError(t, err)
	require.Equal(t, "12.350001", bid)
	require.True(t, selectedOthersUnchanged)
	err = e.pool.QueryRow(ctx, `SELECT bid_rub=99.99 AND target_cir=1.23 AND top_position=4 AND updated_at='2020-01-01T00:00:00Z' FROM ozon_campaign_products WHERE campaign_id=$1 AND sku=303`, e.campaign).Scan(&peerUnchanged)
	require.NoError(t, err)
	require.True(t, peerUnchanged)
}

func TestManagerAdProductBidWBMirrorUpdatesOnlySelectedCampaignAndPlacement(t *testing.T) {
	for _, placement := range []string{"search", "recommendations"} {
		t.Run(placement, func(t *testing.T) {
			e := newPauseEnv(t, "wb", "ok")
			ctx := context.Background()
			var product, otherCampaign string
			err := e.pool.QueryRow(ctx, `INSERT INTO products(workspace_id,seller_cabinet_id,wb_product_id,title) VALUES($1,$2,202,'Test-only native product') RETURNING id::text`, e.workspace, e.cabinet).Scan(&product)
			require.NoError(t, err)
			err = e.pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,bid_type,payment_type) VALUES($1,$2,102,'Other test campaign','active','manual','cpm') RETURNING id::text`, e.workspace, e.cabinet).Scan(&otherCampaign)
			require.NoError(t, err)
			for _, pair := range []struct {
				campaign any
				external int
			}{{e.campaign, 101}, {otherCampaign, 102}} {
				_, err = e.pool.Exec(ctx, `INSERT INTO campaign_products(campaign_id,product_id,workspace_id,seller_cabinet_id,wb_campaign_id,wb_product_id,bid_search,bid_recommendations,updated_at) VALUES($1,$2,$3,$4,$5,202,1234,5678,'2020-01-01T00:00:00Z')`, pair.campaign, product, e.workspace, e.cabinet, pair.external)
				require.NoError(t, err)
			}
			target := "12.35"
			search, recommendations := int64(1235), int64(5678)
			if placement == "recommendations" {
				target, search, recommendations = "56.79", 1234, 5679
			}
			op, err := e.svc.PrepareBid(ctx, managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "202", placement, target, domain.ManagerAdBidConstraints{MaxBidRub: "60", MaxChangePercent: "100"})
			require.NoError(t, err)
			final, err := e.approveBid(op)
			require.NoError(t, err)
			require.Equal(t, "succeeded", final.Status)
			require.True(t, *final.Result.MirrorUpdated)
			var selected, other bool
			err = e.pool.QueryRow(ctx, `SELECT bid_search=$2 AND bid_recommendations=$3 AND updated_at='2020-01-01T00:00:00Z' FROM campaign_products WHERE campaign_id=$1`, e.campaign, search, recommendations).Scan(&selected)
			require.NoError(t, err)
			require.True(t, selected)
			err = e.pool.QueryRow(ctx, `SELECT bid_search=1234 AND bid_recommendations=5678 AND updated_at='2020-01-01T00:00:00Z' FROM campaign_products WHERE campaign_id=$1`, otherCampaign).Scan(&other)
			require.NoError(t, err)
			require.True(t, other)
		})
	}
}

func TestManagerAdProductBidDowngradeCannotRoundOrOverflowOzonBid(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000073_manager_ad_product_bids.down.sql")
	require.NoError(t, err)
	for _, value := range []string{"12.34", "12.350001", "10000000000"} {
		t.Run(value, func(t *testing.T) {
			e := newPauseEnv(t, "ozon", "ok")
			ctx := context.Background()
			_, err := e.pool.Exec(ctx, `INSERT INTO ozon_campaign_products(campaign_id,sku,bid_rub) VALUES($1,202,$2::numeric)`, e.campaign, value)
			require.NoError(t, err)
			_, err = e.pool.Exec(ctx, string(raw))
			if value == "12.34" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "lossy cents downgrade")
			}
			var unchanged bool
			err = e.pool.QueryRow(ctx, `SELECT bid_rub=$2::numeric FROM ozon_campaign_products WHERE campaign_id=$1 AND sku=202`, e.campaign, value).Scan(&unchanged)
			require.NoError(t, err)
			require.True(t, unchanged)
		})
	}
}
