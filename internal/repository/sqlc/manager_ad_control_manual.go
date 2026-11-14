package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

func sameAdResources(a, b adguard.Request) bool {
	return a.AccountKey == b.AccountKey && a.Marketplace == b.Marketplace && slices.Equal(a.ResourceKeys(), b.ResourceKeys())
}
func managedAdScope(previous, current domain.ManagerAdOperation) bool {
	return previous.ID != current.ID && previous.WorkspaceID == current.WorkspaceID && previous.ExternalWorkspaceID == current.ExternalWorkspaceID &&
		previous.IntegrationID == current.IntegrationID && previous.CabinetID == current.CabinetID && previous.CampaignID == current.CampaignID &&
		previous.ExternalID == current.ExternalID && previous.Marketplace == current.Marketplace && sameAdResources(previous.Request, current.Request) &&
		previous.Status == "succeeded" && (previous.Action == "pause" || previous.Action == "resume" || previous.Action == "launch" && previous.Result != nil && previous.Result.LaunchVerified || previous.Action == "set_bid" && previous.Result != nil && previous.Result.BidVerified || previous.Action == "set_budget" && previous.Result != nil && previous.Result.BudgetVerified) && previous.Result != nil && previous.Result.FieldsVerified &&
		previous.Result.VendorState != nil && *previous.Result.VendorState == previous.TargetStatus && previous.TargetStatus == current.Snapshot.Status
}

// CurrentManagerAdControl looks only at real, currently held commands. Legacy,
// global or unresolved commands are never treated as transferable controls.
func (q *Queries) CurrentManagerAdControl(ctx context.Context, r adguard.Request) (*domain.ManagerAdOperation, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	rows, err := q.db.Query(ctx, `SELECT DISTINCT command_id FROM ad_native_resource_holds WHERE resource_key=ANY($1::text[]) OR (account_key=$2 AND global_scope)`, r.ResourceKeys(), r.AccountKey)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) != 1 {
		return nil, ErrAdResourceHeld
	}
	o, err := scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1`, ids[0]))
	if err != nil {
		return nil, ErrAdResourceHeld
	}
	c, err := q.ReadNativeAdCommand(ctx, o.ID)
	if err != nil || c.Origin != "manager" || c.Status != "observed" || !sameAdResources(c.Request, r) || c.Request.Fingerprint != o.Request.Fingerprint ||
		c.ApprovalFingerprint != o.Fingerprint || !c.WorkspaceID.Valid || c.WorkspaceID.Bytes != o.WorkspaceID || !c.ActorID.Valid || c.ActorID.Bytes != o.ActorID ||
		o.Status != "succeeded" || o.Result == nil || !o.Result.FieldsVerified || !o.Result.ControlHoldActive || o.Result.ControlTransferredTo != nil {
		return nil, ErrAdResourceHeld
	}
	var count int
	if err = q.db.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1 AND resource_key=ANY($2::text[])`, o.ID, r.ResourceKeys()).Scan(&count); err != nil {
		return nil, err
	}
	if count != len(r.ResourceKeys()) {
		return nil, ErrAdResourceHeld
	}
	return &o, nil
}

// ValidateManagerAdControl also recognizes a reservation already transferred
// to this exact proposal; it is used by the fresh callback under that hold.
func (q *Queries) ValidateManagerAdControl(ctx context.Context, o domain.ManagerAdOperation) error {
	var count int
	if err := q.db.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds h JOIN ad_native_commands c ON c.id=h.command_id WHERE h.command_id=$1 AND h.resource_key=ANY($2::text[]) AND c.origin='manager' AND c.status='approved' AND c.started_at IS NULL AND c.request_fingerprint=$3 AND c.approval_fingerprint=$4`, o.ID, o.Request.ResourceKeys(), o.Request.Fingerprint, o.Fingerprint).Scan(&count); err != nil {
		return err
	}
	if count == len(o.Request.ResourceKeys()) {
		return nil
	}
	previous, err := q.CurrentManagerAdControl(ctx, o.Request)
	if err != nil {
		return err
	}
	if o.PreviousOperationID == nil {
		if previous != nil {
			return ErrAdResourceHeld
		}
		return nil
	}
	if previous == nil || previous.ID != *o.PreviousOperationID || previous.Fingerprint != o.PreviousFingerprint || !managedAdScope(*previous, o) {
		return ErrManagerAdOperationChanged
	}
	return nil
}

func (q *Queries) lockPreviousManagerAdControl(ctx context.Context, o domain.ManagerAdOperation) (domain.ManagerAdOperation, error) {
	if o.PreviousOperationID == nil {
		return domain.ManagerAdOperation{}, ErrManagerAdOperationChanged
	}
	previous, err := scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1 FOR UPDATE`, *o.PreviousOperationID))
	if err != nil {
		return previous, err
	}
	if previous.Fingerprint != o.PreviousFingerprint || !managedAdScope(previous, o) || !previous.Result.ControlHoldActive || previous.Result.ControlTransferredTo != nil {
		return previous, ErrManagerAdOperationChanged
	}
	active, err := q.CurrentManagerAdControl(ctx, o.Request)
	if err != nil {
		return previous, err
	}
	if active == nil || active.ID != previous.ID {
		return previous, ErrAdResourceHeld
	}
	return previous, nil
}

