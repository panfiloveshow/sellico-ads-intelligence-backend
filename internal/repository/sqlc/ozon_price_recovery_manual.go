package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrOzonPriceHeld       = errors.New("another Ozon price command holds this product")
	ErrOzonPriceChanged    = errors.New("price command is no longer pending or its baseline changed")
	ErrOzonPriceBudget     = errors.New("Ozon hourly price write budget exhausted")
	ErrOzonScheduleStarted = errors.New("Ozon schedule already started or is no longer pending")
	ErrOzonScheduleLegacy  = errors.New("legacy Ozon calendar command needs manual reconciliation")
)

// OzonPriceWriteLease is the immutable dispatch payload and its durable
// receipt/readback state. The account key excludes API keys and local aliases.
type OzonPriceWriteLease struct {
	ChangeID, SellerCabinetID                pgtype.UUID
	ProductID                                int64
	VendorAccountKey, Status                 string
	ResourceKey                              pgtype.Text
	PriceCents                               int64
	OldPriceCents, MinPriceCents             *int64
	Baseline, Observed                       []byte
	ClaimedAt                                time.Time
	DispatchedAt, AcknowledgedAt, VerifiedAt pgtype.Timestamptz
	LastObservedAt                           pgtype.Timestamptz
	Error                                    pgtype.Text
}

type ozonPriceTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func (q *Queries) ozonPriceTx(ctx context.Context) (pgx.Tx, error) {
	b, ok := q.db.(ozonPriceTxBeginner)
	if !ok {
		return nil, errors.New("Ozon price journal requires transactional storage")
	}
	return b.Begin(ctx)
}

