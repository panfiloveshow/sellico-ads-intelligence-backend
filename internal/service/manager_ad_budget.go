package service

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

func canonicalBudgetTarget(market, period string, target domain.ManagerAdBudgetTarget) (domain.ManagerAdBudgetTarget, error) {
	if period != "daily" && period != "weekly" || market == "wb" && period != "daily" {
		return target, apperror.New(apperror.ErrValidation, "specific supported budget period required")
	}
	if !target.Enabled {
		if target.AmountRub != nil || target.CarryOverEnabled != nil {
			return target, apperror.New(apperror.ErrValidation, "removing a limit must not contain a hidden amount or carry flag")
		}
		return target, nil
	}
	if target.AmountRub == nil || market == "wb" && target.CarryOverEnabled == nil || market == "ozon" && target.CarryOverEnabled != nil {
		return target, apperror.New(apperror.ErrValidation, "explicit budget amount and applicable carry-over choice required")
	}
	canonical, _, err := managerBidAmount(*target.AmountRub, false)
	if err != nil {
		return target, err
	}
	if parts := strings.Split(canonical, "."); len(parts) == 2 && len(parts[1]) > 2 {
		return target, apperror.New(apperror.ErrValidation, "budget target must be an exact whole-kopeck amount")
	}
	target.AmountRub = &canonical
	return target, nil
}

func managerBudgetConstraints(before domain.NativeAdBudget, target domain.ManagerAdBudgetTarget, caps *domain.ManagerAdBudgetConstraints) (*domain.ManagerAdBudgetConstraints, error) {
	if before.AmountRub == nil || before.Currency != "RUB" {
		return nil, apperror.New(apperror.ErrConflict, "current exact budget and currency required")
	}
	if !target.Enabled {
		if caps != nil || !before.Enabled {
			return nil, apperror.New(apperror.ErrValidation, "limit already disabled or meaningless numeric caps supplied for unlimited spending")
		}
		return nil, nil
	}
	if caps == nil || target.AmountRub == nil {
		return nil, apperror.New(apperror.ErrValidation, "two explicit personal budget caps required")
	}
	_, amount, err := managerBidAmount(*target.AmountRub, false)
	if err != nil {
		return nil, err
	}
	_, old, err := managerBidAmount(*before.AmountRub, true)
	if err != nil {
		return nil, err
	}
	canonicalMax, maximum, err := managerBidAmount(caps.MaxBudgetRub, false)
	if err != nil {
		return nil, err
	}
	canonicalDelta, delta, err := managerBidAmount(caps.MaxAbsoluteChangeRub, true)
	if err != nil {
		return nil, err
	}
	change := new(big.Rat).Abs(new(big.Rat).Sub(amount, old))
	if amount.Cmp(maximum) > 0 || change.Cmp(delta) > 0 {
		return nil, apperror.New(apperror.ErrValidation, "budget exceeds a personal nominal limit or absolute change cap")
	}
	if before.MinimumAmountRub != nil {
		_, minimum, err := managerBidAmount(*before.MinimumAmountRub, false)
		if err != nil || amount.Cmp(minimum) < 0 {
			return nil, apperror.New(apperror.ErrValidation, "budget below the confirmed mandatory vendor minimum")
		}
	}
	if before.MaximumAmountRub != nil {
		_, maximum, err := managerBidAmount(*before.MaximumAmountRub, false)
		if err != nil || amount.Cmp(maximum) > 0 {
			return nil, apperror.New(apperror.ErrValidation, "budget above the confirmed vendor maximum")
		}
	}
	carryEqual := before.CarryOverEnabled == nil && target.CarryOverEnabled == nil || before.CarryOverEnabled != nil && target.CarryOverEnabled != nil && *before.CarryOverEnabled == *target.CarryOverEnabled
	if before.Enabled && amount.Cmp(old) == 0 && carryEqual {
		return nil, apperror.New(apperror.ErrValidation, "budget settings are unchanged")
	}
	return &domain.ManagerAdBudgetConstraints{MaxBudgetRub: canonicalMax, MaxAbsoluteChangeRub: canonicalDelta}, nil
}

