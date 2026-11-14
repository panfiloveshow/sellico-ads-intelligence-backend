package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type managerAdAuthority interface {
	ManagerIntegrationVerifier
	VerifyManagerAccess(context.Context, string, string) (*domain.ManagerAccess, error)
}
type ManagerAdOperationsService struct {
	q       *sqlcgen.Queries
	crm     managerAdAuthority
	context *ManagerAdContextService
	enabled bool
}

func NewManagerAdOperationsService(q *sqlcgen.Queries, crm managerAdAuthority, reader *ManagerAdContextService, enabled bool) *ManagerAdOperationsService {
	return &ManagerAdOperationsService{q, crm, reader, enabled}
}

type managerAdTransport struct {
	readLaunch func(context.Context) (*domain.NativeAdLaunchContext, error)
	request    adguard.Request
	digest     string
	read       func(context.Context) (*domain.NativeAdCampaign, error)
	send       func(context.Context) error
	readBid    func(context.Context) (*domain.NativeAdBidContext, error)
	readBudget func(context.Context) (*domain.NativeAdBudgetContext, error)
}

func adDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func adOperationFingerprint(o domain.ManagerAdOperation) string {
	values := []any{"manager_ad_pause_v1", o.ID, o.WorkspaceID, o.ActorID, o.ExternalUserID, o.ExternalWorkspaceID, o.IntegrationID, o.IntegrationName, o.CabinetID, o.CampaignID, o.ExternalID, o.Marketplace, o.Action, o.Snapshot, o.TargetStatus, o.Source, o.Request, o.CredentialDigest, o.CreatedAt.UTC(), o.ExpiresAt.UTC()}
	if o.Action != "pause" || o.PreviousOperationID != nil {
		values[0] = "manager_ad_control_v2"
		values = append(values, o.PreviousOperationID, o.PreviousFingerprint)
	}
	if o.BidPlan != nil {
		values[0] = "manager_ad_product_bid_v1"
		values = append(values, o.BidPlan)
	}
	if o.BudgetPlan != nil {
		values[0] = "manager_ad_campaign_budget_v1"
		plan := *o.BudgetPlan
		// Today's spend is a moving observation, not a mutable approved limit.
		// It is displayed with the proposal time but cannot authorize spending.
		plan.Before.SpentTodayRub = nil
		values = append(values, &plan)
	}
	if o.LaunchPlan != nil {
		values[0] = "manager_ad_launch_v1"
		values = append(values, domain.StableAdLaunchSettings(o.LaunchPlan.Settings))
	}
	return adDigest(values)
}
func (s *ManagerAdOperationsService) access(ctx context.Context, identity domain.ManagerAccess, token string, write bool) (domain.ManagerAccess, error) {
	if s == nil || s.q == nil || s.crm == nil || s.context == nil {
		return domain.ManagerAccess{}, apperror.New(errManagerAdsUnavailable, "advertising operations unavailable")
	}
	fresh, err := s.crm.VerifyManagerAccess(ctx, token, identity.ExternalWorkspaceID)
	if err != nil {
		return domain.ManagerAccess{}, err
	}
	if fresh == nil || fresh.ExternalUserID != identity.ExternalUserID || fresh.ExternalWorkspaceID != identity.ExternalWorkspaceID || !fresh.Allows("marketing.view") || !fresh.Allows("integrations.view") || (write && (!fresh.PersonalAccountVerified || !fresh.Allows("marketing.manage"))) {
		return domain.ManagerAccess{}, apperror.New(apperror.ErrForbidden, "fresh personal advertising access required")
	}
	return *fresh, nil
}
func (s *ManagerAdOperationsService) transport(target *managerAdTarget, action string) (managerAdTransport, error) {
	var result managerAdTransport
	if action == "launch" {
		return s.launchTransport(target)
	}
	if target.cabinet.Marketplace == "wb" {
		if s.context.wb == nil {
			return result, errors.New("WB transport unavailable")
		}
		token, err := crypto.Decrypt(target.credentials.EncryptedToken, s.context.key)
		if err != nil || token == "" {
			return result, errors.New("WB identity unavailable")
		}
		if action == "resume" {
			result.request, err = s.context.wb.AdvertisingResumeRequest(token, target.externalID)
		} else {
			result.request, err = s.context.wb.AdvertisingPauseRequest(token, target.externalID)
		}
		if err != nil {
			return result, err
		}
		result.digest = adDigest(token)
		result.read = func(ctx context.Context) (*domain.NativeAdCampaign, error) {
			if action == "resume" || action == "handover" {
				return s.context.wb.ReadCampaignControlContext(ctx, token, target.externalID)
			}
			return s.context.wb.ReadCampaignContext(ctx, token, target.externalID)
		}
		if action == "resume" {
			result.send = func(ctx context.Context) error { return s.context.wb.StartCampaign(ctx, token, target.externalID) }
		} else if action == "pause" {
			result.send = func(ctx context.Context) error { return s.context.wb.PauseCampaign(ctx, token, target.externalID) }
		}
		return result, nil
	}
	if target.cabinet.Marketplace != "ozon" || s.context.ozon == nil {
		return result, errors.New("advertising transport unavailable")
	}
	creds, err := decryptOzonCredentialsBlob(target.credentials.EncryptedCredentials, s.context.key)
	if err != nil || !creds.HasPerformanceAPI() {
		return result, errors.New("Ozon Performance identity unavailable")
	}
	c := ozonClientCreds(creds)
	if action == "resume" {
		result.request, err = s.context.ozon.AdvertisingResumeRequest(c, target.externalID)
	} else {
		result.request, err = s.context.ozon.AdvertisingPauseRequest(c, target.externalID)
	}
	if err != nil {
		return result, err
	}
	result.digest = adDigest([]string{c.PerfClientID, c.PerfClientSecret})
	result.read = func(ctx context.Context) (*domain.NativeAdCampaign, error) {
		return s.context.ozon.ReadCampaignContext(ctx, c, target.externalID)
	}
	if action == "resume" {
		result.send = func(ctx context.Context) error { return s.context.ozon.ActivateCampaign(ctx, c, target.externalID) }
	} else if action == "pause" {
		result.send = func(ctx context.Context) error { return s.context.ozon.DeactivateCampaign(ctx, c, target.externalID) }
	}
	return result, nil
}
func pauseTarget(market string, n domain.NativeAdCampaign) (string, error) {
	if market == "wb" && n.Status == "9" {
		return "11", nil
	}
	if market == "ozon" && n.Status == "CAMPAIGN_STATE_RUNNING" && n.CampaignType != nil && *n.CampaignType == "SKU" {
		return "CAMPAIGN_STATE_INACTIVE", nil
	}
	return "", apperror.New(apperror.ErrValidation, "pause requires an active WB campaign or an active SKU Ozon campaign; global CPO and unknown types require a separate action")
}
func positiveAdAmount(v *string) bool {
	if v == nil {
		return false
	}
	amount, ok := new(big.Rat).SetString(*v)
	return ok && amount.Sign() > 0
}
func managerAdTargetStatus(market, action string, n domain.NativeAdCampaign) (string, error) {
	if action == "launch" {
		if market == "wb" && (n.Status == "4" || n.Status == "11") && positiveAdAmount(n.RemainingBudgetRub) {
			return "9", nil
		}
		if market == "ozon" && n.Status == "CAMPAIGN_STATE_INACTIVE" && n.CampaignType != nil && *n.CampaignType == "SKU" && (positiveAdAmount(n.DailyBudgetRub) != positiveAdAmount(n.WeeklyBudgetRub)) {
			return "CAMPAIGN_STATE_RUNNING", nil
		}
		return "", apperror.New(apperror.ErrValidation, "launch requires a configured inactive campaign and a positive current budget")
	}
	if action == "pause" {
		return pauseTarget(market, n)
	}
	valid := market == "wb" && (n.Status == "9" || n.Status == "11") || market == "ozon" && n.CampaignType != nil && *n.CampaignType == "SKU" && (n.Status == "CAMPAIGN_STATE_RUNNING" || n.Status == "CAMPAIGN_STATE_INACTIVE")
	if !valid {
		return "", apperror.New(apperror.ErrValidation, "exact managed WB or SKU Ozon state required")
	}
	knownBudget := market == "wb" && n.RemainingBudgetRub != nil || market == "ozon" && (n.DailyBudgetRub != nil || n.WeeklyBudgetRub != nil)
	if !knownBudget {
		return "", apperror.New(apperror.ErrConflict, "current campaign budget is not confirmed")
	}
	if action == "handover" {
		return n.Status, nil
	}
	if action == "resume" && (market == "wb" && n.Status == "11" && positiveAdAmount(n.RemainingBudgetRub) || market == "ozon" && n.Status == "CAMPAIGN_STATE_INACTIVE" && (positiveAdAmount(n.DailyBudgetRub) || positiveAdAmount(n.WeeklyBudgetRub))) {
		if market == "wb" {
			return "9", nil
		}
		return "CAMPAIGN_STATE_RUNNING", nil
	}
	return "", apperror.New(apperror.ErrValidation, "resume requires a managed pause and a positive current budget; initial launches and global CPO are separate actions")
}
func (s *ManagerAdOperationsService) Prepare(ctx context.Context, identity domain.ManagerAccess, workspace, actor uuid.UUID, token, integration string, campaign uuid.UUID, action string) (*domain.ManagerAdOperation, error) {
	if workspace == uuid.Nil || actor == uuid.Nil || (action != "pause" && action != "resume" && action != "handover" && action != "launch") {
		return nil, apperror.New(apperror.ErrValidation, "specific advertising control action required")
	}
	fresh, err := s.access(ctx, identity, token, true)
	if err != nil {
		return nil, err
	}
	target, err := s.context.resolveTarget(ctx, fresh, workspace, token, integration, campaign)
	if err != nil {
		return nil, err
	}
	native, err := s.transport(target, action)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "native advertising identity unavailable")
	}
	var snapshot *domain.NativeAdCampaign
	var launch *domain.NativeAdLaunchContext
	if action == "launch" {
		launch, err = native.readLaunch(ctx)
		if err == nil {
			err = validateLaunchSettings(target.cabinet.Marketplace, launch)
		}
		if launch != nil {
			snapshot = &launch.Campaign
		}
	} else {
		snapshot, err = native.read(ctx)
	}
	if err != nil || snapshot == nil || snapshot.ExternalID != target.campaign.ExternalID {
		return nil, apperror.New(errManagerAdsUnavailable, "native advertising snapshot unavailable")
	}
	state, err := managerAdTargetStatus(target.cabinet.Marketplace, action, *snapshot)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := "WB Advertising API"
	if target.cabinet.Marketplace == "ozon" {
		source = "Ozon Performance API"
	}
	o := domain.ManagerAdOperation{ID: uuid.New(), WorkspaceID: workspace, ActorID: actor, ExternalUserID: fresh.ExternalUserID, ExternalWorkspaceID: fresh.ExternalWorkspaceID,
		IntegrationID: integration, IntegrationName: target.integration.Name, CabinetID: target.cabinet.ID, CampaignID: campaign, ExternalID: target.campaign.ExternalID, Marketplace: target.cabinet.Marketplace,
		OperationType: "ad_change", Action: action, Snapshot: *snapshot, TargetStatus: state, Source: source, Request: native.request, CredentialDigest: native.digest, Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if launch != nil {
		o.LaunchPlan = &domain.ManagerAdLaunchPlan{Settings: launch.Settings}
	}
	previous, err := s.q.CurrentManagerAdControl(ctx, native.request)
	if err != nil {
		return nil, apperror.New(apperror.ErrConflict, "campaign has unresolved or nontransferable advertising control")
	}
	if previous != nil {
		if previous.Fingerprint != adOperationFingerprint(*previous) {
			return nil, apperror.New(apperror.ErrConflict, "previous advertising control differs")
		}
		o.PreviousOperationID = &previous.ID
		o.PreviousFingerprint = previous.Fingerprint
	}
	if (action == "resume" || action == "handover") && previous == nil {
		return nil, apperror.New(apperror.ErrConflict, "resume or handover requires an observed managed control")
	}
	if err = s.q.ValidateManagerAdControl(ctx, o); err != nil {
		return nil, apperror.New(apperror.ErrConflict, "managed campaign control changed")
	}
	o.Fingerprint = adOperationFingerprint(o)
	if err = s.q.CreateManagerAdOperation(ctx, o); err != nil {
		return nil, err
	}
	return s.view(o), nil
}
func (s *ManagerAdOperationsService) view(o domain.ManagerAdOperation) *domain.ManagerAdOperation {
	o.ApprovalAvailable = s.enabled && o.Status == "awaiting_confirmation" && o.ExpiresAt.After(time.Now())
	o.ExecutionAllowed = false
	return &o
}
func (s *ManagerAdOperationsService) owned(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID) (domain.ManagerAdOperation, error) {
	if workspace == uuid.Nil || actor == uuid.Nil || id == uuid.Nil {
		return domain.ManagerAdOperation{}, apperror.New(apperror.ErrNotFound, "advertising proposal unavailable")
	}
	o, err := s.q.GetManagerAdOperation(ctx, id, workspace, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, apperror.New(apperror.ErrNotFound, "owned advertising proposal not found")
	}
	if err != nil {
		return o, err
	}
	if o.ExternalUserID != identity.ExternalUserID || o.ExternalWorkspaceID != identity.ExternalWorkspaceID || o.Fingerprint != adOperationFingerprint(o) {
		return o, apperror.New(apperror.ErrForbidden, "advertising proposal scope differs")
	}
	return o, nil
}
func validateAdChecks(action string, checks map[string]bool) error {
	if action == "set_bid" {
		if len(checks) != 3 || !checks["bid_target_checked"] || !checks["bid_spend_checked"] || !checks["vendor_limits_checked"] {
			return apperror.New(apperror.ErrValidation, "personally check the exact bid, spending constraints and vendor limits")
		}
		return nil
	}
	if action == "launch" {
		if len(checks) != 3 || !checks["campaign_checked"] || !checks["launch_settings_checked"] || !checks["launch_spend_checked"] {
			return apperror.New(apperror.ErrValidation, "three personal launch checks required")
		}
		return nil
	}
	impact := map[string]string{"pause": "pause_impact_checked", "resume": "resume_spend_checked", "handover": "handover_impact_checked"}[action]
	if impact == "" || len(checks) != 2 || !checks["campaign_checked"] || !checks[impact] {
		return apperror.New(apperror.ErrValidation, "personally confirm the exact campaign and the specific control/spending impact")
	}
	return nil
}
func (s *ManagerAdOperationsService) validateCurrent(ctx context.Context, o domain.ManagerAdOperation, fresh domain.ManagerAccess, token string) (managerAdTransport, error) {
	if o.Action == "launch" {
		return s.validateCurrentLaunch(ctx, o, fresh, token)
	}
	if o.Action == "set_budget" {
		return s.validateCurrentBudget(ctx, o, fresh, token)
	}
	if o.Action == "set_bid" {
		return s.validateCurrentBid(ctx, o, fresh, token)
	}
	target, err := s.context.resolveTarget(ctx, fresh, o.WorkspaceID, token, o.IntegrationID, o.CampaignID)
	if err != nil {
		return managerAdTransport{}, err
	}
	native, err := s.transport(target, o.Action)
	if err != nil {
		return native, err
	}
	snapshot, err := native.read(ctx)
	if err != nil || snapshot == nil {
		return native, errors.New("current advertising snapshot unavailable")
	}
	state, err := managerAdTargetStatus(target.cabinet.Marketplace, o.Action, *snapshot)
	if err != nil {
		return native, err
	}
	candidate := o
	candidate.Snapshot = *snapshot
	candidate.Request = native.request
	candidate.CredentialDigest = native.digest
	candidate.IntegrationName = target.integration.Name
	candidate.CabinetID = target.cabinet.ID
	candidate.ExternalID = target.campaign.ExternalID
	candidate.TargetStatus = state
	candidate.Marketplace = target.cabinet.Marketplace
	if adOperationFingerprint(candidate) != o.Fingerprint {
		return native, apperror.New(apperror.ErrConflict, "advertising source changed; prepare a new proposal")
	}
	if err = s.q.ValidateManagerAdControl(ctx, o); err != nil {
		return native, apperror.New(apperror.ErrConflict, "managed campaign control changed")
	}
	return native, nil
}
func (s *ManagerAdOperationsService) Approve(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string, checks map[string]bool) (*domain.ManagerAdOperation, error) {
	fresh, err := s.access(ctx, identity, token, true)
	if err != nil {
		return nil, err
	}
	if !s.enabled {
		return nil, apperror.New(apperror.ErrConflict, "advertising execution stopped")
	}
	o, err := s.owned(ctx, fresh, workspace, actor, id)
	if err != nil {
		return nil, err
	}
	if o.Fingerprint != fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "advertising approval fingerprint differs")
	}
	if o.Action == "set_budget" {
		err = validateBudgetChecks(o, checks)
	} else {
		err = validateAdChecks(o.Action, checks)
	}
	if err != nil {
		return nil, err
	}
	if o.Status != "awaiting_confirmation" {
		return s.Receipt(ctx, fresh, workspace, actor, id, token)
	}
	native, err := s.validateCurrent(ctx, o, fresh, token)
	if err != nil {
		return nil, err
	}
	if err = s.q.ClaimManagerAdOperation(ctx, o); err != nil {
		return nil, apperror.New(apperror.ErrConflict, "advertising proposal expired or already claimed")
	}
	if o.Action == "handover" {
		// The old hold remains in place during the final personal and native checks.
		access, checkErr := s.access(ctx, fresh, token, true)
		if checkErr == nil {
			_, checkErr = s.validateCurrent(ctx, o, access, token)
		}
		if checkErr == nil {
			checkErr = s.q.HandoverManagerAdControl(ctx, o, o.Snapshot, time.Now().UTC())
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if checkErr != nil {
			_ = s.q.RejectManagerAdBeforeSend(persist, o, "preflight_changed")
		}
		return s.afterAttempt(persist, fresh, workspace, actor, id, token)
	}
	if err = s.q.ReserveManagerAdCommand(ctx, o); err != nil {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.q.RejectManagerAdBeforeSend(persist, o, "reservation_unavailable")
		return s.afterAttempt(persist, fresh, workspace, actor, id, token)
	}
	gate := func(gateCtx context.Context, r adguard.Request) error {
		if r.Fingerprint != o.Request.Fingerprint {
			return errors.New("native advertising request differs")
		}
		current, err := s.owned(gateCtx, fresh, workspace, actor, id)
		if err != nil {
			return err
		}
		if current.Status != "executing" || !current.ExpiresAt.After(time.Now()) || !s.enabled {
			return errors.New("advertising approval no longer executable")
		}
		access, err := s.access(gateCtx, fresh, token, true)
		if err != nil {
			return err
		}
		_, err = s.validateCurrent(gateCtx, o, access, token)
		return err
	}
	err = native.send(adguard.WithApprovedCommandGate(ctx, id, gate))
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	command, receiptErr := s.q.ReadNativeAdCommand(persist, o.ID)
	if err != nil && receiptErr == nil && command.Status == "approved" && !command.StartedAt.Valid {
		if rejectErr := s.q.RejectManagerAdBeforeSend(persist, o, "preflight_changed"); rejectErr != nil {
			_ = s.q.SetManagerAdOperationPending(persist, o, "unknown", "receipt_unavailable")
		}
	} else if receiptErr == nil && command.Status == "rejected" {
		_ = s.q.SetManagerAdOperationPending(persist, o, "failed", "vendor_rejected")
	} else if err == nil && receiptErr == nil && command.Status == "acknowledged" {
		_ = s.q.SetManagerAdOperationPending(persist, o, "verifying", "")
	} else {
		_ = s.q.SetManagerAdOperationPending(persist, o, "unknown", "receipt_unavailable")
	}
	return s.afterAttempt(persist, fresh, workspace, actor, id, token)
}

