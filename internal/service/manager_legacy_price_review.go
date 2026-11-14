package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const legacyPriceReviewScope = "configured_price_account_legacy_review"

var legacyPriceReviewChecks = []string{"legacy_history_and_support_checked", "legacy_no_native_work_in_progress", "legacy_late_effect_risk_accepted", "legacy_new_approvals_only"}

type ManagerLegacyPriceReviewService struct {
	controls *ManagerPriceExecutionControlService
	enabled  bool
}

func NewManagerLegacyPriceReviewService(q *sqlcgen.Queries, crm managerAdAuthority, w priceControlWBReader, o priceControlOzonReader, key []byte, enabled bool) *ManagerLegacyPriceReviewService {
	return &ManagerLegacyPriceReviewService{controls: NewManagerPriceExecutionControlService(q, crm, w, o, key, false), enabled: enabled}
}

func legacyReviewControlAllowed(c domain.PriceExecutionControlState) bool {
	return c.Version > 0 && (c.Mode == "stopped" || c.Mode == "manual_only")
}

func legacyPriceOperationKind(market, endpoint string) string {
	if market == "wb" {
		switch endpoint {
		case "/api/v2/upload/task":
			return "seller_prices_and_discounts"
		case "/api/v2/upload/task/size":
			return "size_prices"
		case "/api/v2/upload/task/club-discount":
			return "club_discounts"
		}
	}
	switch endpoint {
	case "/v1/product/import/prices":
		return "seller_prices"
	case "/v3/product/import":
		return "product_import_with_prices"
	case "/v1/actions/products/activate", "/v1/actions/hotsales/activate":
		return "promotion_activation"
	case "/v1/actions/products/deactivate", "/v1/actions/hotsales/deactivate":
		return "promotion_deactivation"
	}
	return "unknown_price_operation"
}

func legacyPricePublicItem(r sqlcgen.LegacyPriceAuthorizationRecord) domain.LegacyPriceJournalItem {
	status := "unresolved"
	if r.ResolutionID != nil {
		status = "resolved_manually"
	}
	return domain.LegacyPriceJournalItem{CommandID: r.CommandID, Marketplace: r.Marketplace, OperationKind: legacyPriceOperationKind(r.Marketplace, r.Endpoint),
		IssuedAt: r.IssuedAt.UTC(), AdmissionExpiresAt: r.ExpiresAt.UTC(), Status: status, HoldActive: r.ResolutionID == nil, ResolutionID: r.ResolutionID}
}

func legacyPriceContextFingerprint(c domain.PriceExecutionControlContext, r sqlcgen.LegacyPriceAuthorizationRecord) string {
	return adDigest([]any{"legacy_price_review_context_v1", c.ContextFingerprint, r.CommandID, r.AccountKey, r.Marketplace,
		r.Endpoint, r.RequestFingerprint, r.IssuedAt.UTC(), r.ExpiresAt.UTC()})
}

func legacyPriceReviewFingerprint(o domain.LegacyPriceReviewOperation) string {
	return adDigest([]any{"legacy_price_personal_review_v1", o.ID, o.WorkspaceID, o.ActorID, o.CabinetID, o.ExternalUserID, o.ExternalWorkspaceID,
		o.IntegrationID, o.IntegrationName, o.Marketplace, o.AccountKey, o.CredentialDigest, o.GrantFingerprint, o.Scope,
		o.Entry, o.Before, o.Proof, o.Reason, o.SupportReference, o.CreatedAt.UTC(), o.ExpiresAt.UTC()})
}

type legacyPriceCursor struct {
	Context string    `json:"context"`
	After   time.Time `json:"after"`
	ID      uuid.UUID `json:"id"`
}

