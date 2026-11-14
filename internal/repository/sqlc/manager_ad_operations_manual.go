package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

var ErrManagerAdOperationChanged = errors.New("advertising proposal changed, expired or already claimed")

const managerAdColumns = `id,workspace_id,actor_id,external_user_id,external_workspace_id,integration_id,integration_name,seller_cabinet_id,campaign_id,external_id,marketplace,action,before_snapshot,target_status,source,native_request,credential_digest,fingerprint,status,message_code,result,created_at,expires_at,previous_operation_id,previous_fingerprint,bid_plan,budget_plan,launch_plan`

func scanManagerAd(row rowScanner) (domain.ManagerAdOperation, error) {
	var o domain.ManagerAdOperation
	var snapshot, request, result, bidPlan, budgetPlan, launchPlan []byte
	var previousFingerprint *string
	err := row.Scan(&o.ID, &o.WorkspaceID, &o.ActorID, &o.ExternalUserID, &o.ExternalWorkspaceID, &o.IntegrationID, &o.IntegrationName, &o.CabinetID, &o.CampaignID, &o.ExternalID, &o.Marketplace, &o.Action, &snapshot, &o.TargetStatus, &o.Source, &request, &o.CredentialDigest, &o.Fingerprint, &o.Status, &o.MessageCode, &result, &o.CreatedAt, &o.ExpiresAt, &o.PreviousOperationID, &previousFingerprint, &bidPlan, &budgetPlan, &launchPlan)
	if err != nil {
		return o, err
	}
	if err = json.Unmarshal(snapshot, &o.Snapshot); err != nil {
		return o, err
	}
	if err = json.Unmarshal(request, &o.Request); err != nil {
		return o, err
	}
	if len(result) > 0 {
		if err = json.Unmarshal(result, &o.Result); err != nil {
			return o, err
		}
	}
	if previousFingerprint != nil {
		o.PreviousFingerprint = *previousFingerprint
	}
	if len(bidPlan) > 0 {
		if err = json.Unmarshal(bidPlan, &o.BidPlan); err != nil {
			return o, err
		}
	}
	o.OperationType = "ad_change"
	if len(budgetPlan) > 0 {
		if err = json.Unmarshal(budgetPlan, &o.BudgetPlan); err != nil {
			return o, err
		}
	}
	if len(launchPlan) > 0 {
		if err = json.Unmarshal(launchPlan, &o.LaunchPlan); err != nil {
			return o, err
		}
	}
	o.CreatedAt = o.CreatedAt.UTC()
	o.ExpiresAt = o.ExpiresAt.UTC()
	return o, nil
}
func (q *Queries) CreateManagerAdOperation(ctx context.Context, o domain.ManagerAdOperation) error {
	snapshot, err := json.Marshal(o.Snapshot)
	if err != nil {
		return err
	}
	request, err := json.Marshal(o.Request)
	if err != nil {
		return err
	}
	var bidPlan []byte
	if o.BidPlan != nil {
		bidPlan, err = json.Marshal(o.BidPlan)
		if err != nil {
			return err
		}
	}
	var budgetPlan []byte
	if o.BudgetPlan != nil {
		budgetPlan, err = json.Marshal(o.BudgetPlan)
		if err != nil {
			return err
		}
	}
	var launchPlan []byte
	if o.LaunchPlan != nil {
		launchPlan, err = json.Marshal(o.LaunchPlan)
		if err != nil {
			return err
		}
	}
	_, err = q.db.Exec(ctx, `INSERT INTO manager_ad_operations(id,workspace_id,actor_id,external_user_id,external_workspace_id,integration_id,integration_name,seller_cabinet_id,campaign_id,external_id,marketplace,action,before_snapshot,target_status,source,native_request,credential_digest,fingerprint,status,created_at,expires_at,previous_operation_id,previous_fingerprint,bid_plan,budget_plan,launch_plan) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'awaiting_confirmation',$19,$20,$21,NULLIF($22,''),$23,$24,$25)`, o.ID, o.WorkspaceID, o.ActorID, o.ExternalUserID, o.ExternalWorkspaceID, o.IntegrationID, o.IntegrationName, o.CabinetID, o.CampaignID, o.ExternalID, o.Marketplace, o.Action, snapshot, o.TargetStatus, o.Source, request, o.CredentialDigest, o.Fingerprint, o.CreatedAt, o.ExpiresAt, o.PreviousOperationID, o.PreviousFingerprint, bidPlan, budgetPlan, launchPlan)
	return err
}
func (q *Queries) GetManagerAdOperation(ctx context.Context, id, workspace, actor uuid.UUID) (domain.ManagerAdOperation, error) {
	return scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3`, id, workspace, actor))
}
func (q *Queries) ClaimManagerAdOperation(ctx context.Context, o domain.ManagerAdOperation) error {
	tag, err := q.db.Exec(ctx, `UPDATE manager_ad_operations SET status='executing',updated_at=statement_timestamp() WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND fingerprint=$4 AND status='awaiting_confirmation' AND expires_at>statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrManagerAdOperationChanged
	}
	return nil
}
func (q *Queries) CancelManagerAdOperation(ctx context.Context, o domain.ManagerAdOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_operations SET status='cancelled',updated_at=statement_timestamp() WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND fingerprint=$4 AND status='awaiting_confirmation'`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint)
	return err
}
func (q *Queries) SetManagerAdOperationPending(ctx context.Context, o domain.ManagerAdOperation, status, code string) error {
	if status != "unknown" && status != "verifying" && status != "failed" {
		return ErrManagerAdOperationChanged
	}
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_operations SET status=$4,message_code=NULLIF($5,''),updated_at=statement_timestamp() WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status IN ('executing','verifying','unknown')`, o.ID, o.WorkspaceID, o.ActorID, status, code)
	return err
}

