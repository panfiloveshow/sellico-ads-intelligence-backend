package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

var ErrPriceExecutionStopped = errors.New("price service writes are stopped or require a new personal approval")
var ErrPriceExecutionControlChanged = errors.New("price execution control proposal changed or expired")

// These methods run under the existing native account transaction lock at a
// mutation boundary. A stop and a dispatch marker cannot pass each other.
func (q *Queries) PriceExecutionControl(ctx context.Context, account, marketplace string) (domain.PriceExecutionControlState, error) {
	if !priceExecutionAccountPattern.MatchString(account) || !strings.HasPrefix(account, marketplace+":") || (marketplace != "wb" && marketplace != "ozon") {
		return domain.PriceExecutionControlState{}, ErrPriceExecutionControlChanged
	}
	var s domain.PriceExecutionControlState
	var storedMarket string
	err := q.db.QueryRow(ctx, `SELECT marketplace,mode,version,changed_at FROM price_account_execution_controls WHERE account_key=$1`, account).Scan(&storedMarket, &s.Mode, &s.Version, &s.ChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PriceExecutionControlState{Mode: "legacy", Version: 0}, nil
	}
	if err != nil {
		return s, err
	}
	if storedMarket != marketplace {
		return s, ErrPriceExecutionControlChanged
	}
	return s, nil
}

// Private columns are serialized separately from the public DTO. No endpoint
// accepts this envelope, account key, credential digest or actor as input.
type storedPriceExecutionControl struct {
	Operation        domain.PriceExecutionControlOperation `json:"operation"`
	WorkspaceID      uuid.UUID                             `json:"workspace_id"`
	ActorID          uuid.UUID                             `json:"actor_id"`
	ExternalUserID   string                                `json:"external_user_id"`
	CabinetID        uuid.UUID                             `json:"cabinet_id"`
	AccountKey       string                                `json:"account_key"`
	CredentialDigest string                                `json:"credential_digest"`
}

