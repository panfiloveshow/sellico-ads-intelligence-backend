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

var managerAdManualResolutionChecks = []string{
	"ad_cabinet_state_checked",
	"ad_no_pending_mutation",
	"ad_late_effect_risk_accepted",
	"ad_hold_release_understood",
}

type ManagerAdAdvancedService struct {
	q       *sqlcgen.Queries
	ads     *ManagerAdsService
	crm     managerAdAuthority
	enabled bool
}

func NewManagerAdAdvancedService(q *sqlcgen.Queries, crm managerAdAuthority, enabled bool) *ManagerAdAdvancedService {
	return &ManagerAdAdvancedService{
		q:       q,
		ads:     NewManagerAdsService(q, crm),
		crm:     crm,
		enabled: enabled,
	}
}

func normalizeManagerAdMarketplace(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "wb", "wildberries":
		return "wb", true
	case "ozon":
		return "ozon", true
	default:
		return "", false
	}
}

func classifyManagerAdOperationType(action string) string {
	switch action {
	case "set_bid":
		return "ad_bid_change"
	case "set_budget":
		return "ad_budget_change"
	case "launch":
		return "ad_launch"
	default:
		return "ad_change"
	}
}

func adManualResolutionFingerprint(r domain.ManagerAdManualResolution) string {
	return adDigest([]any{
		"manager_ad_manual_resolution_v1",
		r.ID,
		r.WorkspaceID,
		r.ActorID,
		r.CabinetID,
		r.ExternalUserID,
		r.ExternalWorkspaceID,
		r.OperationID,
		r.OperationType,
		r.IntegrationID,
		r.IntegrationName,
		r.Marketplace,
		r.CampaignID,
		r.ExternalID,
		r.Reason,
		r.SupportReference,
		r.CreatedAt.UTC(),
		r.ExpiresAt.UTC(),
	})
}

func (s *ManagerAdAdvancedService) Attribution(
	ctx context.Context,
	identity domain.ManagerAccess,
	workspaceID uuid.UUID,
	token, integrationID string,
	from, to time.Time,
	campaignID *uuid.UUID,
) (*domain.ManagerAdAttributionReport, error) {
	if s == nil || s.q == nil || s.ads == nil {
		return nil, apperror.New(errManagerAdsUnavailable, "manager ad attribution is not configured")
	}
	days := int(to.Sub(from).Hours()/24) + 1
	if days < 1 || days > 90 {
		return nil, apperror.New(apperror.ErrValidation, "attribution period must be between 1 and 90 days")
	}
	integration, cabinet, err := s.ads.resolveCabinet(ctx, identity, workspaceID, token, integrationID)
	if err != nil {
		return nil, err
	}
	if campaignID != nil {
		found, err := s.q.ManagerAdsCampaignByID(ctx, *cabinet, *campaignID)
		if err != nil {
			return nil, apperror.New(errManagerAdsUnavailable, "campaign mapping unavailable")
		}
		if found == nil {
			return nil, apperror.New(apperror.ErrNotFound, "campaign not found in selected store")
		}
	}
	report, err := s.q.ManagerAdsAttribution(ctx, *cabinet, from, to, campaignID)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "manager ad attribution statistics unavailable")
	}
	report.WorkspaceID = identity.ExternalWorkspaceID
	report.IntegrationID = integrationID
	report.IntegrationName = integration.Name
	return report, nil
}

func (s *ManagerAdAdvancedService) access(ctx context.Context, identity domain.ManagerAccess, token string, write bool) (domain.ManagerAccess, error) {
	if s == nil || s.q == nil || s.crm == nil || s.ads == nil {
		return domain.ManagerAccess{}, apperror.New(errManagerAdsUnavailable, "advertising manual resolution unavailable")
	}
	fresh, err := s.crm.VerifyManagerAccess(ctx, token, identity.ExternalWorkspaceID)
	if err != nil {
		return domain.ManagerAccess{}, err
	}
	if fresh == nil || fresh.ExternalUserID != identity.ExternalUserID || fresh.ExternalWorkspaceID != identity.ExternalWorkspaceID ||
		!fresh.Allows("marketing.view") || !fresh.Allows("integrations.view") ||
		(write && (!fresh.PersonalAccountVerified || !fresh.Allows("marketing.manage"))) {
		return domain.ManagerAccess{}, apperror.New(apperror.ErrForbidden, "fresh personal advertising access required")
	}
	return *fresh, nil
}

