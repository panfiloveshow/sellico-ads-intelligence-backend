package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

var ErrAdResourceHeld = errors.New("advertising resource has an unresolved command")
var ErrAdCommandChanged = errors.New("advertising approval expired, changed or already entered transport")

type NativeAdCommand struct {
	ID                               uuid.UUID
	Request                          adguard.Request
	Origin, Status                   string
	WorkspaceID, ActorID             pgtype.UUID
	ApprovalFingerprint              string
	ExpiresAt, StartedAt, FinishedAt pgtype.Timestamptz
	HTTPStatus                       pgtype.Int2
	ResponseHash                     pgtype.Text
	ExecutionControlVersion          int64
}

const nativeAdColumns = `id,marketplace,account_key,request_fingerprint,method,endpoint,resources,origin,status,workspace_id,actor_id,COALESCE(approval_fingerprint,''),approval_expires_at,started_at,finished_at,http_status,response_hash,execution_control_version`

func scanNativeAd(row rowScanner) (NativeAdCommand, error) {
	var c NativeAdCommand
	var raw []byte
	err := row.Scan(&c.ID, &c.Request.Marketplace, &c.Request.AccountKey, &c.Request.Fingerprint, &c.Request.Method, &c.Request.URL, &raw, &c.Origin, &c.Status, &c.WorkspaceID, &c.ActorID, &c.ApprovalFingerprint, &c.ExpiresAt, &c.StartedAt, &c.FinishedAt, &c.HTTPStatus, &c.ResponseHash, &c.ExecutionControlVersion)
	if err == nil {
		err = json.Unmarshal(raw, &c.Request.Resources)
	}
	return c, err
}

func (q *Queries) ReadNativeAdCommand(ctx context.Context, id uuid.UUID) (NativeAdCommand, error) {
	return scanNativeAd(q.db.QueryRow(ctx, `SELECT `+nativeAdColumns+` FROM ad_native_commands WHERE id=$1`, id))
}