func (s *ManagerLegacyPriceReviewService) Context(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration, resource, cursor string) (*domain.LegacyPriceJournalContext, error) {
	if s == nil || s.controls == nil {
		return nil, apperror.New(errManagerAdsUnavailable, "legacy price journal unavailable")
	}
	c, target, err := s.controls.context(ctx, identity, workspace, token, integration, resource, false)
	if err != nil {
		return nil, err
	}
	var after time.Time
	var afterID uuid.UUID
	if cursor != "" {
		var parsed legacyPriceCursor
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if len(cursor) > 512 || e != nil || json.Unmarshal(raw, &parsed) != nil || parsed.Context != c.ContextFingerprint || parsed.ID == uuid.Nil || parsed.After.IsZero() {
			return nil, apperror.New(apperror.ErrValidation, "current journal cursor required")
		}
		after, afterID = parsed.After, parsed.ID
	}
	rows, more, err := s.controls.q.LegacyPriceAuthorizationPage(ctx, target.accountKey, after, afterID)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "legacy price journal storage unavailable")
	}
	count, err := s.controls.q.LegacyPriceHeldCount(ctx, target.accountKey)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current legacy price holds unavailable")
	}
	available := s.enabled && priceControlCanManage(target.identity) && legacyReviewControlAllowed(c.Control)
	result := &domain.LegacyPriceJournalContext{Version: "legacy_price_journal_v1", WorkspaceID: c.WorkspaceID, IntegrationID: c.IntegrationID,
		IntegrationName: c.IntegrationName, Marketplace: c.Marketplace, Scope: legacyPriceReviewScope, AccountProof: c.AccountProof,
		Control: c.Control, Items: make([]domain.LegacyPriceJournalItem, 0, len(rows)), HeldCount: count, ApprovalAvailable: available}
	for _, row := range rows {
		entry := legacyPricePublicItem(row)
		entry.ReviewContextFingerprint = legacyPriceContextFingerprint(*c, row)
		entry.PersonalReviewAvailable = available && row.ResolutionID == nil && !row.ExpiresAt.After(time.Now()) && legacyPriceEndpoint(row.Marketplace, row.Endpoint)
		result.Items = append(result.Items, entry)
	}
	if more {
		last := rows[len(rows)-1]
		raw, _ := json.Marshal(legacyPriceCursor{c.ContextFingerprint, last.IssuedAt.UTC(), last.CommandID})
		next := base64.RawURLEncoding.EncodeToString(raw)
		result.NextCursor = &next
	}
	return result, nil
}

func (s *ManagerLegacyPriceReviewService) Prepare(ctx context.Context, identity domain.ManagerAccess, workspace, actor uuid.UUID, token, integration, resource string, command uuid.UUID, contextFingerprint, reason, support string) (*domain.LegacyPriceReviewOperation, error) {
	if s == nil || !s.enabled || s.controls == nil {
		return nil, apperror.New(errManagerAdsUnavailable, "new personal legacy price reviews disabled")
	}
	reason, support = strings.TrimSpace(reason), strings.TrimSpace(support)
	if actor == uuid.Nil || command == uuid.Nil {
		return nil, apperror.New(apperror.ErrForbidden, "exact personal actor and recorded admission required")
	}
	if len(contextFingerprint) != 64 || len([]rune(reason)) < 3 || len([]rune(reason)) > 500 || len([]rune(support)) < 12 || len([]rune(support)) > 1500 || strings.IndexFunc(reason+support, unicode.IsControl) >= 0 {
		return nil, apperror.New(apperror.ErrValidation, "current journal context, personal reason and platform/support investigation reference required")
	}
	c, target, err := s.controls.context(ctx, identity, workspace, token, integration, resource, true)
	if err != nil {
		return nil, err
	}
	if !legacyReviewControlAllowed(c.Control) {
		return nil, apperror.New(apperror.ErrConflict, "stop legacy dispatches before reviewing an old admission")
	}
	grant, err := s.controls.q.LegacyPriceAuthorization(ctx, target.accountKey, command)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "recorded admission not found in this native price account")
	}
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "recorded admission unavailable")
	}
	if grant.ResolutionID != nil || grant.ExpiresAt.After(time.Now()) || grant.Marketplace != c.Marketplace || !legacyPriceEndpoint(grant.Marketplace, grant.Endpoint) || legacyPriceContextFingerprint(*c, grant) != contextFingerprint {
		return nil, apperror.New(apperror.ErrConflict, "admission, current context or admission window changed")
	}
	entry := legacyPricePublicItem(grant)
	entry.ReviewContextFingerprint, entry.PersonalReviewAvailable = contextFingerprint, true
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := domain.LegacyPriceReviewOperation{ID: uuid.New(), WorkspaceID: workspace, ActorID: actor, CabinetID: target.cabinet.ID,
		ExternalUserID: target.identity.ExternalUserID, ExternalWorkspaceID: target.identity.ExternalWorkspaceID,
		IntegrationID: integration, IntegrationName: target.integration.Name, Marketplace: target.cabinet.Marketplace,
		AccountKey: target.accountKey, CredentialDigest: target.digest, GrantFingerprint: grant.RequestFingerprint,
		Scope: legacyPriceReviewScope, Entry: entry, Before: c.Control, Proof: c.AccountProof, Reason: reason, SupportReference: support,
		Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), ApprovalAvailable: true}
	o.Fingerprint = legacyPriceReviewFingerprint(o)
	if err = s.controls.q.CreateLegacyPriceReview(ctx, o); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "personal legacy price review could not be saved")
	}
	return s.receipt(ctx, o)
}