func (s *ManagerAdAdvancedService) withHoldState(ctx context.Context, r domain.ManagerAdManualResolution, canManage bool) (*domain.ManagerAdManualResolution, error) {
	held, err := s.q.ManagerAdOperationHoldActive(ctx, r.OperationID)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising hold state unavailable")
	}
	r.ControlHoldActive = held
	r.NativeOutcomeVerified = false
	r.NativeCampaignsChanged = false
	r.ApprovalAvailable = s.enabled && canManage && r.Status == "awaiting_confirmation" && r.ExpiresAt.After(time.Now())
	return &r, nil
}

func (s *ManagerAdAdvancedService) PrepareResolution(
	ctx context.Context,
	identity domain.ManagerAccess,
	workspace, actor uuid.UUID,
	token string,
	operationID uuid.UUID,
	integrationID, marketplaceRaw string,
	campaignID uuid.UUID,
	reason, supportRef string,
) (*domain.ManagerAdManualResolution, error) {
	if s == nil || !s.enabled {
		return nil, apperror.New(apperror.ErrConflict, "advertising manual resolution is disabled")
	}
	if workspace == uuid.Nil || actor == uuid.Nil || operationID == uuid.Nil || campaignID == uuid.Nil {
		return nil, apperror.New(apperror.ErrValidation, "exact workspace, actor, campaign and operation IDs required")
	}
	market, ok := normalizeManagerAdMarketplace(marketplaceRaw)
	if !ok {
		return nil, apperror.New(apperror.ErrValidation, "supported marketplace (wildberries or ozon) required")
	}
	reason = strings.TrimSpace(reason)
	supportRef = strings.TrimSpace(supportRef)
	reasonLen := len([]rune(reason))
	supportLen := len([]rune(supportRef))
	if reasonLen < 10 || reasonLen > 2000 || supportLen < 12 || supportLen > 256 || strings.IndexFunc(reason+supportRef, unicode.IsControl) >= 0 {
		return nil, apperror.New(apperror.ErrValidation, "personal reason (10..2000 chars) and cabinet/support reference (12..256 chars) required")
	}

	fresh, err := s.access(ctx, identity, token, true)
	if err != nil {
		return nil, err
	}
	integration, cabinet, err := s.ads.resolveCabinet(ctx, fresh, workspace, token, integrationID)
	if err != nil {
		return nil, err
	}
	if cabinet.Marketplace != market {
		return nil, apperror.New(apperror.ErrValidation, "marketplace does not match selected store")
	}

	op, err := s.q.GetWorkspaceManagerAdOperation(ctx, operationID, workspace)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "advertising operation not found in workspace")
	}
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising operation unavailable")
	}
	if op.ExternalWorkspaceID != fresh.ExternalWorkspaceID || op.IntegrationID != integrationID ||
		op.CabinetID != cabinet.ID || op.CampaignID != campaignID || op.Marketplace != market {
		return nil, apperror.New(apperror.ErrConflict, "advertising operation scope does not match campaign or store")
	}
	if op.Status != "unknown" {
		return nil, apperror.New(apperror.ErrConflict, "manual resolution applies only to operations in unknown status")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	res := domain.ManagerAdManualResolution{
		ID:                     uuid.New(),
		WorkspaceID:            workspace,
		ActorID:                actor,
		CabinetID:              cabinet.ID,
		ExternalUserID:         fresh.ExternalUserID,
		ExternalWorkspaceID:    fresh.ExternalWorkspaceID,
		Status:                 "awaiting_confirmation",
		OperationID:            op.ID,
		OperationType:          classifyManagerAdOperationType(op.Action),
		IntegrationID:          integrationID,
		IntegrationName:        integration.Name,
		Marketplace:            market,
		CampaignID:             op.CampaignID,
		ExternalID:             op.ExternalID,
		Reason:                 reason,
		SupportReference:       supportRef,
		CreatedAt:              now,
		ExpiresAt:              now.Add(5 * time.Minute),
		NativeOutcomeVerified:  false,
		NativeCampaignsChanged: false,
	}
	res.Fingerprint = adManualResolutionFingerprint(res)
	if err := s.q.CreateManagerAdManualResolution(ctx, res); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising manual resolution could not be saved")
	}
	return s.withHoldState(ctx, res, true)
}

