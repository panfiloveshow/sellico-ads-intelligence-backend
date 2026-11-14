package sqlcgen

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// This private history lookup never broadens scope and is not an actor's public
// receipt endpoint. The service verifies every fingerprint link it traverses.
func (q *Queries) GetManagerAdControlPredecessor(ctx context.Context, current domain.ManagerAdOperation, id uuid.UUID) (domain.ManagerAdOperation, error) {
	o, err := scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 AND external_workspace_id=$3 AND integration_id=$4 AND seller_cabinet_id=$5 AND campaign_id=$6 AND external_id=$7 AND marketplace=$8 AND status='succeeded'`, id, current.WorkspaceID, current.ExternalWorkspaceID, current.IntegrationID, current.CabinetID, current.CampaignID, current.ExternalID, current.Marketplace))
	if err != nil {
		return o, err
	}
	if !sameAdResources(o.Request, current.Request) {
		return o, ErrManagerAdOperationChanged
	}
	return o, nil
}

func (q *Queries) ObserveManagerAdBudget(ctx context.Context, o domain.ManagerAdOperation, native domain.NativeAdBudgetContext, observed time.Time) error {
	if o.Action != "set_budget" || o.BudgetPlan == nil || native.Campaign.ExternalID != o.ExternalID || native.Campaign.Status != o.TargetStatus || native.Budget.Currency != "RUB" || native.Budget.Period != o.BudgetPlan.Before.Period || native.Budget.Enabled != o.BudgetPlan.Target.Enabled {
		return ErrManagerAdOperationChanged
	}
	if o.BudgetPlan.Target.Enabled && (native.Budget.AmountRub == nil || o.BudgetPlan.Target.AmountRub == nil || *native.Budget.AmountRub != *o.BudgetPlan.Target.AmountRub) {
		return ErrManagerAdOperationChanged
	}
	if o.BudgetPlan.Target.CarryOverEnabled != nil && (native.Budget.CarryOverEnabled == nil || *native.Budget.CarryOverEnabled != *o.BudgetPlan.Target.CarryOverEnabled) {
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
	if c.Origin != "manager" || c.Request.Fingerprint != o.Request.Fingerprint || c.Request.AccountKey != o.Request.AccountKey || !c.WorkspaceID.Valid || c.WorkspaceID.Bytes != o.WorkspaceID || !c.ActorID.Valid || c.ActorID.Bytes != o.ActorID || c.ApprovalFingerprint != o.Fingerprint || c.Status != "acknowledged" && c.Status != "unknown" && c.Status != "observed" {
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
	// Legacy budget columns cannot represent every exact amount and disabled/
	// carry-over state. Keep them untouched; the exact observation is the receipt.
	mirror := false
	ack := c.HTTPStatus.Valid && c.HTTPStatus.Int16 >= 200 && c.HTTPStatus.Int16 < 300
	raw, err := json.Marshal(domain.ManagerAdResult{FieldsVerified: true, SubmissionAcknowledged: ack, VendorState: &native.Campaign.Status, ControlHoldActive: true, ObservedAt: &observed, BudgetVerified: true, Budget: &native.Budget, MirrorUpdated: &mirror})
	if err != nil {
		return err
	}
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
