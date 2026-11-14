package service

import (
	"context"
	"errors"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

func (s *ManagerAdOperationsService) launchTransport(target *managerAdTarget) (managerAdTransport, error) {
	native, err := s.transport(target, "resume")
	if err != nil {
		return native, err
	}
	if target.cabinet.Marketplace == "wb" {
		token, err := crypto.Decrypt(target.credentials.EncryptedToken, s.context.key)
		if err != nil {
			return native, err
		}
		native.readLaunch = func(ctx context.Context) (*domain.NativeAdLaunchContext, error) {
			return s.context.wb.ReadManagerCampaignLaunch(ctx, token, target.externalID)
		}
	} else {
		creds, err := decryptOzonCredentialsBlob(target.credentials.EncryptedCredentials, s.context.key)
		if err != nil {
			return native, err
		}
		c := ozonClientCreds(creds)
		native.readLaunch = func(ctx context.Context) (*domain.NativeAdLaunchContext, error) {
			return s.context.ozon.ReadManagerCampaignLaunch(ctx, c, target.externalID)
		}
	}
	return native, nil
}
func validateLaunchSettings(market string, n *domain.NativeAdLaunchContext) error {
	if n == nil || len(n.Settings.Products) == 0 || len(n.Settings.Placements) == 0 || n.Settings.EligibilityVerified || n.Settings.VendorLimitsVerified {
		return errors.New("explicit partial launch settings required")
	}
	if market == "wb" {
		if n.Settings.Strategy != "manual" || (n.Settings.PaymentType != "cpc" && n.Settings.PaymentType != "cpm") || !positiveAdAmount(n.Campaign.RemainingBudgetRub) {
			return errors.New("funded configured manual WB campaign required")
		}
		if n.Settings.PaymentType == "cpc" {
			b := n.Settings.Budget
			if b == nil || b.Period != "daily" || !b.PeriodConfirmed || !b.Enabled || b.Currency != "RUB" || b.CarryOverEnabled == nil || b.MinimumAmountRub == nil || !positiveAdAmount(b.AmountRub) {
				return errors.New("explicit enabled daily CPC limit required")
			}
			_, v, e := managerBidAmount(*b.AmountRub, false)
			_, min, e2 := managerBidAmount(*b.MinimumAmountRub, false)
			if e != nil || e2 != nil || v.Cmp(min) < 0 {
				return errors.New("daily limit below mandatory WB minimum")
			}
		}
	} else if market != "ozon" || n.Settings.Strategy != "TARGET_BIDS" || n.Settings.PaymentType != "CPC" || positiveAdAmount(n.Campaign.DailyBudgetRub) == positiveAdAmount(n.Campaign.WeeklyBudgetRub) {
		return errors.New("one confirmed positive Ozon period budget required")
	}
	return nil
}
func (s *ManagerAdOperationsService) validateCurrentLaunch(ctx context.Context, o domain.ManagerAdOperation, fresh domain.ManagerAccess, token string) (managerAdTransport, error) {
	var native managerAdTransport
	if o.LaunchPlan == nil {
		return native, errors.New("immutable launch settings unavailable")
	}
	target, err := s.context.resolveTarget(ctx, fresh, o.WorkspaceID, token, o.IntegrationID, o.CampaignID)
	if err != nil {
		return native, err
	}
	native, err = s.launchTransport(target)
	if err != nil {
		return native, err
	}
	n, err := native.readLaunch(ctx)
	if err != nil {
		return native, err
	}
	if err = validateLaunchSettings(target.cabinet.Marketplace, n); err != nil {
		return native, err
	}
	state, err := managerAdTargetStatus(target.cabinet.Marketplace, "launch", n.Campaign)
	if err != nil {
		return native, err
	}
	candidate := o
	candidate.Snapshot = n.Campaign
	candidate.LaunchPlan = &domain.ManagerAdLaunchPlan{Settings: n.Settings}
	candidate.Request = native.request
	candidate.CredentialDigest = native.digest
	candidate.IntegrationName = target.integration.Name
	candidate.CabinetID = target.cabinet.ID
	candidate.ExternalID = target.campaign.ExternalID
	candidate.Marketplace = target.cabinet.Marketplace
	candidate.TargetStatus = state
	if adOperationFingerprint(candidate) != o.Fingerprint {
		return native, apperror.New(apperror.ErrConflict, "launch configuration, bids, budget, scope or keys changed; prepare again")
	}
	if err = s.q.ValidateManagerAdControl(ctx, o); err != nil {
		return native, err
	}
	return native, nil
}