func (s *ManagerAdOperationsService) budgetTransport(target *managerAdTarget, plan *domain.ManagerAdBudgetPlan) (managerAdTransport, error) {
	var result managerAdTransport
	if plan == nil {
		return result, errors.New("immutable budget plan required")
	}
	period := plan.Before.Period
	if target.cabinet.Marketplace == "wb" {
		if s.context.wb == nil {
			return result, errors.New("WB budget transport unavailable")
		}
		token, err := crypto.Decrypt(target.credentials.EncryptedToken, s.context.key)
		if err != nil || token == "" {
			return result, errors.New("WB identity unavailable")
		}
		result.request, err = s.context.wb.AdvertisingBudgetRequest(token, target.externalID, period, plan.Target.Enabled, plan.Target.AmountRub, plan.Target.CarryOverEnabled)
		if err != nil {
			return result, err
		}
		result.digest = adDigest(token)
		result.readBudget = func(ctx context.Context) (*domain.NativeAdBudgetContext, error) {
			return s.context.wb.ReadManagerCampaignBudget(ctx, token, target.externalID, period)
		}
		result.send = func(ctx context.Context) error {
			return s.context.wb.SetExactCampaignBudget(ctx, token, target.externalID, period, plan.Target.Enabled, plan.Target.AmountRub, plan.Target.CarryOverEnabled)
		}
		return result, nil
	}
	if target.cabinet.Marketplace != "ozon" || s.context.ozon == nil {
		return result, errors.New("Ozon budget transport unavailable")
	}
	creds, err := decryptOzonCredentialsBlob(target.credentials.EncryptedCredentials, s.context.key)
	if err != nil || !creds.HasPerformanceAPI() {
		return result, errors.New("Ozon Performance identity unavailable")
	}
	c := ozonClientCreds(creds)
	amount := "0"
	if plan.Target.Enabled {
		if plan.Target.AmountRub == nil {
			return result, errors.New("explicit Ozon budget amount required")
		}
		amount = *plan.Target.AmountRub
	}
	result.request, err = s.context.ozon.AdvertisingBudgetRequest(c, target.externalID, period, amount)
	if err != nil {
		return result, err
	}
	result.digest = adDigest([]string{c.PerfClientID, c.PerfClientSecret})
	result.readBudget = func(ctx context.Context) (*domain.NativeAdBudgetContext, error) {
		return s.context.ozon.ReadManagerCampaignBudget(ctx, c, target.externalID, period)
	}
	result.send = func(ctx context.Context) error {
		return s.context.ozon.SetExactCampaignBudget(ctx, c, target.externalID, period, amount)
	}
	return result, nil
}

// A disabled Ozon budget does not expose its original period. Only a verified
// native observation inside this exact uninterrupted management chain can
// establish it; neither a model guess nor the caller's desired period can.
func (s *ManagerAdOperationsService) budgetPeriodEvidence(ctx context.Context, o domain.ManagerAdOperation, period string) (*domain.ManagerAdBudgetPeriodEvidence, error) {
	next, expected := o.PreviousOperationID, o.PreviousFingerprint
	seen := map[uuid.UUID]bool{o.ID: true}
	for depth := 0; next != nil && depth < 64; depth++ {
		if seen[*next] {
			return nil, errors.New("budget control history cycle")
		}
		seen[*next] = true
		previous, err := s.q.GetManagerAdControlPredecessor(ctx, o, *next)
		if err != nil || previous.Fingerprint != expected || previous.Fingerprint != adOperationFingerprint(previous) || previous.Result == nil || !previous.Result.FieldsVerified || previous.Action == "handover" || previous.Result.ControlReleased {
			return nil, errors.New("budget period history cannot be verified")
		}
		if previous.Action == "set_budget" && previous.BudgetPlan != nil && previous.BudgetPlan.Before.PeriodConfirmed && previous.BudgetPlan.Before.Period == period && previous.Result.BudgetVerified && previous.Result.Budget != nil && previous.Result.Budget.Period == period {
			return &domain.ManagerAdBudgetPeriodEvidence{OperationID: previous.ID, Fingerprint: previous.Fingerprint, Period: period}, nil
		}
		next, expected = previous.PreviousOperationID, previous.PreviousFingerprint
	}
	return nil, errors.New("budget period has no confirmed source in the current control history")
}