// Once claimed, a later CRM/observation failure cannot become an HTTP rejection
// that the chat mistakes for "nothing sent". Return the owned durable state.
func (s *ManagerAdOperationsService) afterAttempt(ctx context.Context, access domain.ManagerAccess, workspace, actor, id uuid.UUID, token string) (*domain.ManagerAdOperation, error) {
	view, err := s.Receipt(ctx, access, workspace, actor, id, token)
	if err == nil {
		return view, nil
	}
	view, err = s.reload(ctx, access, workspace, actor, id)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising receipt unavailable; do not repeat the command")
	}
	return view, nil
}
func (s *ManagerAdOperationsService) Cancel(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string) (*domain.ManagerAdOperation, error) {
	fresh, err := s.access(ctx, identity, token, false)
	if err != nil {
		return nil, err
	}
	o, err := s.owned(ctx, fresh, workspace, actor, id)
	if err != nil {
		return nil, err
	}
	if o.Fingerprint != fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "advertising proposal differs")
	}
	if err = s.q.CancelManagerAdOperation(ctx, o); err != nil {
		return nil, err
	}
	return s.Receipt(ctx, fresh, workspace, actor, id, token)
}
func (s *ManagerAdOperationsService) Receipt(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string) (*domain.ManagerAdOperation, error) {
	fresh, err := s.access(ctx, identity, token, false)
	if err != nil {
		return nil, err
	}
	o, err := s.owned(ctx, fresh, workspace, actor, id)
	if err != nil {
		return nil, err
	}
	if err = s.q.ExpireManagerAdOperation(ctx, o); err != nil {
		return nil, err
	}
	o, err = s.owned(ctx, fresh, workspace, actor, id)
	if err != nil {
		return nil, err
	}
	if o.Status != "executing" && o.Status != "verifying" && o.Status != "unknown" {
		return s.view(o), nil
	}
	if o.Action == "handover" {
		return s.view(o), nil
	} // observation cannot release control
	command, err := s.q.ReadNativeAdCommand(ctx, o.ID)
	if err != nil {
		return s.view(o), nil
	}
	if command.Status == "rejected" {
		_ = s.q.SetManagerAdOperationPending(ctx, o, "failed", "vendor_rejected")
		return s.reload(ctx, fresh, workspace, actor, id)
	}
	if command.Status != "acknowledged" && command.Status != "unknown" && command.Status != "observed" {
		return s.view(o), nil
	}
	target, err := s.context.resolveTarget(ctx, fresh, workspace, token, o.IntegrationID, o.CampaignID)
	if err != nil {
		return s.view(o), nil
	}
	var native managerAdTransport
	if o.Action == "set_bid" {
		native, err = s.bidTransport(target, o.BidPlan)
	} else if o.Action == "set_budget" {
		native, err = s.budgetTransport(target, o.BudgetPlan)
	} else {
		native, err = s.transport(target, o.Action)
	}
	if err != nil || native.request.Fingerprint != o.Request.Fingerprint || target.cabinet.ID != o.CabinetID {
		return s.view(o), nil
	}
	if o.Action == "launch" {
		observed, readErr := native.readLaunch(ctx)
		if readErr != nil || observed == nil || observed.Campaign.Status != o.TargetStatus || !sameBudgetString(observed.Campaign.CampaignType, o.Snapshot.CampaignType) || !sameBudgetString(observed.Campaign.PaymentType, o.Snapshot.PaymentType) || !sameBudgetString(observed.Campaign.BidType, o.Snapshot.BidType) || o.LaunchPlan == nil || !domain.SameAdLaunchSettings(o.LaunchPlan.Settings, observed.Settings) {
			return s.view(o), nil
		}
		if err = s.q.ObserveManagerAdLaunch(ctx, o, *observed, time.Now().UTC()); err != nil {
			return s.view(o), nil
		}
		return s.reload(ctx, fresh, workspace, actor, id)
	}
	if o.Action == "set_budget" {
		observed, readErr := native.readBudget(ctx)
		if readErr != nil || observed == nil || !managerBudgetObserved(o, *observed) {
			return s.view(o), nil
		}
		if err = s.q.ObserveManagerAdBudget(ctx, o, *observed, time.Now().UTC()); err != nil {
			return s.view(o), nil
		}
		return s.reload(ctx, fresh, workspace, actor, id)
	}
	if o.Action == "set_bid" {
		observed, readErr := native.readBid(ctx)
		if readErr != nil || observed == nil || observed.Campaign.Status != o.TargetStatus || !managerBidObserved(o.BidPlan, *observed) {
			return s.view(o), nil
		}
		if err = s.q.ObserveManagerAdProductBid(ctx, o, *observed, time.Now().UTC()); err != nil {
			return s.view(o), nil
		}
		return s.reload(ctx, fresh, workspace, actor, id)
	}
	observed, err := native.read(ctx)
	if err != nil || observed == nil || observed.ExternalID != o.ExternalID || observed.Status != o.TargetStatus {
		return s.view(o), nil
	}
	if err = s.q.ObserveManagerAdState(ctx, o, *observed, time.Now().UTC()); err != nil {
		return s.view(o), nil
	}
	return s.reload(ctx, fresh, workspace, actor, id)
}
func (s *ManagerAdOperationsService) reload(ctx context.Context, access domain.ManagerAccess, workspace, actor, id uuid.UUID) (*domain.ManagerAdOperation, error) {
	o, err := s.owned(ctx, access, workspace, actor, id)
	if err != nil {
		return nil, err
	}
	return s.view(o), nil
}
