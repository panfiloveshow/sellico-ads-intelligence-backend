package service

import (
	"context"
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

const advertisingAccountControlScope = "configured_advertising_account"

var adExecutionChecks = []string{"advertising_account_scope_checked", "advertising_service_effect_checked", "advertising_reopening_checked"}

type ManagerAdExecutionControlService struct {
	q       *sqlcgen.Queries
	ads     *ManagerAdOperationsService
	enabled bool
}

func NewManagerAdExecutionControlService(q *sqlcgen.Queries, crm managerAdAuthority, reader *ManagerAdContextService, enabled bool) *ManagerAdExecutionControlService {
	return &ManagerAdExecutionControlService{q: q, ads: NewManagerAdOperationsService(q, crm, reader, false), enabled: enabled}
}

type executionControlTarget struct {
	identity  domain.ManagerAccess
	target    *managerAdTarget
	transport managerAdTransport
}

func (s *ManagerAdExecutionControlService) target(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration string, campaign uuid.UUID, write bool) (executionControlTarget, error) {
	var result executionControlTarget
	if s == nil || s.q == nil || s.ads == nil {
		return result, apperror.New(errManagerAdsUnavailable, "advertising execution control unavailable")
	}
	fresh, err := s.ads.access(ctx, identity, token, write)
	if err != nil {
		return result, err
	}
	target, err := s.ads.context.resolveTarget(ctx, fresh, workspace, token, integration, campaign)
	if err != nil {
		return result, err
	}
	transport, err := s.ads.transport(target, "pause")
	if err != nil {
		return result, apperror.New(errManagerAdsUnavailable, "advertising account identity unavailable")
	}
	if transport.request.AccountKey == "" || transport.digest == "" || len(target.integration.Name) > 512 || strings.TrimSpace(target.integration.Name) == "" {
		return result, apperror.New(errManagerAdsUnavailable, "advertising account scope could not be confirmed")
	}
	return executionControlTarget{fresh, target, transport}, nil
}
func sameExecutionControlTarget(a, b executionControlTarget) bool {
	return a.target.cabinet.ID == b.target.cabinet.ID && a.target.campaign.ID == b.target.campaign.ID && a.target.integration.ID == b.target.integration.ID && a.target.integration.Name == b.target.integration.Name && a.target.cabinet.Marketplace == b.target.cabinet.Marketplace && a.transport.request.AccountKey == b.transport.request.AccountKey && a.transport.digest == b.transport.digest && a.identity.ExternalUserID == b.identity.ExternalUserID && a.identity.ExternalWorkspaceID == b.identity.ExternalWorkspaceID
}
func boundedControlName(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
	r := []rune(strings.TrimSpace(v))
	if len(r) > 280 {
		r = r[:280]
	}
	return string(r)
}
func controlContextFingerprint(c domain.AdExecutionControlContext, t executionControlTarget) string {
	// Retrieval time is observation metadata, not a new control version.
	return adDigest([]any{"ad_execution_context_v1", t.identity.ExternalUserID, t.identity.ExternalWorkspaceID, t.target.cabinet.ID, t.transport.request.AccountKey, t.transport.digest, c.IntegrationID, c.IntegrationName, c.Marketplace, c.Scope, c.Control, c.AccountProof.CampaignID, c.AccountProof.ExternalID, c.AccountProof.CampaignName, c.AccountProof.Source})
}
func controlOperationFingerprint(o domain.AdExecutionControlOperation) string {
	return adDigest([]any{"ad_execution_control_v1", o.ID, o.WorkspaceID, o.ActorID, o.ExternalUserID, o.ExternalWorkspaceID, o.IntegrationID, o.IntegrationName, o.CabinetID, o.Marketplace, o.AccountKey, o.CredentialDigest, o.Scope, o.TargetMode, o.Before, o.Proof, o.Reason, o.CreatedAt.UTC(), o.ExpiresAt.UTC()})
}
func (s *ManagerAdExecutionControlService) context(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration string, campaign uuid.UUID, write bool) (*domain.AdExecutionControlContext, executionControlTarget, error) {
	target, err := s.target(ctx, identity, workspace, token, integration, campaign, write)
	if err != nil {
		return nil, target, err
	}
	native, err := target.transport.read(ctx)
	if err != nil || native == nil || native.ExternalID != target.target.campaign.ExternalID {
		return nil, target, apperror.New(errManagerAdsUnavailable, "current native account proof unavailable")
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	after, err := s.target(ctx, target.identity, workspace, token, integration, campaign, write)
	if err != nil {
		return nil, target, err
	}
	if !sameExecutionControlTarget(target, after) {
		return nil, target, apperror.New(apperror.ErrConflict, "advertising account changed during verification")
	}
	state, err := s.q.AdExecutionControl(ctx, target.transport.request.AccountKey, target.target.cabinet.Marketplace)
	if err != nil {
		return nil, target, apperror.New(errManagerAdsUnavailable, "advertising execution control could not be read")
	}
	source := "WB Advertising API"
	if target.target.cabinet.Marketplace == "ozon" {
		source = "Ozon Performance API"
	}
	result := &domain.AdExecutionControlContext{WorkspaceID: target.identity.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: target.target.integration.Name, Marketplace: target.target.cabinet.Marketplace, Scope: advertisingAccountControlScope, AccountProof: domain.AdExecutionAccountProof{CampaignID: campaign, ExternalID: native.ExternalID, CampaignName: boundedControlName(native.Name), Source: source, ObservedAt: observed}, Control: state, ApprovalAvailable: s.enabled && target.identity.PersonalAccountVerified && target.identity.Allows("marketing.manage")}
	result.ContextFingerprint = controlContextFingerprint(*result, target)
	return result, target, nil
}
func (s *ManagerAdExecutionControlService) Context(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration string, campaign uuid.UUID) (*domain.AdExecutionControlContext, error) {
	result, _, err := s.context(ctx, identity, workspace, token, integration, campaign, false)
	return result, err
}
func (s *ManagerAdExecutionControlService) Prepare(ctx context.Context, identity domain.ManagerAccess, workspace, actor uuid.UUID, token, integration string, campaign uuid.UUID, targetMode, reason, contextFingerprint string) (*domain.AdExecutionControlOperation, error) {
	if s == nil || !s.enabled {
		return nil, apperror.New(errManagerAdsUnavailable, "new advertising execution controls disabled")
	}
	if actor == uuid.Nil {
		return nil, apperror.New(apperror.ErrForbidden, "verified local actor required")
	}
	reason = strings.TrimSpace(reason)
	if (targetMode != "stopped" && targetMode != "manual_only") || len([]rune(reason)) < 3 || len([]rune(reason)) > 500 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || len(contextFingerprint) != 64 {
		return nil, apperror.New(apperror.ErrValidation, "exact target mode, reason and current account context required")
	}
	current, target, err := s.context(ctx, identity, workspace, token, integration, campaign, true)
	if err != nil {
		return nil, err
	}
	if current.ContextFingerprint != contextFingerprint {
		return nil, apperror.New(apperror.ErrConflict, "advertising account or execution control changed")
	}
	if current.Control.Mode == targetMode {
		return nil, apperror.New(apperror.ErrConflict, "advertising execution control already has this mode")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := domain.AdExecutionControlOperation{ID: uuid.New(), WorkspaceID: workspace, ActorID: actor, ExternalUserID: target.identity.ExternalUserID, ExternalWorkspaceID: target.identity.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: current.IntegrationName, CabinetID: target.target.cabinet.ID, Marketplace: current.Marketplace, AccountKey: target.transport.request.AccountKey, CredentialDigest: target.transport.digest, Scope: advertisingAccountControlScope, TargetMode: targetMode, Before: current.Control, Proof: current.AccountProof, Reason: reason, Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	o.Fingerprint = controlOperationFingerprint(o)
	if err = s.q.CreateAdExecutionControlOperation(ctx, o); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising execution control proposal could not be saved")
	}
	return s.receipt(ctx, o)
}
func (s *ManagerAdExecutionControlService) owned(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string, write bool) (domain.AdExecutionControlOperation, error) {
	var empty domain.AdExecutionControlOperation
	if s == nil || s.q == nil || s.ads == nil {
		return empty, apperror.New(errManagerAdsUnavailable, "advertising execution control unavailable")
	}
	fresh, err := s.ads.access(ctx, identity, token, write)
	if err != nil {
		return empty, err
	}
	o, err := s.q.GetAdExecutionControlOperation(ctx, id, workspace, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, apperror.New(apperror.ErrNotFound, "personal control proposal not found")
	}
	if err != nil {
		return empty, apperror.New(errManagerAdsUnavailable, "control proposal could not be verified")
	}
	if o.ExternalWorkspaceID != fresh.ExternalWorkspaceID || o.ExternalUserID != fresh.ExternalUserID || o.WorkspaceID != workspace || o.ActorID != actor || o.Scope != advertisingAccountControlScope || o.NativeCampaignsChanged || controlOperationFingerprint(o) != o.Fingerprint {
		return empty, apperror.New(apperror.ErrConflict, "control proposal does not match personal scope")
	}
	if err = s.q.ExpireAdExecutionControlOperation(ctx, o); err != nil {
		return empty, apperror.New(errManagerAdsUnavailable, "control proposal expiry unavailable")
	}
	return s.q.GetAdExecutionControlOperation(ctx, id, workspace, actor)
}
func (s *ManagerAdExecutionControlService) receipt(ctx context.Context, o domain.AdExecutionControlOperation) (*domain.AdExecutionControlOperation, error) {
	current, err := s.q.AdExecutionControl(ctx, o.AccountKey, o.Marketplace)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current execution control unavailable")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	o.CurrentControl = &current
	o.CurrentCheckedAt = &now
	o.ApprovalAvailable = s.enabled && o.Status == "awaiting_confirmation" && o.ExpiresAt.After(now) && current.Version == o.Before.Version && current.Mode == o.Before.Mode
	return &o, nil
}
func (s *ManagerAdExecutionControlService) Receipt(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string) (*domain.AdExecutionControlOperation, error) {
	o, err := s.owned(ctx, identity, workspace, actor, id, token, false)
	if err != nil {
		return nil, err
	}
	return s.receipt(ctx, o)
}
func (s *ManagerAdExecutionControlService) Cancel(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string) (*domain.AdExecutionControlOperation, error) {
	o, err := s.owned(ctx, identity, workspace, actor, id, token, false)
	if err != nil {
		return nil, err
	}
	if fingerprint != o.Fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "control proposal changed")
	}
	if err = s.q.CancelAdExecutionControlOperation(ctx, o); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "control cancellation unavailable")
	}
	return s.Receipt(ctx, identity, workspace, actor, id, token)
}
func (s *ManagerAdExecutionControlService) Approve(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string, checks map[string]bool) (*domain.AdExecutionControlOperation, error) {
	if s == nil || !s.enabled {
		return nil, apperror.New(errManagerAdsUnavailable, "new advertising execution controls disabled")
	}
	if actor == uuid.Nil {
		return nil, apperror.New(apperror.ErrForbidden, "verified local actor required")
	}
	o, err := s.owned(ctx, identity, workspace, actor, id, token, true)
	if err != nil {
		return nil, err
	}
	if fingerprint != o.Fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "control proposal changed")
	}
	if o.Status != "awaiting_confirmation" {
		return s.receipt(ctx, o)
	}
	if len(checks) != len(adExecutionChecks) {
		return nil, apperror.New(apperror.ErrValidation, "three separate personal execution-control checks required")
	}
	for _, key := range adExecutionChecks {
		if !checks[key] {
			return nil, apperror.New(apperror.ErrValidation, "three separate personal execution-control checks required")
		}
	}
	err = s.q.ApplyAdExecutionControl(ctx, o, func(locked context.Context) error {
		current, err := s.target(locked, identity, workspace, token, o.IntegrationID, o.Proof.CampaignID, true)
		if err != nil {
			return err
		}
		if current.target.cabinet.ID != o.CabinetID || current.target.integration.Name != o.IntegrationName || current.target.cabinet.Marketplace != o.Marketplace || current.transport.request.AccountKey != o.AccountKey || current.transport.digest != o.CredentialDigest || current.identity.ExternalUserID != o.ExternalUserID || current.target.campaign.ExternalID != o.Proof.ExternalID {
			return apperror.New(apperror.ErrConflict, "advertising account or credential binding changed")
		}
		// Recheck permissions after the store/credential lookup while the native
		// account lock still prevents a concurrent dispatch marker.
		_, err = s.ads.access(locked, current.identity, token, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	persisted, readErr := s.q.GetAdExecutionControlOperation(ctx, id, workspace, actor)
	if readErr != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "committed control receipt unavailable; read saved decision before another action")
	}
	return s.receipt(ctx, persisted)
}