func (s *ManagerAdOperationsService) PrepareBudget(ctx context.Context, identity domain.ManagerAccess, workspace, actor uuid.UUID, token, integration string, campaign uuid.UUID, period string, targetBudget domain.ManagerAdBudgetTarget, caps *domain.ManagerAdBudgetConstraints) (*domain.ManagerAdOperation, error) {
	if workspace == uuid.Nil || actor == uuid.Nil {
		return nil, apperror.New(apperror.ErrValidation, "specific personal budget scope required")
	}
	fresh, err := s.access(ctx, identity, token, true)
	if err != nil {
		return nil, err
	}
	target, err := s.context.resolveTarget(ctx, fresh, workspace, token, integration, campaign)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalBudgetTarget(target.cabinet.Marketplace, period, targetBudget)
	if err != nil {
		return nil, err
	}
	plan := &domain.ManagerAdBudgetPlan{Before: domain.NativeAdBudget{Period: period}, Target: canonical}
	native, err := s.budgetTransport(target, plan)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "budget precision, target or native identity unavailable")
	}
	snapshot, err := native.readBudget(ctx)
	if err != nil || snapshot == nil || snapshot.Campaign.ExternalID != target.campaign.ExternalID || snapshot.Budget.Period != period {
		return nil, apperror.New(errManagerAdsUnavailable, "current native budget unavailable")
	}
	if target.cabinet.Marketplace == "wb" && snapshot.Campaign.Status != "9" && snapshot.Campaign.Status != "11" {
		return nil, apperror.New(apperror.ErrValidation, "initial campaign configuration requires a separate action")
	}
	plan.Before = snapshot.Budget
	plan.Constraints, err = managerBudgetConstraints(plan.Before, canonical, caps)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := "WB Advertising API"
	if target.cabinet.Marketplace == "ozon" {
		source = "Ozon Performance API"
	}
	o := domain.ManagerAdOperation{ID: uuid.New(), WorkspaceID: workspace, ActorID: actor, ExternalUserID: fresh.ExternalUserID, ExternalWorkspaceID: fresh.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: target.integration.Name, CabinetID: target.cabinet.ID, CampaignID: campaign, ExternalID: target.campaign.ExternalID, Marketplace: target.cabinet.Marketplace, OperationType: "ad_change", Action: "set_budget", Snapshot: snapshot.Campaign, TargetStatus: snapshot.Campaign.Status, Source: source, Request: native.request, CredentialDigest: native.digest, BudgetPlan: plan, Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	previous, err := s.q.CurrentManagerAdControl(ctx, native.request)
	if err != nil {
		return nil, apperror.New(apperror.ErrConflict, "campaign has unresolved or nontransferable budget control")
	}
	if previous != nil {
		if previous.Fingerprint != adOperationFingerprint(*previous) {
			return nil, apperror.New(apperror.ErrConflict, "previous control differs")
		}
		o.PreviousOperationID = &previous.ID
		o.PreviousFingerprint = previous.Fingerprint
	}
	if !plan.Before.PeriodConfirmed {
		if o.Marketplace != "ozon" || plan.Before.AmountRub == nil || *plan.Before.AmountRub != "0" || positiveAdAmount(snapshot.Campaign.DailyBudgetRub) || positiveAdAmount(snapshot.Campaign.WeeklyBudgetRub) {
			return nil, apperror.New(apperror.ErrConflict, "current budget period is unknown")
		}
		plan.PeriodEvidence, err = s.budgetPeriodEvidence(ctx, o, period)
		if err != nil {
			return nil, apperror.New(apperror.ErrConflict, "original budget period has no verified current or historical source")
		}
	}
	if err = s.q.ValidateManagerAdControl(ctx, o); err != nil {
		return nil, apperror.New(apperror.ErrConflict, "managed budget control changed")
	}
	o.Fingerprint = adOperationFingerprint(o)
	if err = s.q.CreateManagerAdOperation(ctx, o); err != nil {
		return nil, err
	}
	return s.view(o), nil
}

func validateBudgetChecks(o domain.ManagerAdOperation, checks map[string]bool) error {
	if o.BudgetPlan == nil {
		return apperror.New(apperror.ErrConflict, "immutable budget plan unavailable")
	}
	keys := []string{"budget_target_checked", "budget_spend_checked", "vendor_limits_checked"}
	if !o.BudgetPlan.Target.Enabled {
		keys = append(keys, "unlimited_spend_checked")
	} else if o.BudgetPlan.Target.CarryOverEnabled != nil && *o.BudgetPlan.Target.CarryOverEnabled && (!o.BudgetPlan.Before.Enabled || o.BudgetPlan.Before.CarryOverEnabled == nil || !*o.BudgetPlan.Before.CarryOverEnabled) {
		keys = append(keys, "carry_over_spend_checked")
	}
	if len(checks) != len(keys) {
		return apperror.New(apperror.ErrValidation, "personally confirm the budget target and its exact spending impact")
	}
	for _, key := range keys {
		if !checks[key] {
			return apperror.New(apperror.ErrValidation, "all applicable personal budget confirmations are required")
		}
	}
	return nil
}