// A known pre-send failure may close only an unstarted reservation. A started
// transport, even if its caller lost the commit response, retains its hold.
func (q *Queries) RejectManagerAdBeforeSend(ctx context.Context, o domain.ManagerAdOperation, code string) error {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID).Scan(&status); err != nil {
		return err
	}
	if status != "executing" {
		return nil
	}
	var nativeStatus string
	var started *time.Time
	err = tx.QueryRow(ctx, `SELECT status,started_at FROM ad_native_commands WHERE id=$1 FOR UPDATE`, o.ID).Scan(&nativeStatus, &started)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && (nativeStatus != "approved" || started != nil) {
		return ErrManagerAdOperationChanged
	}
	_, err = tx.Exec(ctx, `UPDATE ad_native_commands SET status='rejected',finished_at=statement_timestamp() WHERE id=$1 AND status='approved' AND started_at IS NULL`, o.ID)
	if err != nil {
		return err
	}
	err = q.WithTx(tx).restoreManagerAdControl(ctx, o.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE manager_ad_operations SET status='failed',message_code=$2,updated_at=statement_timestamp() WHERE id=$1`, o.ID, code)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (q *Queries) ExpireManagerAdOperation(ctx context.Context, o domain.ManagerAdOperation) error {
	if err := q.ExpireUnsentApprovedAdCommand(ctx, o.ID); err != nil {
		return err
	}
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_operations SET status='expired',message_code='expired',updated_at=statement_timestamp() WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND expires_at<=statement_timestamp() AND (status='awaiting_confirmation' OR (status='executing' AND NOT EXISTS(SELECT 1 FROM ad_native_commands c WHERE c.id=$1 AND c.started_at IS NOT NULL)))`, o.ID, o.WorkspaceID, o.ActorID)
	return err
}

// ObserveManagerAdState records the desired state, not causal acceptance of a
// lost HTTP response. Keep the campaign hold: legacy/autopilot writers cannot
// undo this personal pause. Releasing that intent requires a new personal decision.
func (q *Queries) ObserveManagerAdState(ctx context.Context, o domain.ManagerAdOperation, native domain.NativeAdCampaign, observed time.Time) error {
	return q.observeManagerAdState(ctx, o, native, observed, nil)
}
func (q *Queries) ObserveManagerAdLaunch(ctx context.Context, o domain.ManagerAdOperation, native domain.NativeAdLaunchContext, observed time.Time) error {
	if o.Action != "launch" || o.LaunchPlan == nil || !domain.SameAdLaunchSettings(o.LaunchPlan.Settings, native.Settings) {
		return ErrManagerAdOperationChanged
	}
	return q.observeManagerAdState(ctx, o, native.Campaign, observed, &native.Settings)
}
func (q *Queries) observeManagerAdState(ctx context.Context, o domain.ManagerAdOperation, native domain.NativeAdCampaign, observed time.Time, launch *domain.NativeAdLaunchSettings) error {
	if (o.Action != "pause" && o.Action != "resume" && (o.Action != "launch" || launch == nil)) || native.ExternalID != o.ExternalID || native.Status != o.TargetStatus {
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
	if c.Origin != "manager" || c.Request.Fingerprint != o.Request.Fingerprint || c.Request.AccountKey != o.Request.AccountKey || c.ApprovalFingerprint != o.Fingerprint || !c.WorkspaceID.Valid || !c.ActorID.Valid || c.WorkspaceID.Bytes != o.WorkspaceID || c.ActorID.Bytes != o.ActorID || (c.Status != "acknowledged" && c.Status != "unknown" && c.Status != "observed") {
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
	ack := c.HTTPStatus.Valid && c.HTTPStatus.Int16 >= 200 && c.HTTPStatus.Int16 < 300
	result, _ := json.Marshal(domain.ManagerAdResult{FieldsVerified: true, SubmissionAcknowledged: ack, VendorState: &native.Status, ControlHoldActive: true, ObservedAt: &observed, LaunchVerified: launch != nil, LaunchSettings: launch})
	var updated pgconn.CommandTag
	if o.Marketplace == "wb" {
		state := "paused"
		if o.Action == "resume" || o.Action == "launch" {
			state = "active"
		}
		updated, err = tx.Exec(ctx, `UPDATE campaigns SET status=$5,updated_at=statement_timestamp() WHERE id=$1 AND workspace_id=$2 AND seller_cabinet_id=$3 AND wb_campaign_id::text=$4`, o.CampaignID, o.WorkspaceID, o.CabinetID, o.ExternalID, state)
	} else {
		updated, err = tx.Exec(ctx, `UPDATE ozon_campaigns SET state=$3,updated_at=statement_timestamp() WHERE id=$1 AND seller_cabinet_id=$2 AND ozon_campaign_id::text=$4`, o.CampaignID, o.CabinetID, o.TargetStatus, o.ExternalID)
	}
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrManagerAdOperationChanged
	}
	_, err = tx.Exec(ctx, `UPDATE ad_native_commands SET status='observed' WHERE id=$1`, o.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE manager_ad_operations SET status='succeeded',message_code=NULL,result=$2,updated_at=statement_timestamp() WHERE id=$1`, o.ID, result)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
