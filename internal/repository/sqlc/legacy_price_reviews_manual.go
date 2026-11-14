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

var ErrLegacyPriceReviewChanged = errors.New("legacy price review or account changed")

// One predicate serves the Native fence, journal and receipt reads. A personal
// resolution records accepted uncertainty, never an automatically verified result.
const legacyPriceResolutionMatch = `r.grant_id=a.command_id AND r.account_key=a.account_key
 AND r.marketplace=a.marketplace AND r.grant_fingerprint=a.request_fingerprint
 AND r.status='resolved_manually' AND r.resolved_at>=a.expires_at
 AND r.snapshot#>>'{before,mode}' IN ('stopped','manual_only')
 AND r.snapshot#>>'{before,version}' ~ '^[1-9][0-9]{0,18}$'
 AND r.confirmations='{"legacy_history_and_support_checked":true,"legacy_no_native_work_in_progress":true,"legacy_late_effect_risk_accepted":true,"legacy_new_approvals_only":true}'::jsonb`

type LegacyPriceAuthorizationRecord struct {
	CommandID          uuid.UUID
	AccountKey         string
	Marketplace        string
	Endpoint           string
	RequestFingerprint string
	IssuedAt           time.Time
	ExpiresAt          time.Time
	ResolutionID       *uuid.UUID
}

const legacyPriceRecordColumns = `a.command_id,a.account_key,a.marketplace,a.endpoint,a.request_fingerprint,a.issued_at,a.expires_at,r.id`

func scanLegacyPriceRecord(row pgx.Row) (LegacyPriceAuthorizationRecord, error) {
	var r LegacyPriceAuthorizationRecord
	err := row.Scan(&r.CommandID, &r.AccountKey, &r.Marketplace, &r.Endpoint, &r.RequestFingerprint, &r.IssuedAt, &r.ExpiresAt, &r.ResolutionID)
	return r, err
}

func (q *Queries) LegacyPriceAuthorization(ctx context.Context, account string, id uuid.UUID) (LegacyPriceAuthorizationRecord, error) {
	return scanLegacyPriceRecord(q.db.QueryRow(ctx, `SELECT `+legacyPriceRecordColumns+` FROM legacy_price_dispatch_authorizations a
 LEFT JOIN legacy_price_personal_reviews r ON `+legacyPriceResolutionMatch+` WHERE a.account_key=$1 AND a.command_id=$2`, account, id))
}

func (q *Queries) LegacyPriceAuthorizationPage(ctx context.Context, account string, after time.Time, afterID uuid.UUID) ([]LegacyPriceAuthorizationRecord, bool, error) {
	rows, err := q.db.Query(ctx, `SELECT `+legacyPriceRecordColumns+` FROM legacy_price_dispatch_authorizations a
 LEFT JOIN legacy_price_personal_reviews r ON `+legacyPriceResolutionMatch+`
 WHERE a.account_key=$1 AND ($2::timestamptz IS NULL OR (a.issued_at,a.command_id)>($2,$3))
 ORDER BY a.issued_at,a.command_id LIMIT 26`, account, nullableLegacyTime(after), afterID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]LegacyPriceAuthorizationRecord, 0, 26)
	for rows.Next() {
		r, e := scanLegacyPriceRecord(rows)
		if e != nil {
			return nil, false, e
		}
		result = append(result, r)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > 25
	if more {
		result = result[:25]
	}
	return result, more, nil
}

func nullableLegacyTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (q *Queries) LegacyPriceHeldCount(ctx context.Context, account string) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, `SELECT count(*) FROM legacy_price_dispatch_authorizations a WHERE a.account_key=$1
 AND NOT EXISTS(SELECT 1 FROM legacy_price_personal_reviews r WHERE `+legacyPriceResolutionMatch+`)`, account).Scan(&n)
	return n, err
}

type legacyPriceReviewSnapshot struct {
	Scope            string                            `json:"scope"`
	Entry            domain.LegacyPriceJournalItem     `json:"entry"`
	Before           domain.PriceExecutionControlState `json:"before"`
	Proof            domain.PriceExecutionAccountProof `json:"proof"`
	Reason           string                            `json:"reason"`
	SupportReference string                            `json:"support_reference"`
}

