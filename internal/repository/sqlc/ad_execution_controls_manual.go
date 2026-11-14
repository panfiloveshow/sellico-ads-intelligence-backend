package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

var ErrAdExecutionStopped = errors.New("advertising service writes are stopped or require a new personal approval")
var ErrAdExecutionControlChanged = errors.New("advertising execution control proposal changed or expired")

// These methods run under the existing native account transaction lock at a
// mutation boundary. A stop and a dispatch marker cannot pass each other.
func (q *Queries) AdExecutionControl(ctx context.Context, account, marketplace string) (domain.AdExecutionControlState, error) {
	var s domain.AdExecutionControlState
	var storedMarket string
	err := q.db.QueryRow(ctx, `SELECT marketplace,mode,version,changed_at FROM ad_account_execution_controls WHERE account_key=$1`, account).Scan(&storedMarket, &s.Mode, &s.Version, &s.ChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdExecutionControlState{Mode: "legacy", Version: 0}, nil
	}
	if err != nil {
		return s, err
	}
	if storedMarket != marketplace {
		return s, ErrAdExecutionControlChanged
	}
	return s, nil
}
func (q *Queries) adExecutionReservation(ctx context.Context, account, marketplace string, manager bool, created time.Time) (int64, error) {
	s, err := q.AdExecutionControl(ctx, account, marketplace)
	if err != nil {
		return 0, err
	}
	if s.Mode == "stopped" || s.Mode == "manual_only" && (!manager || s.ChangedAt == nil || !created.After(*s.ChangedAt)) {
		return 0, ErrAdExecutionStopped
	}
	return s.Version, nil
}
func (q *Queries) adExecutionDispatch(ctx context.Context, account, marketplace string, manager bool, version int64) error {
	s, err := q.AdExecutionControl(ctx, account, marketplace)
	if err != nil {
		return err
	}
	if s.Mode == "stopped" || s.Mode == "manual_only" && !manager || manager && s.Version != version {
		return ErrAdExecutionStopped
	}
	return nil
}

// Private columns are serialized separately from the public DTO. No endpoint
// accepts this envelope, account key, credential digest or actor as input.
type storedAdExecutionControl struct {
	Operation        domain.AdExecutionControlOperation `json:"operation"`
	WorkspaceID      uuid.UUID                          `json:"workspace_id"`
	ActorID          uuid.UUID                          `json:"actor_id"`
	ExternalUserID   string                             `json:"external_user_id"`
	CabinetID        uuid.UUID                          `json:"cabinet_id"`
	AccountKey       string                             `json:"account_key"`
	CredentialDigest string                             `json:"credential_digest"`
}

