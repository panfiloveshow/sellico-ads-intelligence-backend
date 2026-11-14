package sqlcgen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

var ErrLegacyPriceDispatchReplay = errors.New("legacy price dispatch already authorized or expired")
var ErrLegacyPriceActiveWrite = errors.New("legacy dispatch blocked by an active price write")
var ErrLegacyPriceBindingUnknown = errors.New("active price write account binding is unknown")

// A v1 admission is not a terminal Native receipt. In particular its five-second
// start window cannot establish that a separate Products process never sent or
// that an asynchronous Native write finished. No timer, current-price match or
// control change erases this uncertainty. The only release is a committed
// personal resolution of that exact admission (legacyPriceResolutionMatch): an
// owner-accepted uncertainty, never an inferred Native success. Missing review
// storage is an error, not an empty journal. Called under
// acquirePriceExecutionAccount.
func (q *Queries) checkLegacyPricePermitFence(ctx context.Context, account string) error {
	var held bool
	if err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_price_dispatch_authorizations a
 WHERE a.account_key=$1 AND NOT EXISTS(SELECT 1 FROM legacy_price_personal_reviews r WHERE `+legacyPriceResolutionMatch+`))`, account).Scan(&held); err != nil {
		return err
	}
	if held {
		return ErrLegacyPriceActiveWrite
	}
	return nil
}

// The v1 attestation contains a hash, not the exact native product identities.
// Therefore one unresolved physical write holds new raw requests for its whole
// account. Old WB rows without a trustworthy binding cannot be excluded by a
// current cabinet/token and conservatively hold all raw WB requests.
func (q *Queries) checkLegacyPriceActiveWrites(ctx context.Context, account, market string) error {
	var held, unbound bool
	var err error
	if market == "ozon" {
		err = q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_price_write_attempts
 WHERE vendor_account_key=$1 AND resource_key IS NOT NULL)`, strings.TrimPrefix(account, "ozon:")).Scan(&held)
	} else {
		err = q.db.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM price_changes WHERE wb_status IN ('submitting','submit_unknown','uploaded')
  AND jsonb_typeof(decision_context#>'{wb_dispatch,account_key}')='string'
  AND decision_context#>>'{wb_dispatch,account_key}' ~ '^wb:[a-f0-9]{64}$'
  AND decision_context#>>'{wb_dispatch,account_key}'=$1),
 EXISTS(SELECT 1 FROM price_changes WHERE wb_status IN ('submitting','submit_unknown','uploaded')
  AND NOT COALESCE(jsonb_typeof(decision_context#>'{wb_dispatch,account_key}')='string'
   AND decision_context#>>'{wb_dispatch,account_key}' ~ '^wb:[a-f0-9]{64}$',false))`, account).Scan(&held, &unbound)
	}
	if err != nil {
		return err
	}
	if held {
		return ErrLegacyPriceActiveWrite
	}
	if unbound {
		return ErrLegacyPriceBindingUnknown
	}
	return nil
}

// AuthorizeLegacyPriceDispatch shares the actual account barrier with personal
// stops and both native price executors. No supplied manager_approval can make a
// legacy transport eligible after an explicit account restriction exists.
func (q *Queries) AuthorizeLegacyPriceDispatch(ctx context.Context, request domain.LegacyPriceDispatchRequest) (domain.LegacyPriceDispatchGrant, error) {
	var grant domain.LegacyPriceDispatchGrant
	if request.Origin != "products" || request.Method != "POST" || request.Marketplace != "wb" && request.Marketplace != "ozon" {
		return grant, ErrPriceExecutionControlChanged
	}
	id, err := uuid.Parse(request.CommandID)
	if err != nil || id.String() != request.CommandID {
		return grant, ErrPriceExecutionControlChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return grant, err
	}
	defer tx.Rollback(context.Background())
	if err = acquirePriceExecutionAccount(ctx, tx, request.AccountKey); err != nil {
		return grant, err
	}
	state, err := q.WithTx(tx).PriceExecutionControl(ctx, request.AccountKey, request.Marketplace)
	if err != nil {
		return grant, err
	}
	if state.Mode != "legacy" || state.Version != 0 {
		return grant, ErrPriceExecutionStopped
	}
	var replay bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM legacy_price_dispatch_authorizations WHERE command_id=$1)", id).Scan(&replay); err != nil {
		return grant, err
	}
	if replay {
		return grant, ErrLegacyPriceDispatchReplay
	}
	if err = q.WithTx(tx).checkLegacyPricePermitFence(ctx, request.AccountKey); err != nil {
		return grant, err
	}
	if err = q.WithTx(tx).checkLegacyPriceActiveWrites(ctx, request.AccountKey, request.Marketplace); err != nil {
		return grant, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return grant, err
	}
	sum := sha256.Sum256(raw)
	err = tx.QueryRow(ctx, `INSERT INTO legacy_price_dispatch_authorizations
 (command_id,origin,account_key,marketplace,endpoint,payload_sha256,request_fingerprint,sent_at,issued_at,expires_at)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,statement_timestamp(),statement_timestamp()+interval '5 seconds'
 WHERE $8::timestamptz BETWEEN statement_timestamp()-interval '30 seconds' AND statement_timestamp()+interval '30 seconds'
 ON CONFLICT(command_id) DO NOTHING RETURNING issued_at,expires_at`, id, request.Origin, request.AccountKey, request.Marketplace, request.Endpoint, request.PayloadSHA256, hex.EncodeToString(sum[:]), time.Unix(request.SentAt, 0).UTC()).Scan(&grant.IssuedAt, &grant.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return grant, ErrLegacyPriceDispatchReplay
	}
	if err != nil {
		return grant, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.LegacyPriceDispatchGrant{}, err
	}
	grant.Version, grant.Origin, grant.CommandID = "legacy_price_dispatch_grant_v1", request.Origin, request.CommandID
	grant.Marketplace, grant.Endpoint, grant.PayloadSHA256 = request.Marketplace, request.Endpoint, request.PayloadSHA256
	grant.Method = request.Method
	grant.Authorized, grant.Scope, grant.ValidForMS = true, "one_legacy_price_request", 5000
	return grant, nil
}