func legacyReviewSnapshot(o domain.LegacyPriceReviewOperation) legacyPriceReviewSnapshot {
	return legacyPriceReviewSnapshot{o.Scope, o.Entry, o.Before, o.Proof, o.Reason, o.SupportReference}
}

func (q *Queries) CreateLegacyPriceReview(ctx context.Context, o domain.LegacyPriceReviewOperation) error {
	body, err := json.Marshal(legacyReviewSnapshot(o))
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO legacy_price_personal_reviews
 (id,workspace_id,actor_id,cabinet_id,external_user_id,external_workspace_id,integration_id,integration_name,
 marketplace,account_key,credential_digest,grant_id,grant_fingerprint,fingerprint,snapshot,status,created_at,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'awaiting_confirmation',$16,$17)`,
		o.ID, o.WorkspaceID, o.ActorID, o.CabinetID, o.ExternalUserID, o.ExternalWorkspaceID, o.IntegrationID, o.IntegrationName,
		o.Marketplace, o.AccountKey, o.CredentialDigest, o.Entry.CommandID, o.GrantFingerprint, o.Fingerprint, body, o.CreatedAt, o.ExpiresAt)
	return err
}

const legacyPriceReviewColumns = `id,workspace_id,actor_id,cabinet_id,external_user_id,external_workspace_id,integration_id,integration_name,
 marketplace,account_key,credential_digest,grant_id,grant_fingerprint,fingerprint,snapshot,status,confirmations,created_at,expires_at,resolved_at`

func scanLegacyPriceReview(row pgx.Row) (domain.LegacyPriceReviewOperation, error) {
	var o domain.LegacyPriceReviewOperation
	var id uuid.UUID
	var raw, checks []byte
	if err := row.Scan(&o.ID, &o.WorkspaceID, &o.ActorID, &o.CabinetID, &o.ExternalUserID, &o.ExternalWorkspaceID,
		&o.IntegrationID, &o.IntegrationName, &o.Marketplace, &o.AccountKey, &o.CredentialDigest, &id, &o.GrantFingerprint,
		&o.Fingerprint, &raw, &o.Status, &checks, &o.CreatedAt, &o.ExpiresAt, &o.ResolvedAt); err != nil {
		return o, err
	}
	var body legacyPriceReviewSnapshot
	if err := json.Unmarshal(raw, &body); err != nil {
		return o, err
	}
	if body.Entry.CommandID != id {
		return o, ErrLegacyPriceReviewChanged
	}
	o.Scope, o.Entry, o.Before, o.Proof, o.Reason, o.SupportReference = body.Scope, body.Entry, body.Before, body.Proof, body.Reason, body.SupportReference
	// The driver returns these columns in the process time zone, while a new
	// proposal is answered from memory in UTC. One operation must serialize the
	// same instants identically on every read: its chat owner compares the saved
	// scope with later receipts and must not mistake a zone offset for a change.
	o.CreatedAt, o.ExpiresAt = o.CreatedAt.UTC(), o.ExpiresAt.UTC()
	if o.ResolvedAt != nil {
		resolved := o.ResolvedAt.UTC()
		o.ResolvedAt = &resolved
	}
	if len(checks) > 0 && string(checks) != "null" {
		if err := json.Unmarshal(checks, &o.Confirmations); err != nil {
			return o, err
		}
	}
	return o, nil
}

func (q *Queries) GetLegacyPriceReview(ctx context.Context, id, workspace, actor uuid.UUID) (domain.LegacyPriceReviewOperation, error) {
	return scanLegacyPriceReview(q.db.QueryRow(ctx, `SELECT `+legacyPriceReviewColumns+` FROM legacy_price_personal_reviews
 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3`, id, workspace, actor))
}

func (q *Queries) ExpireLegacyPriceReview(ctx context.Context, o domain.LegacyPriceReviewOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE legacy_price_personal_reviews SET status='expired'
 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='awaiting_confirmation' AND expires_at<=statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID)
	return err
}

func (q *Queries) CancelLegacyPriceReview(ctx context.Context, o domain.LegacyPriceReviewOperation) error {
	_, err := q.db.Exec(ctx, `UPDATE legacy_price_personal_reviews SET status='cancelled'
 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND fingerprint=$4 AND status='awaiting_confirmation'`, o.ID, o.WorkspaceID, o.ActorID, o.Fingerprint)
	return err
}

func (q *Queries) ApplyLegacyPriceReview(ctx context.Context, o domain.LegacyPriceReviewOperation, checks map[string]bool, validate func(context.Context) error) error {
	if validate == nil || o.Before.Version < 1 || (o.Before.Mode != "stopped" && o.Before.Mode != "manual_only") || o.NativeOutcomeVerified || o.NativePricesChanged {
		return ErrLegacyPriceReviewChanged
	}
	expectedChecks := map[string]bool{"legacy_history_and_support_checked": true, "legacy_no_native_work_in_progress": true,
		"legacy_late_effect_risk_accepted": true, "legacy_new_approvals_only": true}
	if len(checks) != len(expectedChecks) {
		return ErrLegacyPriceReviewChanged
	}
	for key := range expectedChecks {
		if !checks[key] {
			return ErrLegacyPriceReviewChanged
		}
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = acquirePriceExecutionAccount(ctx, tx, o.AccountKey); err != nil {
		return err
	}
	current, err := scanLegacyPriceReview(tx.QueryRow(ctx, `SELECT `+legacyPriceReviewColumns+` FROM legacy_price_personal_reviews
 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 FOR UPDATE`, o.ID, o.WorkspaceID, o.ActorID))
	if err != nil {
		return err
	}
	if current.Status == "resolved_manually" {
		return nil
	}
	wanted, _ := json.Marshal(legacyReviewSnapshot(o))
	saved, _ := json.Marshal(legacyReviewSnapshot(current))
	if current.Status != "awaiting_confirmation" || current.Fingerprint != o.Fingerprint || current.AccountKey != o.AccountKey || current.GrantFingerprint != o.GrantFingerprint || string(wanted) != string(saved) ||
		current.CabinetID != o.CabinetID || current.ExternalUserID != o.ExternalUserID || current.ExternalWorkspaceID != o.ExternalWorkspaceID ||
		current.IntegrationID != o.IntegrationID || current.IntegrationName != o.IntegrationName || current.Marketplace != o.Marketplace || current.CredentialDigest != o.CredentialDigest ||
		!current.CreatedAt.Equal(o.CreatedAt) || !current.ExpiresAt.Equal(o.ExpiresAt) {
		return ErrLegacyPriceReviewChanged
	}
	qt := q.WithTx(tx)
	control, err := qt.PriceExecutionControl(ctx, o.AccountKey, o.Marketplace)
	if err != nil {
		return err
	}
	if control.Mode != o.Before.Mode || control.Version != o.Before.Version {
		return ErrLegacyPriceReviewChanged
	}
	grant, err := qt.LegacyPriceAuthorization(ctx, o.AccountKey, o.Entry.CommandID)
	if err != nil {
		return err
	}
	if grant.ResolutionID != nil || grant.Marketplace != o.Marketplace || grant.RequestFingerprint != o.GrantFingerprint || !grant.IssuedAt.Equal(o.Entry.IssuedAt) || !grant.ExpiresAt.Equal(o.Entry.AdmissionExpiresAt) {
		return ErrLegacyPriceReviewChanged
	}
	if err = validate(ctx); err != nil {
		return err
	}
	if err = qt.checkLegacyPriceActiveWrites(ctx, o.AccountKey, o.Marketplace); err != nil {
		return err
	}
	body, err := json.Marshal(checks)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE legacy_price_personal_reviews SET status='resolved_manually',
 confirmations=$4,resolved_at=statement_timestamp()
 WHERE id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='awaiting_confirmation'
 AND expires_at>statement_timestamp() AND created_at<=statement_timestamp()
 AND $5::timestamptz<=statement_timestamp()`, o.ID, o.WorkspaceID, o.ActorID, body, grant.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLegacyPriceReviewChanged
	}
	return tx.Commit(ctx)
}