func legacyPriceReviewTargetMatches(o domain.LegacyPriceReviewOperation, t priceExecutionTarget) bool {
	return t.cabinet.ID == o.CabinetID && t.integration.ID == o.IntegrationID && t.integration.Name == o.IntegrationName &&
		t.cabinet.Marketplace == o.Marketplace && t.accountKey == o.AccountKey && t.digest == o.CredentialDigest &&
		t.identity.ExternalUserID == o.ExternalUserID && t.identity.ExternalWorkspaceID == o.ExternalWorkspaceID && t.resource == o.Proof.ResourceID
}

func (s *ManagerLegacyPriceReviewService) owned(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string, write bool) (domain.LegacyPriceReviewOperation, error) {
	var empty domain.LegacyPriceReviewOperation
	if s == nil || s.controls == nil {
		return empty, apperror.New(errManagerAdsUnavailable, "legacy price review unavailable")
	}
	fresh, err := s.controls.access(ctx, identity, token, write)
	if err != nil {
		return empty, err
	}
	o, err := s.controls.q.GetLegacyPriceReview(ctx, id, workspace, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, apperror.New(apperror.ErrNotFound, "personal legacy price review not found")
	}
	if err != nil {
		return empty, apperror.New(errManagerAdsUnavailable, "personal review receipt unavailable")
	}
	if o.ExternalUserID != fresh.ExternalUserID || o.ExternalWorkspaceID != fresh.ExternalWorkspaceID || o.WorkspaceID != workspace || o.ActorID != actor || o.Scope != legacyPriceReviewScope ||
		o.NativeOutcomeVerified || o.NativePricesChanged || legacyPriceReviewFingerprint(o) != o.Fingerprint {
		return empty, apperror.New(apperror.ErrConflict, "personal review scope or immutable snapshot changed")
	}
	if err = s.controls.q.ExpireLegacyPriceReview(ctx, o); err != nil {
		return empty, apperror.New(errManagerAdsUnavailable, "review expiration unavailable")
	}
	saved, err := s.controls.q.GetLegacyPriceReview(ctx, id, workspace, actor)
	if err == nil {
		saved.ApprovalAvailable = priceControlCanManage(fresh)
	}
	return saved, err
}

func (s *ManagerLegacyPriceReviewService) receipt(ctx context.Context, o domain.LegacyPriceReviewOperation) (*domain.LegacyPriceReviewOperation, error) {
	state, err := s.controls.q.PriceExecutionControl(ctx, o.AccountKey, o.Marketplace)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current account restriction unavailable; read saved review before another action")
	}
	grant, err := s.controls.q.LegacyPriceAuthorization(ctx, o.AccountKey, o.Entry.CommandID)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current recorded admission unavailable")
	}
	count, err := s.controls.q.LegacyPriceHeldCount(ctx, o.AccountKey)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current legacy holds unavailable")
	}
	held := grant.ResolutionID == nil
	now := time.Now().UTC().Truncate(time.Microsecond)
	o.CurrentControl, o.CurrentHoldActive, o.CurrentHeldCount, o.CurrentCheckedAt = &state, &held, &count, &now
	o.ApprovalAvailable = o.ApprovalAvailable && s.enabled && o.Status == "awaiting_confirmation" && o.ExpiresAt.After(now) && held &&
		state.Mode == o.Before.Mode && state.Version == o.Before.Version && legacyReviewControlAllowed(state) && !grant.ExpiresAt.After(now) &&
		grant.RequestFingerprint == o.GrantFingerprint && grant.IssuedAt.Equal(o.Entry.IssuedAt) && grant.ExpiresAt.Equal(o.Entry.AdmissionExpiresAt)
	return &o, nil
}