func (q *Queries) markManagerAdControlTransferred(ctx context.Context, previous domain.ManagerAdOperation, to uuid.UUID) error {
	previous.Result.ControlHoldActive = false
	previous.Result.ControlTransferredTo = &to
	raw, err := json.Marshal(previous.Result)
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `UPDATE manager_ad_operations SET result=$2,updated_at=statement_timestamp() WHERE id=$1`, previous.ID, raw)
	return err
}

// ReserveManagerAdCommand transfers an observed control atomically. At no
// committed point is the campaign available to a legacy/autopilot writer.
func (q *Queries) ReserveManagerAdCommand(ctx context.Context, o domain.ManagerAdOperation) error {
	if o.PreviousOperationID == nil {
		return q.ReserveApprovedAdCommand(ctx, o.ID, o.Request, pgtype.UUID{Bytes: o.WorkspaceID, Valid: true}, pgtype.UUID{Bytes: o.ActorID, Valid: true}, o.Fingerprint, o.CreatedAt, o.ExpiresAt)
	}
	if (o.Action != "pause" && o.Action != "resume" && o.Action != "set_bid" && o.Action != "set_budget" && o.Action != "launch") || o.Request.Validate() != nil {
		return ErrManagerAdOperationChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = acquireAdAccount(ctx, tx, o.Request.AccountKey); err != nil {
		return err
	}
	qt := q.WithTx(tx)
	controlVersion, err := qt.adExecutionReservation(ctx, o.Request.AccountKey, o.Marketplace, true, o.CreatedAt)
	if err != nil {
		return err
	}
	var claimed bool
	err = tx.QueryRow(ctx, `SELECT status='executing' AND expires_at>statement_timestamp() AND fingerprint=$4 FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint).Scan(&claimed)
	if err != nil {
		return err
	}
	if !claimed {
		return ErrManagerAdOperationChanged
	}
	previous, err := qt.lockPreviousManagerAdControl(ctx, o)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(o.Request.Resources)
	_, err = tx.Exec(ctx, `INSERT INTO ad_native_commands(id,marketplace,account_key,request_fingerprint,method,endpoint,resources,origin,status,workspace_id,actor_id,approval_fingerprint,approval_expires_at,execution_control_version) VALUES($1,$2,$3,$4,$5,$6,$7,'manager','approved',$8,$9,$10,$11,$12)`, o.ID, o.Marketplace, o.Request.AccountKey, o.Request.Fingerprint, o.Request.Method, o.Request.URL, raw, o.WorkspaceID, o.ActorID, o.Fingerprint, o.ExpiresAt, controlVersion)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE ad_native_resource_holds SET command_id=$2 WHERE command_id=$1 AND resource_key=ANY($3::text[])`, previous.ID, o.ID, o.Request.ResourceKeys())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(o.Request.ResourceKeys())) {
		return ErrAdResourceHeld
	}
	if err = qt.markManagerAdControlTransferred(ctx, previous, o.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// restoreManagerAdControl runs inside the same transaction as a definite
// rejection or never-sent expiry. An unknown outcome never restores/releases
// control; a started command may restore it only after a definite rejection.
func (q *Queries) restoreManagerAdControl(ctx context.Context, id uuid.UUID) error {
	o, err := scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && o.PreviousOperationID == nil) {
		_, err = q.db.Exec(ctx, `DELETE FROM ad_native_resource_holds WHERE command_id=$1`, id)
		return err
	}
	if err != nil {
		return err
	}
	// A failed reservation never acquired the old control and cannot restore it.
	var reserved bool
	if err = q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ad_native_commands WHERE id=$1)`, id).Scan(&reserved); err != nil {
		return err
	}
	if !reserved {
		return nil
	}
	previous, err := scanManagerAd(q.db.QueryRow(ctx, `SELECT `+managerAdColumns+` FROM manager_ad_operations WHERE id=$1 FOR UPDATE`, *o.PreviousOperationID))
	if err != nil {
		return err
	}
	if previous.Fingerprint != o.PreviousFingerprint || !managedAdScope(previous, o) {
		return ErrManagerAdOperationChanged
	}
	if previous.Result.ControlHoldActive && previous.Result.ControlTransferredTo == nil {
		// Reservation failed before any transfer; the previous control remains.
		var owns int
		if err = q.db.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1 AND resource_key=ANY($2::text[])`, previous.ID, o.Request.ResourceKeys()).Scan(&owns); err != nil {
			return err
		}
		if owns == len(o.Request.ResourceKeys()) {
			return nil
		}
		return ErrAdResourceHeld
	}
	if previous.Result.ControlTransferredTo == nil || *previous.Result.ControlTransferredTo != id {
		return ErrManagerAdOperationChanged
	}
	tag, err := q.db.Exec(ctx, `UPDATE ad_native_resource_holds SET command_id=$2 WHERE command_id=$1 AND resource_key=ANY($3::text[])`, id, previous.ID, o.Request.ResourceKeys())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(o.Request.ResourceKeys())) {
		return ErrAdResourceHeld
	}
	previous.Result.ControlHoldActive = true
	previous.Result.ControlTransferredTo = nil
	raw, _ := json.Marshal(previous.Result)
	_, err = q.db.Exec(ctx, `UPDATE manager_ad_operations SET result=$2,updated_at=statement_timestamp() WHERE id=$1`, previous.ID, raw)
	return err
}