func (s *ManagerAdAdvancedService) ownedResolution(
	ctx context.Context,
	identity domain.ManagerAccess,
	workspace, actor, operationID, resolutionID uuid.UUID,
	token string,
	write bool,
) (domain.ManagerAdManualResolution, domain.ManagerAccess, error) {
	var empty domain.ManagerAdManualResolution
	fresh, err := s.access(ctx, identity, token, write)
	if err != nil {
		return empty, fresh, err
	}
	r, err := s.q.GetManagerAdManualResolution(ctx, resolutionID, operationID, workspace, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, fresh, apperror.New(apperror.ErrNotFound, "owned advertising manual resolution not found")
	}
	if err != nil {
		return empty, fresh, apperror.New(errManagerAdsUnavailable, "advertising manual resolution unavailable")
	}
	if r.ExternalUserID != fresh.ExternalUserID || r.ExternalWorkspaceID != fresh.ExternalWorkspaceID ||
		r.NativeOutcomeVerified || r.NativeCampaignsChanged || adManualResolutionFingerprint(r) != r.Fingerprint {
		return empty, fresh, apperror.New(apperror.ErrForbidden, "advertising manual resolution scope differs")
	}
	if err := s.q.ExpireManagerAdManualResolution(ctx, r); err != nil {
		return empty, fresh, apperror.New(errManagerAdsUnavailable, "resolution expiration unavailable")
	}
	saved, err := s.q.GetManagerAdManualResolution(ctx, resolutionID, operationID, workspace, actor)
	if err != nil {
		return empty, fresh, apperror.New(errManagerAdsUnavailable, "advertising manual resolution unavailable")
	}
	return saved, fresh, nil
}

func (s *ManagerAdAdvancedService) ResolutionReceipt(
	ctx context.Context,
	identity domain.ManagerAccess,
	workspace, actor, operationID, resolutionID uuid.UUID,
	token string,
) (*domain.ManagerAdManualResolution, error) {
	r, fresh, err := s.ownedResolution(ctx, identity, workspace, actor, operationID, resolutionID, token, false)
	if err != nil {
		return nil, err
	}
	if _, _, err := s.ads.resolveCabinet(ctx, fresh, workspace, token, r.IntegrationID); err != nil {
		return nil, err
	}
	return s.withHoldState(ctx, r, fresh.PersonalAccountVerified && fresh.Allows("marketing.manage"))
}

func (s *ManagerAdAdvancedService) CancelResolution(
	ctx context.Context,
	identity domain.ManagerAccess,
	workspace, actor, operationID, resolutionID uuid.UUID,
	token, fingerprint string,
) (*domain.ManagerAdManualResolution, error) {
	r, fresh, err := s.ownedResolution(ctx, identity, workspace, actor, operationID, resolutionID, token, false)
	if err != nil {
		return nil, err
	}
	if r.Fingerprint != fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "exact resolution fingerprint required")
	}
	if _, _, err := s.ads.resolveCabinet(ctx, fresh, workspace, token, r.IntegrationID); err != nil {
		return nil, err
	}
	if err := s.q.CancelManagerAdManualResolution(ctx, r); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "resolution cancellation unavailable")
	}
	return s.ResolutionReceipt(ctx, fresh, workspace, actor, operationID, resolutionID, token)
}

func (s *ManagerAdAdvancedService) ApproveResolution(
	ctx context.Context,
	identity domain.ManagerAccess,
	workspace, actor, operationID, resolutionID uuid.UUID,
	token, fingerprint string,
	checks map[string]bool,
) (*domain.ManagerAdManualResolution, error) {
	if s == nil || !s.enabled {
		return nil, apperror.New(apperror.ErrConflict, "advertising manual resolution is disabled")
	}
	r, fresh, err := s.ownedResolution(ctx, identity, workspace, actor, operationID, resolutionID, token, true)
	if err != nil {
		return nil, err
	}
	if r.Fingerprint != fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "exact resolution fingerprint required")
	}
	if len(checks) != len(managerAdManualResolutionChecks) {
		return nil, apperror.New(apperror.ErrValidation, "four personal manual resolution confirmations required")
	}
	for _, k := range managerAdManualResolutionChecks {
		if !checks[k] {
			return nil, apperror.New(apperror.ErrValidation, "four personal manual resolution confirmations required")
		}
	}
	if r.Status != "awaiting_confirmation" {
		return s.withHoldState(ctx, r, true)
	}

	err = s.q.ApplyManagerAdManualResolution(ctx, r, checks, func(locked context.Context) error {
		rechecked, e := s.access(locked, fresh, token, true)
		if e != nil {
			return e
		}
		_, cab, e := s.ads.resolveCabinet(locked, rechecked, workspace, token, r.IntegrationID)
		if e != nil {
			return e
		}
		if cab.ID != r.CabinetID || cab.Marketplace != r.Marketplace {
			return apperror.New(apperror.ErrConflict, "advertising cabinet mapping changed")
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sqlcgen.ErrManagerAdResolutionChanged) || errors.Is(err, sqlcgen.ErrAdResourceHeld) {
			return nil, apperror.New(apperror.ErrConflict, "resolution or target operation state changed")
		}
		return nil, err
	}
	saved, err := s.q.GetManagerAdManualResolution(ctx, resolutionID, operationID, workspace, actor)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "committed resolution receipt unavailable; read the saved resolution before retrying")
	}
	return s.withHoldState(ctx, saved, true)
}