func (s *ManagerAdOperationsService) validateCurrentBudget(ctx context.Context, o domain.ManagerAdOperation, fresh domain.ManagerAccess, token string) (managerAdTransport, error) {
	var native managerAdTransport
	if o.BudgetPlan == nil {
		return native, errors.New("immutable budget plan unavailable")
	}
	target, err := s.context.resolveTarget(ctx, fresh, o.WorkspaceID, token, o.IntegrationID, o.CampaignID)
	if err != nil {
		return native, err
	}
	native, err = s.budgetTransport(target, o.BudgetPlan)
	if err != nil {
		return native, err
	}
	snapshot, err := native.readBudget(ctx)
	if err != nil || snapshot == nil || snapshot.Campaign.ExternalID != o.ExternalID {
		return native, errors.New("current native budget unavailable")
	}
	plan := *o.BudgetPlan
	plan.Before = snapshot.Budget
	plan.Constraints, err = managerBudgetConstraints(plan.Before, plan.Target, plan.Constraints)
	if err != nil {
		return native, err
	}
	plan.PeriodEvidence = nil
	if !plan.Before.PeriodConfirmed {
		if o.Marketplace != "ozon" || plan.Before.AmountRub == nil || *plan.Before.AmountRub != "0" || positiveAdAmount(snapshot.Campaign.DailyBudgetRub) || positiveAdAmount(snapshot.Campaign.WeeklyBudgetRub) {
			return native, errors.New("budget period unknown")
		}
		plan.PeriodEvidence, err = s.budgetPeriodEvidence(ctx, o, plan.Before.Period)
		if err != nil {
			return native, err
		}
	}
	candidate := o
	candidate.BudgetPlan = &plan
	candidate.Snapshot = snapshot.Campaign
	candidate.TargetStatus = snapshot.Campaign.Status
	candidate.Request = native.request
	candidate.CredentialDigest = native.digest
	candidate.CabinetID = target.cabinet.ID
	candidate.IntegrationName = target.integration.Name
	candidate.ExternalID = target.campaign.ExternalID
	candidate.Marketplace = target.cabinet.Marketplace
	if adOperationFingerprint(candidate) != o.Fingerprint {
		return native, apperror.New(apperror.ErrConflict, "budget settings, caps, type evidence, keys or rights changed; prepare again")
	}
	if err = s.q.ValidateManagerAdControl(ctx, o); err != nil {
		return native, apperror.New(apperror.ErrConflict, "managed budget control changed")
	}
	return native, nil
}

func sameBudgetString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func managerBudgetObserved(o domain.ManagerAdOperation, native domain.NativeAdBudgetContext) bool {
	plan := o.BudgetPlan
	if plan == nil || native.Campaign.ExternalID != o.ExternalID || native.Campaign.Status != o.TargetStatus || native.Budget.Period != plan.Before.Period || native.Budget.Currency != "RUB" || native.Budget.Enabled != plan.Target.Enabled || !sameBudgetString(native.Campaign.CampaignType, o.Snapshot.CampaignType) || !sameBudgetString(native.Campaign.PaymentType, o.Snapshot.PaymentType) {
		return false
	}
	if plan.Target.Enabled && !sameBudgetString(native.Budget.AmountRub, plan.Target.AmountRub) {
		return false
	}
	if plan.Target.CarryOverEnabled != nil && (native.Budget.CarryOverEnabled == nil || *native.Budget.CarryOverEnabled != *plan.Target.CarryOverEnabled) {
		return false
	}
	if o.Marketplace == "ozon" {
		if plan.Before.Period == "weekly" {
			return sameBudgetString(native.Campaign.DailyBudgetRub, o.Snapshot.DailyBudgetRub)
		}
		return sameBudgetString(native.Campaign.WeeklyBudgetRub, o.Snapshot.WeeklyBudgetRub)
	}
	return true
}