// ClaimOzonPriceWrite acquires a physical product hold and rechecks the budget
// in the same transaction as the pending -> submitting transition. A unique
// resource_key prevents concurrent aliases from both reaching dispatch.
func (q *Queries) ClaimOzonPriceWrite(ctx context.Context, changeID, cabinetID pgtype.UUID, productID int64, accountKey, resourceKey string, currentCents, targetCents int64, baseline []byte, budget int64) (err error) {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var rowCabinet pgtype.UUID
	var sku, target int64
	var previous, oldPrice, minPrice pgtype.Int8
	var status string
	err = tx.QueryRow(ctx, `SELECT seller_cabinet_id, sku, (old_price_rub*100)::bigint,
		(new_price_rub*100)::bigint, (new_old_price_rub*100)::bigint,
		(min_price_rub*100)::bigint, status
		FROM ozon_price_changes WHERE id=$1 FOR UPDATE`, changeID).
		Scan(&rowCabinet, &sku, &previous, &target, &oldPrice, &minPrice, &status)
	if err != nil {
		return err
	}
	if rowCabinet != cabinetID || sku != productID || status != "pending" || !previous.Valid || previous.Int64 != currentCents || target != targetCents || target <= 0 {
		return ErrOzonPriceChanged
	}
	_, err = tx.Exec(ctx, `INSERT INTO ozon_price_write_attempts
		(change_id, seller_cabinet_id, product_id, vendor_account_key, resource_key,
		 status, price_cents, old_price_cents, min_price_cents, baseline)
		VALUES ($1,$2,$3,$4,$5,'submitting',$6,$7,$8,$9)`,
		changeID, cabinetID, productID, accountKey, resourceKey, target, oldPrice, minPrice, baseline)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrOzonPriceHeld
		}
		return err
	}
	var legacyPending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_price_changes legacy
		WHERE legacy.seller_cabinet_id=$1 AND legacy.sku=$2 AND legacy.id<>$3
		AND legacy.status IN ('pending','submitting','verifying','submit_unknown')
		AND COALESCE(legacy.decision_context->>'write_protocol','')<>'observed_v1'
		AND NOT EXISTS(SELECT 1 FROM ozon_price_write_attempts a WHERE a.change_id=legacy.id))`,
		cabinetID, productID, changeID).Scan(&legacyPending); err != nil {
		return err
	}
	if legacyPending {
		return ErrOzonPriceHeld
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM ozon_price_write_attempts
		 WHERE vendor_account_key=$1 AND product_id=$2 AND dispatched_at >= now()-interval '1 hour')
		+
		(SELECT count(*) FROM ozon_price_changes legacy
		 WHERE legacy.seller_cabinet_id=$3 AND legacy.sku=$2
		   AND legacy.status <> 'shadow' AND legacy.created_at >= now()-interval '1 hour'
		   AND COALESCE(legacy.decision_context->>'write_protocol','')<>'observed_v1'
		   AND legacy.id<>$4
		   AND NOT EXISTS (SELECT 1 FROM ozon_price_write_attempts a WHERE a.change_id=legacy.id))`,
		accountKey, productID, cabinetID, changeID).Scan(&count)
	if err != nil {
		return err
	}
	if count >= budget {
		return ErrOzonPriceBudget
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_price_changes SET status='submitting', error=NULL WHERE id=$1`, changeID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkOzonPricesDispatched must commit before HTTP. Expired workers cannot
// send after recovery has released their undispatched claim.
func (q *Queries) MarkOzonPricesDispatched(ctx context.Context, ids []pgtype.UUID) error {
	if len(ids) == 0 || len(ids) > 1000 {
		return ErrOzonPriceChanged
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// The account binding is immutable on a price lease. Take account locks
	// before row locks, in a stable order shared with personal stop decisions.
	accountRows, err := tx.Query(ctx, `SELECT DISTINCT vendor_account_key FROM ozon_price_write_attempts WHERE change_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	accounts := map[string][][]byte{}
	for accountRows.Next() {
		var account string
		if err = accountRows.Scan(&account); err != nil {
			accountRows.Close()
			return err
		}
		accounts["ozon:"+account] = nil
	}
	err = accountRows.Err()
	accountRows.Close()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return ErrOzonPriceChanged
	}
	keys := make([]string, 0, len(accounts))
	for key := range accounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err = acquirePriceExecutionAccount(ctx, tx, key); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT a.vendor_account_key,p.decision_context FROM ozon_price_write_attempts a JOIN ozon_price_changes p ON p.id=a.change_id
 WHERE a.change_id=ANY($1::uuid[]) AND a.status='submitting' AND a.dispatched_at IS NULL FOR UPDATE OF a,p`, ids)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var account string
		var raw []byte
		if err = rows.Scan(&account, &raw); err != nil {
			rows.Close()
			return err
		}
		key := "ozon:" + account
		if _, known := accounts[key]; !known {
			rows.Close()
			return ErrPriceExecutionControlChanged
		}
		accounts[key] = append(accounts[key], raw)
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != len(ids) {
		return ErrOzonPriceChanged
	}
	for _, key := range keys {
		if err = q.WithTx(tx).checkPriceExecutionDecisions(ctx, key, "ozon", accounts[key]); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE ozon_price_write_attempts SET dispatched_at=now()
		WHERE change_id=ANY($1::uuid[]) AND status='submitting' AND dispatched_at IS NULL
		AND claimed_at > now()-interval '3 minutes'
		AND EXISTS (SELECT 1 FROM ozon_price_changes p WHERE p.id=change_id
		  AND (NOT COALESCE(p.decision_context ? 'manager_approval',false) OR
		    COALESCE((p.decision_context#>>'{manager_approval,expires_at}')::timestamptz > statement_timestamp(),false)))`, ids)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(ids)) || len(ids) == 0 {
		return ErrOzonPriceChanged
	}
	return tx.Commit(ctx)
}

func (q *Queries) FailUnclaimedOzonPrice(ctx context.Context, id pgtype.UUID, message string) error {
	_, err := q.db.Exec(ctx, `UPDATE ozon_price_changes SET status='failed', error=$2
		WHERE id=$1 AND status='pending'
		AND NOT EXISTS (SELECT 1 FROM ozon_price_write_attempts WHERE change_id=$1)`, id, message)
	return err
}

func (q *Queries) AbortUndispatchedOzonPrice(ctx context.Context, id pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `WITH changed AS (
		UPDATE ozon_price_write_attempts SET status='failed', resource_key=NULL, error='not dispatched'
		WHERE change_id=$1 AND status='submitting' AND dispatched_at IS NULL RETURNING change_id
	) UPDATE ozon_price_changes SET status='failed', error='not dispatched'
	WHERE id IN (SELECT change_id FROM changed)`, id)
	return err
}

