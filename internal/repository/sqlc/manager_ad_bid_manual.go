package sqlcgen

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// ObserveManagerAdProductBid records exact native evidence. A missing local
// product mirror remains missing: it is not fabricated to make a journal green.
func (q *Queries) ObserveManagerAdProductBid(ctx context.Context, o domain.ManagerAdOperation, native domain.NativeAdBidContext, observed time.Time) error {
	if o.Action != "set_bid" || o.BidPlan == nil || native.Campaign.ExternalID != o.ExternalID || native.Campaign.Status != o.TargetStatus || native.Bid.ProductID != o.BidPlan.Before.ProductID || native.Bid.Placement != o.BidPlan.Before.Placement || native.Bid.CurrentBidRub != o.BidPlan.TargetBidRub {
		return ErrManagerAdOperationChanged
	}
	product, err := strconv.ParseInt(native.Bid.ProductID, 10, 64)
	if err != nil || product <= 0 {
		return ErrManagerAdOperationChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND fingerprint=$4 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint).Scan(&status)
	if err != nil {
		return err
	}
	if status == "succeeded" {
		return nil
	}
	if status != "executing" && status != "unknown" && status != "verifying" {
		return ErrManagerAdOperationChanged
	}
	var cabinet uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM seller_cabinets WHERE id=$1 AND workspace_id=$2 AND external_integration_id=$3 AND marketplace=$4 AND status='active' AND deleted_at IS NULL FOR UPDATE`, o.CabinetID, o.WorkspaceID, o.IntegrationID, o.Marketplace).Scan(&cabinet)
	if err != nil {
		return err
	}
	c, err := scanNativeAd(tx.QueryRow(ctx, `SELECT `+nativeAdColumns+` FROM ad_native_commands WHERE id=$1 FOR UPDATE`, o.ID))
	if err != nil {
		return err
	}
	if c.Origin != "manager" || c.Request.Fingerprint != o.Request.Fingerprint || c.Request.AccountKey != o.Request.AccountKey || c.WorkspaceID.Bytes != o.WorkspaceID || c.ActorID.Bytes != o.ActorID || c.ApprovalFingerprint != o.Fingerprint || (c.Status != "acknowledged" && c.Status != "unknown" && c.Status != "observed") {
		return ErrManagerAdOperationChanged
	}
	var holds int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1 AND resource_key=ANY($2::text[])`, o.ID, o.Request.ResourceKeys()).Scan(&holds)
	if err != nil {
		return err
	}
	if holds != len(o.Request.ResourceKeys()) {
		return ErrManagerAdOperationChanged
	}
	mirror := false
	if o.Marketplace == "wb" {
		amount, ok := new(big.Rat).SetString(native.Bid.CurrentBidRub)
		if !ok {
			return ErrManagerAdOperationChanged
		}
		amount.Mul(amount, big.NewRat(100, 1))
		if !amount.IsInt() || !amount.Num().IsInt64() {
			return ErrManagerAdOperationChanged
		}
		column := "bid_search"
		if native.Bid.Placement == "recommendations" {
			column = "bid_recommendations"
		} else if native.Bid.Placement != "search" {
			return ErrManagerAdOperationChanged
		}
		tag, err := tx.Exec(ctx, `UPDATE campaign_products SET `+column+`=$6 WHERE campaign_id=$1 AND workspace_id=$2 AND seller_cabinet_id=$3 AND wb_campaign_id::text=$4 AND wb_product_id=$5`, o.CampaignID, o.WorkspaceID, o.CabinetID, o.ExternalID, product, amount.Num().Int64())
		if err != nil {
			return err
		}
		mirror = tag.RowsAffected() == 1
	} else if o.Marketplace == "ozon" {
		tag, err := tx.Exec(ctx, `UPDATE ozon_campaign_products SET bid_rub=$3::numeric WHERE campaign_id=$1 AND sku=$2`, o.CampaignID, product, native.Bid.CurrentBidRub)
		if err != nil {
			return err
		}
		mirror = tag.RowsAffected() == 1
	} else {
		return ErrManagerAdOperationChanged
	}
	ack := c.HTTPStatus.Valid && c.HTTPStatus.Int16 >= 200 && c.HTTPStatus.Int16 < 300
	raw, _ := json.Marshal(domain.ManagerAdResult{FieldsVerified: true, SubmissionAcknowledged: ack, VendorState: &native.Campaign.Status, ControlHoldActive: true, ObservedAt: &observed, BidVerified: true, BidRub: &native.Bid.CurrentBidRub, ProductID: &native.Bid.ProductID, Placement: &native.Bid.Placement, MirrorUpdated: &mirror})
	_, err = tx.Exec(ctx, `UPDATE ad_native_commands SET status='observed' WHERE id=$1`, o.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE manager_ad_operations SET status='succeeded',message_code=NULL,result=$2,updated_at=statement_timestamp() WHERE id=$1`, o.ID, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