func acquireAdAccount(ctx context.Context, tx pgx.Tx, account string) error {
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "ad-native:"+account).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrAdResourceHeld
	}
	return nil
}
func (q *Queries) adClaimsAvailable(ctx context.Context, r adguard.Request, id uuid.UUID) error {
	var held bool
	global := len(r.Resources) == 1 && r.Resources[0] == "*"
	err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ad_native_resource_holds WHERE command_id<>$1 AND
 (resource_key=ANY($2::text[]) OR (account_key=$3 AND (global_scope OR $4))))`, id, r.ResourceKeys(), r.AccountKey, global).Scan(&held)
	if err != nil {
		return err
	}
	if held {
		return ErrAdResourceHeld
	}
	return nil
}
func (q *Queries) insertAdHolds(ctx context.Context, r adguard.Request, id uuid.UUID) error {
	for _, key := range r.ResourceKeys() {
		_, err := q.db.Exec(ctx, `INSERT INTO ad_native_resource_holds(resource_key,account_key,command_id,global_scope) VALUES($1,$2,$3,$4)`, key, r.AccountKey, id, len(r.Resources) == 1 && r.Resources[0] == "*")
		if err != nil {
			var pgerr *pgconn.PgError
			if errors.As(err, &pgerr) && pgerr.Code == "23505" {
				return ErrAdResourceHeld
			}
			return err
		}
	}
	return nil
}

// ReserveApprovedAdCommand is an internal seam for a personally authorized
// operation. No public endpoint accepts its scope or approval as client input.
// The personal approval service creates it only after fresh CRM checks.
func (q *Queries) ReserveApprovedAdCommand(ctx context.Context, id uuid.UUID, r adguard.Request, workspace, actor pgtype.UUID, approval string, created, expires time.Time) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if id == uuid.Nil || !workspace.Valid || !actor.Valid || len(approval) != 64 || created.IsZero() || created.After(time.Now().Add(5*time.Second)) || !expires.After(created) || expires.Sub(created) > 5*time.Minute || !expires.After(time.Now()) || expires.After(time.Now().Add(5*time.Minute)) {
		return ErrAdCommandChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = acquireAdAccount(ctx, tx, r.AccountKey); err != nil {
		return err
	}
	qt := q.WithTx(tx)
	controlVersion, err := qt.adExecutionReservation(ctx, r.AccountKey, r.Marketplace, true, created)
	if err != nil {
		return err
	}
	if err = qt.adClaimsAvailable(ctx, r, id); err != nil {
		return err
	}
	raw, _ := json.Marshal(r.Resources)
	_, err = tx.Exec(ctx, `INSERT INTO ad_native_commands(id,marketplace,account_key,request_fingerprint,method,endpoint,resources,origin,status,workspace_id,actor_id,approval_fingerprint,approval_expires_at,execution_control_version) VALUES($1,$2,$3,$4,$5,$6,$7,'manager','approved',$8,$9,$10,$11,$12)`, id, r.Marketplace, r.AccountKey, r.Fingerprint, r.Method, r.URL, raw, workspace, actor, approval, expires, controlVersion)
	if err != nil {
		return err
	}
	if err = qt.insertAdHolds(ctx, r, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (q *Queries) BeginAdvertisingMutation(ctx context.Context, r adguard.Request) (adguard.Lease, error) {
	if err := r.Validate(); err != nil {
		return adguard.Lease{}, err
	}
	id := adguard.ApprovedCommand(ctx)
	manager := id != uuid.Nil
	if manager {
		if err := adguard.CheckApprovalGate(ctx, r); err != nil {
			return adguard.Lease{}, errors.Join(ErrAdCommandChanged, err)
		}
	}
	if !manager {
		id = uuid.New()
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return adguard.Lease{}, err
	}
	defer tx.Rollback(context.Background())
	if err = acquireAdAccount(ctx, tx, r.AccountKey); err != nil {
		return adguard.Lease{}, err
	}
	qt := q.WithTx(tx)
	if err = qt.adClaimsAvailable(ctx, r, id); err != nil {
		return adguard.Lease{}, err
	}
	if manager {
		row, err := scanNativeAd(tx.QueryRow(ctx, `SELECT `+nativeAdColumns+` FROM ad_native_commands WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return adguard.Lease{}, err
		}
		if row.Status != "approved" || row.Origin != "manager" || row.StartedAt.Valid || row.Request.Fingerprint != r.Fingerprint || row.Request.AccountKey != r.AccountKey {
			return adguard.Lease{}, ErrAdCommandChanged
		}
		if err = qt.adExecutionDispatch(ctx, r.AccountKey, r.Marketplace, true, row.ExecutionControlVersion); err != nil {
			return adguard.Lease{}, err
		}
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1 AND resource_key=ANY($2::text[])`, id, r.ResourceKeys()).Scan(&count)
		if err != nil {
			return adguard.Lease{}, err
		}
		if count != len(r.ResourceKeys()) {
			return adguard.Lease{}, ErrAdCommandChanged
		}
		tag, err := tx.Exec(ctx, `UPDATE ad_native_commands SET status='dispatching',started_at=statement_timestamp() WHERE id=$1 AND status='approved' AND approval_expires_at>statement_timestamp()`, id)
		if err != nil {
			return adguard.Lease{}, err
		}
		if tag.RowsAffected() != 1 {
			return adguard.Lease{}, ErrAdCommandChanged
		}
	} else {
		if err = qt.adExecutionDispatch(ctx, r.AccountKey, r.Marketplace, false, 0); err != nil {
			return adguard.Lease{}, err
		}
		raw, _ := json.Marshal(r.Resources)
		_, err = tx.Exec(ctx, `INSERT INTO ad_native_commands(id,marketplace,account_key,request_fingerprint,method,endpoint,resources,origin,status,started_at) VALUES($1,$2,$3,$4,$5,$6,$7,'legacy','dispatching',statement_timestamp())`, id, r.Marketplace, r.AccountKey, r.Fingerprint, r.Method, r.URL, raw)
		if err != nil {
			return adguard.Lease{}, err
		}
		if err = qt.insertAdHolds(ctx, r, id); err != nil {
			return adguard.Lease{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return adguard.Lease{}, err
	}
	return adguard.Lease{ID: id}, nil
}

func (q *Queries) CompleteAdvertisingMutation(ctx context.Context, lease adguard.Lease, outcome adguard.Outcome) error {
	if lease.ID == uuid.Nil || (outcome.State != "acknowledged" && outcome.State != "rejected" && outcome.State != "unknown") || outcome.HTTPStatus < 0 || outcome.HTTPStatus > 599 || len(outcome.ResponseHash) != 64 {
		return ErrAdCommandChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var origin string
	err = tx.QueryRow(ctx, `UPDATE ad_native_commands SET status=$2,finished_at=statement_timestamp(),http_status=NULLIF($3,0),response_hash=$4 WHERE id=$1 AND status='dispatching' RETURNING origin`, lease.ID, outcome.State, outcome.HTTPStatus, outcome.ResponseHash).Scan(&origin)
	if err != nil {
		return err
	}
	if outcome.State == "rejected" && origin == "manager" {
		if err = q.WithTx(tx).restoreManagerAdControl(ctx, lease.ID); err != nil {
			return err
		}
	} else if outcome.State == "rejected" || (origin == "legacy" && outcome.State == "acknowledged") {
		if _, err = tx.Exec(ctx, `DELETE FROM ad_native_resource_holds WHERE command_id=$1`, lease.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Only never-dispatched, expired personal approvals may lose their hold by
// time alone. A started/unknown command cannot be freed by a stale lease TTL.
func (q *Queries) ExpireUnsentApprovedAdCommand(ctx context.Context, id uuid.UUID) error {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, `UPDATE ad_native_commands SET status='rejected',finished_at=statement_timestamp() WHERE id=$1 AND origin='manager' AND status='approved' AND started_at IS NULL AND approval_expires_at<=statement_timestamp()`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if err = q.WithTx(tx).restoreManagerAdControl(ctx, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