func (q *Queries) RecordOzonPriceReceipt(ctx context.Context, id pgtype.UUID, status, message string) error {
	if status != "verifying" && status != "submit_unknown" && status != "failed" {
		return errors.New("invalid Ozon receipt state")
	}
	_, err := q.db.Exec(ctx, `WITH changed AS (
		UPDATE ozon_price_write_attempts SET status=$2, error=NULLIF($3,''),
		acknowledged_at=CASE WHEN $2='verifying' THEN now() ELSE acknowledged_at END,
		resource_key=CASE WHEN $2='failed' THEN NULL ELSE resource_key END
		WHERE change_id=$1 AND dispatched_at IS NOT NULL AND status IN ('submitting','submit_unknown')
		RETURNING change_id
	) UPDATE ozon_price_changes SET status=$2, error=NULLIF($3,'')
	WHERE id IN (SELECT change_id FROM changed)`, id, status, message)
	return err
}

const ozonPriceLeaseColumns = `change_id, seller_cabinet_id, product_id, vendor_account_key,
	resource_key, status, price_cents, old_price_cents, min_price_cents, baseline, observed,
	claimed_at, dispatched_at, acknowledged_at, verified_at, last_observed_at, error`

func scanOzonPriceLease(row pgx.Row) (OzonPriceWriteLease, error) {
	var a OzonPriceWriteLease
	err := row.Scan(&a.ChangeID, &a.SellerCabinetID, &a.ProductID, &a.VendorAccountKey,
		&a.ResourceKey, &a.Status, &a.PriceCents, &a.OldPriceCents, &a.MinPriceCents, &a.Baseline, &a.Observed,
		&a.ClaimedAt, &a.DispatchedAt, &a.AcknowledgedAt, &a.VerifiedAt, &a.LastObservedAt, &a.Error)
	return a, err
}

func (q *Queries) GetOzonPriceLease(ctx context.Context, id pgtype.UUID) (OzonPriceWriteLease, error) {
	return scanOzonPriceLease(q.db.QueryRow(ctx, `SELECT `+ozonPriceLeaseColumns+` FROM ozon_price_write_attempts WHERE change_id=$1`, id))
}