func controlEnvelope(o domain.AdExecutionControlOperation) storedAdExecutionControl {
	o.ResultControl = nil
	o.CurrentControl = nil
	o.CurrentCheckedAt = nil
	o.ApprovalAvailable = false
	return storedAdExecutionControl{o, o.WorkspaceID, o.ActorID, o.ExternalUserID, o.CabinetID, o.AccountKey, o.CredentialDigest}
}
func (q *Queries) CreateAdExecutionControlOperation(ctx context.Context, o domain.AdExecutionControlOperation) error {
	raw, err := json.Marshal(controlEnvelope(o))
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO manager_ad_execution_control_operations(id,workspace_id,actor_id,immutable_payload,fingerprint,status,created_at,expires_at) VALUES($1,$2,$3,$4,$5,'awaiting_confirmation',$6,$7)`, o.ID, o.WorkspaceID, o.ActorID, raw, o.Fingerprint, o.CreatedAt, o.ExpiresAt)
	return err
}
func scanAdExecutionControl(row rowScanner) (domain.AdExecutionControlOperation, error) {
	var o domain.AdExecutionControlOperation
	var stored storedAdExecutionControl
	var raw, result []byte
	var id, workspace, actor uuid.UUID
	var fingerprint, status string
	var created, expires time.Time
	err := row.Scan(&id, &workspace, &actor, &raw, &fingerprint, &status, &created, &expires, &result)
	if err != nil {
		return o, err
	}
	if err = json.Unmarshal(raw, &stored); err != nil {
		return o, err
	}
	o = stored.Operation
	if o.ID != id || stored.WorkspaceID != workspace || stored.ActorID != actor || !o.CreatedAt.Truncate(time.Microsecond).Equal(created) || !o.ExpiresAt.Truncate(time.Microsecond).Equal(expires) || o.Fingerprint != fingerprint {
		return o, ErrAdExecutionControlChanged
	}
	o.WorkspaceID = workspace
	o.ActorID = actor
	o.ExternalUserID = stored.ExternalUserID
	o.CabinetID = stored.CabinetID
	o.AccountKey = stored.AccountKey
	o.CredentialDigest = stored.CredentialDigest
	o.Status = status
	if len(result) > 0 {
		if err = json.Unmarshal(result, &o.ResultControl); err != nil {
			return o, err
		}
	}
	return o, nil
}

const adExecutionOperationColumns = `id,workspace_id,actor_id,immutable_payload,fingerprint,status,created_at,expires_at,result_control`

func (q *Queries) GetAdExecutionControlOperation(ctx context.Context, id, workspace, actor uuid.UUID) (domain.AdExecutionControlOperation, error) {
	return scanAdExecutionControl(q.db.QueryRow(ctx, `SELECT `+adExecutionOperationColumns+` FROM manager_ad_execution_control_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3`, id, workspace, actor))
}
func (q *Queries) ExpireAdExecutionControlOperation(ctx context.Context, o domain.AdExecutionControlOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_execution_control_operations SET status='expired' WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='awaiting_confirmation' AND expires_at<=statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID)
	return err
}
func (q *Queries) CancelAdExecutionControlOperation(ctx context.Context, o domain.AdExecutionControlOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_ad_execution_control_operations SET status='cancelled' WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND fingerprint=$4 AND status='awaiting_confirmation'`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint)
	return err
}
func (q *Queries) ApplyAdExecutionControl(ctx context.Context, o domain.AdExecutionControlOperation, validate func(context.Context) error) error {
	if validate == nil || (o.TargetMode != "stopped" && o.TargetMode != "manual_only") {
		return ErrAdExecutionControlChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = acquireAdAccount(ctx, tx, o.AccountKey); err != nil {
		return err
	}
	qt := q.WithTx(tx)
	current, err := scanAdExecutionControl(tx.QueryRow(ctx, `SELECT `+adExecutionOperationColumns+` FROM manager_ad_execution_control_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID))
	if err != nil {
		return err
	}
	if current.Status == "succeeded" {
		return nil
	}
	before, _ := json.Marshal(controlEnvelope(o))
	saved, _ := json.Marshal(controlEnvelope(current))
	if string(before) != string(saved) || current.Status != "awaiting_confirmation" {
		return ErrAdExecutionControlChanged
	}
	state, err := qt.AdExecutionControl(ctx, o.AccountKey, o.Marketplace)
	if err != nil {
		return err
	}
	if state.Version != o.Before.Version || state.Mode != o.Before.Mode {
		return ErrAdExecutionControlChanged
	}
	if err = validate(ctx); err != nil {
		return err
	}
	var result domain.AdExecutionControlState
	err = tx.QueryRow(ctx, `INSERT INTO ad_account_execution_controls(account_key,marketplace,mode,version,changed_at,changed_by_operation) VALUES($1,$2,$3,1,statement_timestamp(),$4)
 ON CONFLICT(account_key) DO UPDATE SET mode=EXCLUDED.mode,version=ad_account_execution_controls.version+1,changed_at=EXCLUDED.changed_at,changed_by_operation=EXCLUDED.changed_by_operation
 RETURNING mode,version,changed_at`, o.AccountKey, o.Marketplace, o.TargetMode, o.ID).Scan(&result.Mode, &result.Version, &result.ChangedAt)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(result)
	tag, err := tx.Exec(ctx, `UPDATE manager_ad_execution_control_operations SET status='succeeded',result_control=$4 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='awaiting_confirmation' AND expires_at>statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAdExecutionControlChanged
	}
	return tx.Commit(ctx)
}