func priceControlEnvelope(o domain.PriceExecutionControlOperation) storedPriceExecutionControl {
	o.ResultControl = nil
	o.CurrentControl = nil
	o.CurrentCheckedAt = nil
	o.ApprovalAvailable = false
	return storedPriceExecutionControl{o, o.WorkspaceID, o.ActorID, o.ExternalUserID, o.CabinetID, o.AccountKey, o.CredentialDigest}
}
func (q *Queries) CreatePriceExecutionControlOperation(ctx context.Context, o domain.PriceExecutionControlOperation) error {
	raw, err := json.Marshal(priceControlEnvelope(o))
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO manager_price_execution_control_operations(id,workspace_id,actor_id,immutable_payload,fingerprint,status,created_at,expires_at) VALUES($1,$2,$3,$4,$5,'awaiting_confirmation',$6,$7)`, o.ID, o.WorkspaceID, o.ActorID, raw, o.Fingerprint, o.CreatedAt, o.ExpiresAt)
	return err
}
func scanPriceExecutionControl(row rowScanner) (domain.PriceExecutionControlOperation, error) {
	var o domain.PriceExecutionControlOperation
	var stored storedPriceExecutionControl
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
		return o, ErrPriceExecutionControlChanged
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

const priceExecutionOperationColumns = `id,workspace_id,actor_id,immutable_payload,fingerprint,status,created_at,expires_at,result_control`

func (q *Queries) GetPriceExecutionControlOperation(ctx context.Context, id, workspace, actor uuid.UUID) (domain.PriceExecutionControlOperation, error) {
	return scanPriceExecutionControl(q.db.QueryRow(ctx, `SELECT `+priceExecutionOperationColumns+` FROM manager_price_execution_control_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3`, id, workspace, actor))
}
func (q *Queries) ExpirePriceExecutionControlOperation(ctx context.Context, o domain.PriceExecutionControlOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_price_execution_control_operations SET status='expired' WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='awaiting_confirmation' AND expires_at<=statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID)
	return err
}
func (q *Queries) CancelPriceExecutionControlOperation(ctx context.Context, o domain.PriceExecutionControlOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE manager_price_execution_control_operations SET status='cancelled' WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND fingerprint=$4 AND status='awaiting_confirmation'`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint)
	return err
}
func (q *Queries) ApplyPriceExecutionControl(ctx context.Context, o domain.PriceExecutionControlOperation, validate func(context.Context) error) error {
	if validate == nil || (o.TargetMode != "stopped" && o.TargetMode != "manual_only") {
		return ErrPriceExecutionControlChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = acquirePriceExecutionAccount(ctx, tx, o.AccountKey); err != nil {
		return err
	}
	qt := q.WithTx(tx)
	current, err := scanPriceExecutionControl(tx.QueryRow(ctx, `SELECT `+priceExecutionOperationColumns+` FROM manager_price_execution_control_operations WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID))
	if err != nil {
		return err
	}
	if current.Status == "succeeded" {
		return nil
	}
	before, _ := json.Marshal(priceControlEnvelope(o))
	saved, _ := json.Marshal(priceControlEnvelope(current))
	if string(before) != string(saved) || current.Status != "awaiting_confirmation" {
		return ErrPriceExecutionControlChanged
	}
	state, err := qt.PriceExecutionControl(ctx, o.AccountKey, o.Marketplace)
	if err != nil {
		return err
	}
	if state.Version != o.Before.Version || state.Mode != o.Before.Mode {
		return ErrPriceExecutionControlChanged
	}
	if err = validate(ctx); err != nil {
		return err
	}
	var result domain.PriceExecutionControlState
	err = tx.QueryRow(ctx, `INSERT INTO price_account_execution_controls(account_key,marketplace,mode,version,changed_at,changed_by_operation) VALUES($1,$2,$3,1,statement_timestamp(),$4)
 ON CONFLICT(account_key) DO UPDATE SET mode=EXCLUDED.mode,version=price_account_execution_controls.version+1,changed_at=EXCLUDED.changed_at,changed_by_operation=EXCLUDED.changed_by_operation
 RETURNING mode,version,changed_at`, o.AccountKey, o.Marketplace, o.TargetMode, o.ID).Scan(&result.Mode, &result.Version, &result.ChangedAt)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(result)
	tag, err := tx.Exec(ctx, `UPDATE manager_price_execution_control_operations SET status='succeeded',result_control=$4 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='awaiting_confirmation' AND expires_at>statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPriceExecutionControlChanged
	}
	return tx.Commit(ctx)
}

var priceExecutionAccountPattern = regexp.MustCompile(`^(wb|ozon):[a-f0-9]{64}$`)
var ErrPriceExecutionAccountHeld = errors.New("price account execution control is busy")

func acquirePriceExecutionAccount(ctx context.Context, tx pgx.Tx, account string) error {
	if !priceExecutionAccountPattern.MatchString(account) {
		return ErrPriceExecutionControlChanged
	}
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "price-execution:"+account).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrPriceExecutionAccountHeld
	}
	return nil
}

// Call under the account lock immediately before committing the dispatch marker.
// The version comes from the original immutable financial-owner proposal, not
// from a new approval timestamp or the worker's current wall clock.
func (q *Queries) checkPriceExecutionDecisions(ctx context.Context, account, market string, decisions [][]byte) error {
	state, err := q.PriceExecutionControl(ctx, account, market)
	if err != nil {
		return err
	}
	if state.Mode == "stopped" {
		return ErrPriceExecutionStopped
	}
	if len(decisions) == 0 {
		return ErrPriceExecutionControlChanged
	}
	for _, raw := range decisions {
		var envelope struct {
			ManagerApproval *struct {
				Version *int64 `json:"execution_control_version"`
			} `json:"manager_approval"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return ErrPriceExecutionControlChanged
			}
		}
		manager := envelope.ManagerApproval != nil
		version := int64(0)
		if manager && envelope.ManagerApproval.Version != nil {
			version = *envelope.ManagerApproval.Version
		}
		if state.Mode == "manual_only" && !manager || manager && (version < 0 || version != state.Version) {
			return ErrPriceExecutionStopped
		}
	}
	return q.checkLegacyPricePermitFence(ctx, account)
}