func (q *Queries) ListActiveOzonPriceLeases(ctx context.Context, cabinet, after pgtype.UUID, limit int32) ([]OzonPriceWriteLease, error) {
	rows, err := q.db.Query(ctx, `SELECT `+ozonPriceLeaseColumns+` FROM ozon_price_write_attempts
		WHERE seller_cabinet_id=$1 AND resource_key IS NOT NULL
		AND ($2::uuid IS NULL OR change_id>$2::uuid) ORDER BY change_id LIMIT $3`, cabinet, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []OzonPriceWriteLease{}
	for rows.Next() {
		a, err := scanOzonPriceLease(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// RecoverOzonPriceClaims never requeues a dispatched request. Only a claim
// whose dispatch marker was never committed can be safely released.
func (q *Queries) RecoverOzonPriceClaims(ctx context.Context, cabinetID pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `WITH changed AS (
		UPDATE ozon_price_write_attempts
		SET status=CASE WHEN dispatched_at IS NULL THEN 'failed' ELSE 'submit_unknown' END,
		resource_key=CASE WHEN dispatched_at IS NULL THEN NULL ELSE resource_key END,
		error=CASE WHEN dispatched_at IS NULL THEN 'expired before dispatch' ELSE 'submission receipt unavailable' END
		WHERE seller_cabinet_id=$1 AND status='submitting' AND claimed_at<now()-interval '3 minutes'
		RETURNING change_id,status,error
	) UPDATE ozon_price_changes c SET status=changed.status,error=changed.error
	FROM changed WHERE c.id=changed.change_id`, cabinetID)
	return err
}

// Recovery also runs for cabinets without active price strategies, including
// paused automation. It only reads the vendor and never dispatches.
func (q *Queries) ListOzonPriceRecoveryCabinets(ctx context.Context, workspaceID pgtype.UUID) ([]pgtype.UUID, error) {
	rows, err := q.db.Query(ctx, `SELECT DISTINCT c.id FROM seller_cabinets c
		JOIN ozon_price_write_attempts a ON a.seller_cabinet_id=c.id
		WHERE c.workspace_id=$1 AND c.deleted_at IS NULL AND c.marketplace='ozon'
		AND a.resource_key IS NOT NULL ORDER BY c.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (q *Queries) OzonSchedulePriceChange(ctx context.Context, entryID pgtype.UUID, phase string) (OzonPriceChange, error) {
	var id pgtype.UUID
	err := q.db.QueryRow(ctx, `SELECT id FROM ozon_price_changes
		WHERE decision_context->>'schedule_id'=$1::uuid::text
		AND decision_context->>'schedule_phase'=$2`, entryID, phase).Scan(&id)
	if err != nil {
		return OzonPriceChange{}, err
	}
	return q.GetOzonPriceChangeByID(ctx, id)
}

// CreateOzonScheduleCommand serializes with cancellation and returns the
// existing immutable phase on a repeated/concurrent executor pass.
func (q *Queries) CreateOzonScheduleCommand(ctx context.Context, entryID pgtype.UUID, phase string, currentPrice, currentOldPrice pgtype.Numeric, offerID pgtype.Text) (OzonPriceChange, error) {
	if phase != "apply" && phase != "revert" {
		return OzonPriceChange{}, errors.New("invalid calendar phase")
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return OzonPriceChange{}, err
	}
	defer tx.Rollback(context.Background())
	var locked pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM ozon_price_schedule_entries WHERE id=$1 FOR UPDATE`, entryID).Scan(&locked); err != nil {
		return OzonPriceChange{}, err
	}
	qt := q.WithTx(tx)
	if existing, findErr := qt.OzonSchedulePriceChange(ctx, entryID, phase); findErr == nil {
		if err = tx.Commit(ctx); err != nil {
			return OzonPriceChange{}, err
		}
		return existing, nil
	} else if !errors.Is(findErr, pgx.ErrNoRows) {
		return OzonPriceChange{}, findErr
	}
	entry, err := qt.GetOzonPriceScheduleEntry(ctx, entryID)
	if err != nil {
		return OzonPriceChange{}, err
	}
	target, reason := entry.ScheduledPriceRub, "schedule"
	if phase == "apply" && entry.Status != "pending" {
		return OzonPriceChange{}, ErrOzonScheduleStarted
	}
	if phase == "revert" {
		if entry.Status != "applied" {
			return OzonPriceChange{}, ErrOzonScheduleStarted
		}
		target, reason = entry.RevertPriceRub, "schedule revert"
	}
	var legacy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_price_changes
		WHERE decision_context->>'schedule_id'=$1::uuid::text AND NOT(decision_context ? 'schedule_phase')
		AND decision_context->>'reason'=$2)`, entryID, reason).Scan(&legacy); err != nil {
		return OzonPriceChange{}, err
	}
	if legacy {
		return OzonPriceChange{}, ErrOzonScheduleLegacy
	}
	contextJSON, err := json.Marshal(map[string]string{"actor_type": "schedule", "write_protocol": "observed_v1", "reason": reason, "schedule_id": uuid.UUID(entryID.Bytes).String(), "schedule_phase": phase})
	if err != nil {
		return OzonPriceChange{}, err
	}
	change, err := qt.InsertOzonPriceChange(ctx, InsertOzonPriceChangeParams{SellerCabinetID: entry.SellerCabinetID, Sku: entry.Sku, OfferID: offerID, OldPriceRub: currentPrice, OldOldPriceRub: currentOldPrice, NewPriceRub: target, Source: "manual", Status: "pending", Reason: pgtype.Text{String: reason, Valid: true}, DecisionContext: contextJSON})
	if err != nil {
		return OzonPriceChange{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return OzonPriceChange{}, err
	}
	return change, nil
}

func (q *Queries) CancelUnstartedOzonPriceSchedule(ctx context.Context, entryID pgtype.UUID) error {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM ozon_price_schedule_entries WHERE id=$1 FOR UPDATE`, entryID).Scan(&status); err != nil {
		return err
	}
	var started bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_price_changes
		WHERE decision_context->>'schedule_id'=$1::uuid::text
		AND (decision_context->>'schedule_phase'='apply'
		OR (NOT(decision_context ? 'schedule_phase') AND decision_context->>'reason'='schedule')))`, entryID).Scan(&started); err != nil {
		return err
	}
	if status != "pending" || started {
		return ErrOzonScheduleStarted
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_price_schedule_entries SET status='cancelled',error=NULL WHERE id=$1`, entryID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// A late worker cannot overwrite cancelled or already reverted history.
func (q *Queries) FinishOzonSchedulePhase(ctx context.Context, entryID, changeID pgtype.UUID, phase string) error {
	previous, next := "pending", "applied"
	if phase == "revert" {
		previous, next = "applied", "reverted"
	} else if phase != "apply" {
		return errors.New("invalid calendar phase")
	}
	_, err := q.db.Exec(ctx, `UPDATE ozon_price_schedule_entries SET status=$4,error=NULL,
		applied_at=CASE WHEN $4='applied' THEN COALESCE(applied_at,now()) ELSE applied_at END,
		reverted_at=CASE WHEN $4='reverted' THEN COALESCE(reverted_at,now()) ELSE reverted_at END
		WHERE id=$1 AND status=$3 AND EXISTS(SELECT 1 FROM ozon_price_changes
		WHERE id=$2 AND status='applied' AND decision_context->>'schedule_id'=$1::uuid::text
		AND decision_context->>'schedule_phase'=$5)`, entryID, changeID, previous, next, phase)
	return err
}

func (q *Queries) NoteOzonScheduleWaiting(ctx context.Context, entryID pgtype.UUID, phase, message string) error {
	previous := "pending"
	if phase == "revert" {
		previous = "applied"
	} else if phase != "apply" {
		return errors.New("invalid calendar phase")
	}
	_, err := q.db.Exec(ctx, `UPDATE ozon_price_schedule_entries SET error=$3 WHERE id=$1 AND status=$2`, entryID, previous, message)
	return err
}

type OzonPriceObservation struct {
	JSON                         []byte
	PriceCents                   int64
	OldPriceCents, MinPriceCents *int64
}

// ObserveOzonPrice updates the mirror only after both a known acknowledgement
// and an exact readback. A matching price alone never resolves submit_unknown.
func (q *Queries) ObserveOzonPrice(ctx context.Context, id pgtype.UUID, observed OzonPriceObservation) error {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	a, err := scanOzonPriceLease(tx.QueryRow(ctx, `SELECT `+ozonPriceLeaseColumns+` FROM ozon_price_write_attempts WHERE change_id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if !a.ResourceKey.Valid || !a.DispatchedAt.Valid {
		return nil
	}
	matches := a.PriceCents == observed.PriceCents &&
		(a.OldPriceCents == nil || (observed.OldPriceCents != nil && *a.OldPriceCents == *observed.OldPriceCents)) &&
		(a.MinPriceCents == nil || (observed.MinPriceCents != nil && *a.MinPriceCents == *observed.MinPriceCents))
	message := "price not yet observed at requested values"
	if !a.AcknowledgedAt.Valid {
		message = "matching observation cannot prove an unacknowledged command"
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_price_write_attempts SET observed=$2, last_observed_at=now(), error=$3 WHERE change_id=$1`, id, observed.JSON, message); err != nil {
		return err
	}
	if matches && a.AcknowledgedAt.Valid {
		if _, err = tx.Exec(ctx, `UPDATE ozon_price_write_attempts SET status='applied', resource_key=NULL,
		verified_at=now(), error=NULL WHERE change_id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE ozon_price_changes SET status='applied', error=NULL, applied_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		// This is a price observation. Do not refresh synced_at for unrelated
		// commission, acquiring and competitor fields in the hourly mirror.
		if _, err = tx.Exec(ctx, `UPDATE ozon_product_prices SET price_rub=$3::bigint/100.0,
		old_price_rub=$4::bigint/100.0, min_price_rub=$5::bigint/100.0
		WHERE seller_cabinet_id=$1 AND sku=$2`, a.SellerCabinetID, a.ProductID, observed.PriceCents, observed.OldPriceCents, observed.MinPriceCents); err != nil {
			return err
		}
		// Late verification of an audited reversal must finalize the original
		// too; a timeout in the HTTP caller must not lose that relationship.
		if _, err = tx.Exec(ctx, `UPDATE ozon_price_changes original SET status='rolled_back'
		FROM ozon_price_changes reversal
		WHERE reversal.id=$1 AND reversal.decision_context->>'rollback_of'=original.id::text
		AND original.status='applied' AND original.seller_cabinet_id=reversal.seller_cabinet_id
		AND original.sku=reversal.sku AND original.new_price_rub=reversal.old_price_rub
		AND original.old_price_rub=reversal.new_price_rub`, id); err != nil {
			return err
		}
	} else if _, err = tx.Exec(ctx, `UPDATE ozon_price_changes SET error=$2 WHERE id=$1 AND status IN ('submitting','verifying','submit_unknown')`, id, message); err != nil {
		return fmt.Errorf("record unresolved price observation: %w", err)
	}
	return tx.Commit(ctx)
}