func (s *ManagerLegacyPriceReviewService) boundReceipt(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token string, o domain.LegacyPriceReviewOperation) (*domain.LegacyPriceReviewOperation, error) {
	target, err := s.controls.target(ctx, identity, workspace, token, o.IntegrationID, o.Proof.ResourceID, false)
	if err != nil {
		return nil, err
	}
	if target.accountKey != o.AccountKey || target.cabinet.ID != o.CabinetID || target.integration.ID != o.IntegrationID || target.cabinet.Marketplace != o.Marketplace {
		return nil, apperror.New(apperror.ErrForbidden, "current native account cannot read this review")
	}
	o.ApprovalAvailable = o.ApprovalAvailable && legacyPriceReviewTargetMatches(o, target)
	return s.receipt(ctx, o)
}

func (s *ManagerLegacyPriceReviewService) Receipt(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string) (*domain.LegacyPriceReviewOperation, error) {
	o, err := s.owned(ctx, identity, workspace, actor, id, token, false)
	if err != nil {
		return nil, err
	}
	return s.boundReceipt(ctx, identity, workspace, token, o)
}

func (s *ManagerLegacyPriceReviewService) Cancel(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string) (*domain.LegacyPriceReviewOperation, error) {
	o, err := s.owned(ctx, identity, workspace, actor, id, token, false)
	if err != nil {
		return nil, err
	}
	if fingerprint != o.Fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "exact saved review required")
	}
	if _, err = s.boundReceipt(ctx, identity, workspace, token, o); err != nil {
		return nil, err
	}
	if err = s.controls.q.CancelLegacyPriceReview(ctx, o); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "review cancellation unavailable")
	}
	return s.Receipt(ctx, identity, workspace, actor, id, token)
}

func (s *ManagerLegacyPriceReviewService) Approve(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string, checks map[string]bool) (*domain.LegacyPriceReviewOperation, error) {
	if s == nil || !s.enabled || s.controls == nil {
		return nil, apperror.New(errManagerAdsUnavailable, "new personal legacy price reviews disabled")
	}
	o, err := s.owned(ctx, identity, workspace, actor, id, token, true)
	if err != nil {
		return nil, err
	}
	if fingerprint != o.Fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "exact saved review required")
	}
	if o.Status != "awaiting_confirmation" {
		return s.boundReceipt(ctx, identity, workspace, token, o)
	}
	if len(checks) != len(legacyPriceReviewChecks) {
		return nil, apperror.New(apperror.ErrValidation, "four separate personal investigation checks required")
	}
	for _, key := range legacyPriceReviewChecks {
		if !checks[key] {
			return nil, apperror.New(apperror.ErrValidation, "four separate personal investigation checks required")
		}
	}
	c, target, err := s.controls.context(ctx, identity, workspace, token, o.IntegrationID, o.Proof.ResourceID, true)
	if err != nil {
		return nil, err
	}
	if !legacyPriceReviewTargetMatches(o, target) || c.Control.Mode != o.Before.Mode || c.Control.Version != o.Before.Version || c.AccountProof.ResourceLabel != o.Proof.ResourceLabel {
		return nil, apperror.New(apperror.ErrConflict, "native account proof or restriction changed")
	}
	err = s.controls.q.ApplyLegacyPriceReview(ctx, o, checks, func(locked context.Context) error {
		fresh, e := s.controls.target(locked, identity, workspace, token, o.IntegrationID, o.Proof.ResourceID, true)
		if e != nil {
			return e
		}
		if !legacyPriceReviewTargetMatches(o, fresh) {
			return apperror.New(apperror.ErrConflict, "native account or credentials changed during personal review")
		}
		_, e = s.controls.access(locked, fresh.identity, token, true)
		return e
	})
	if err != nil {
		return nil, err
	}
	persisted, err := s.controls.q.GetLegacyPriceReview(ctx, id, workspace, actor)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "committed review receipt unavailable; read the saved decision before another action")
	}
	return s.receipt(ctx, persisted)
}