// HandoverManagerAdControl is a local decision, not a vendor mutation. It can
// release only an observed, exact, still-owned control after a fresh snapshot.
func (q *Queries) HandoverManagerAdControl(ctx context.Context, o domain.ManagerAdOperation, native domain.NativeAdCampaign, observed time.Time) error {
	if o.Action != "handover" || o.PreviousOperationID == nil || native.ExternalID != o.ExternalID || native.Status != o.TargetStatus {
		return ErrManagerAdOperationChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = acquireAdAccount(ctx, tx, o.Request.AccountKey); err != nil {
		return err
	}
	var ready bool
	err = tx.QueryRow(ctx, `SELECT status='executing' AND expires_at>statement_timestamp() AND fingerprint=$4 FROM manager_ad_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return ErrManagerAdOperationChanged
	}
	qt := q.WithTx(tx)
	if err = qt.adExecutionDispatch(ctx, o.Request.AccountKey, o.Marketplace, false, 0); err != nil {
		return err
	}
	previous, err := qt.lockPreviousManagerAdControl(ctx, o)
	if err != nil {
		return err
	}
	var cabinet uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM seller_cabinets WHERE id=$1 AND workspace_id=$2 AND external_integration_id=$3 AND marketplace=$4 AND status='active' AND deleted_at IS NULL FOR UPDATE`, o.CabinetID, o.WorkspaceID, o.IntegrationID, o.Marketplace).Scan(&cabinet)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM ad_native_resource_holds WHERE command_id=$1 AND resource_key=ANY($2::text[])`, previous.ID, o.Request.ResourceKeys())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(o.Request.ResourceKeys())) {
		return ErrAdResourceHeld
	}
	if err = qt.markManagerAdControlTransferred(ctx, previous, o.ID); err != nil {
		return err
	}
	raw, _ := json.Marshal(domain.ManagerAdResult{FieldsVerified: true, VendorState: &native.Status, ObservedAt: &observed, ControlReleased: true})
	_, err = tx.Exec(ctx, `UPDATE manager_ad_operations SET status='succeeded',message_code=NULL,result=$2,updated_at=statement_timestamp() WHERE id=$1`, o.ID, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
