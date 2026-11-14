package service

import (
	"context"
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

var managerBidDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,18})(?:\.[0-9]{1,6})?$`)

func managerBidAmount(raw string, zero bool) (string, *big.Rat, error) {
	if !managerBidDecimal.MatchString(raw) {
		return "", nil, apperror.New(apperror.ErrValidation, "exact decimal bid/limit required")
	}
	amount, ok := new(big.Rat).SetString(raw)
	if !ok || amount.Sign() < 0 || !zero && amount.Sign() == 0 {
		return "", nil, apperror.New(apperror.ErrValidation, "positive bid/limit required")
	}
	canonical := raw
	if strings.Contains(canonical, ".") {
		canonical = strings.TrimSuffix(strings.TrimRight(canonical, "0"), ".")
	}
	return canonical, amount, nil
}
func managerBidConstraints(target string, before domain.NativeAdProductBid, c domain.ManagerAdBidConstraints) (domain.ManagerAdBidConstraints, error) {
	var result domain.ManagerAdBidConstraints
	_, next, err := managerBidAmount(target, false)
	if err != nil {
		return result, err
	}
	_, old, err := managerBidAmount(before.CurrentBidRub, false)
	if err != nil {
		return result, err
	}
	var maximum, percent *big.Rat
	result.MaxBidRub, maximum, err = managerBidAmount(c.MaxBidRub, false)
	if err != nil {
		return result, err
	}
	result.MaxChangePercent, percent, err = managerBidAmount(c.MaxChangePercent, true)
	if err != nil {
		return result, err
	}
	if percent.Cmp(big.NewRat(1000, 1)) > 0 || next.Cmp(maximum) > 0 || next.Cmp(old) == 0 {
		return result, apperror.New(apperror.ErrValidation, "bid exceeds explicit limit, unchanged, or change percentage exceeds 1000")
	}
	change := new(big.Rat).Sub(next, old)
	change.Abs(change)
	change.Mul(change, big.NewRat(100, 1))
	change.Quo(change, old)
	if change.Cmp(percent) > 0 {
		return result, apperror.New(apperror.ErrValidation, "bid exceeds personally specified change percentage")
	}
	if before.MinimumBidRub != nil {
		_, minimum, err := managerBidAmount(*before.MinimumBidRub, false)
		if err != nil || next.Cmp(minimum) < 0 {
			return result, apperror.New(apperror.ErrValidation, "bid below confirmed native minimum")
		}
	}
	if before.MaximumBidRub != nil {
		_, limit, err := managerBidAmount(*before.MaximumBidRub, false)
		if err != nil || next.Cmp(limit) > 0 {
			return result, apperror.New(apperror.ErrValidation, "bid above confirmed native maximum")
		}
	}
	return result, nil
}

func (s *ManagerAdOperationsService) bidTransport(target *managerAdTarget, plan *domain.ManagerAdBidPlan) (managerAdTransport, error) {
	var transport managerAdTransport
	if plan == nil {
		return transport, errors.New("immutable bid plan required")
	}
	product, err := strconv.ParseInt(plan.Before.ProductID, 10, 64)
	if err != nil || product <= 0 || strconv.FormatInt(product, 10) != plan.Before.ProductID {
		return transport, errors.New("exact native product ID required")
	}
	if target.cabinet.Marketplace == "wb" {
		if s.context.wb == nil {
			return transport, errors.New("WB bid transport unavailable")
		}
		token, err := crypto.Decrypt(target.credentials.EncryptedToken, s.context.key)
		if err != nil || token == "" {
			return transport, errors.New("WB identity unavailable")
		}
		transport.request, err = s.context.wb.AdvertisingProductBidRequest(token, target.externalID, product, plan.Before.Placement, plan.TargetBidRub)
		if err != nil {
			return transport, err
		}
		transport.digest = adDigest(token)
		transport.readBid = func(ctx context.Context) (*domain.NativeAdBidContext, error) {
			return s.context.wb.ReadManagerProductBid(ctx, token, target.externalID, product, plan.Before.Placement)
		}
		transport.send = func(ctx context.Context) error {
			return s.context.wb.SetExactProductBid(ctx, token, target.externalID, product, plan.Before.Placement, plan.TargetBidRub)
		}
		return transport, nil
	}
	if target.cabinet.Marketplace != "ozon" || s.context.ozon == nil || plan.Before.Placement != "campaign_product" {
		return transport, errors.New("specific Ozon campaign product bid required")
	}
	creds, err := decryptOzonCredentialsBlob(target.credentials.EncryptedCredentials, s.context.key)
	if err != nil || !creds.HasPerformanceAPI() {
		return transport, errors.New("Ozon Performance identity unavailable")
	}
	c := ozonClientCreds(creds)
	transport.request, err = s.context.ozon.AdvertisingProductBidRequest(c, target.externalID, product, plan.TargetBidRub)
	if err != nil {
		return transport, err
	}
	transport.digest = adDigest([]string{c.PerfClientID, c.PerfClientSecret})
	transport.readBid = func(ctx context.Context) (*domain.NativeAdBidContext, error) {
		return s.context.ozon.ReadManagerProductBid(ctx, c, target.externalID, product)
	}
	transport.send = func(ctx context.Context) error {
		return s.context.ozon.SetExactProductBid(ctx, c, target.externalID, product, plan.TargetBidRub)
	}
	return transport, nil
}

func (s *ManagerAdOperationsService) PrepareBid(ctx context.Context, identity domain.ManagerAccess, workspace, actor uuid.UUID, token, integration string, campaign uuid.UUID, product, placement, value string, constraints domain.ManagerAdBidConstraints) (*domain.ManagerAdOperation, error) {
	if workspace == uuid.Nil || actor == uuid.Nil {
		return nil, apperror.New(apperror.ErrValidation, "specific personal bid scope required")
	}
	fresh, err := s.access(ctx, identity, token, true)
	if err != nil {
		return nil, err
	}
	target, err := s.context.resolveTarget(ctx, fresh, workspace, token, integration, campaign)
	if err != nil {
		return nil, err
	}
	canonical, _, err := managerBidAmount(value, false)
	if err != nil {
		return nil, err
	}
	plan := &domain.ManagerAdBidPlan{Before: domain.NativeAdProductBid{ProductID: product, Placement: placement}, TargetBidRub: canonical, Constraints: constraints}
	native, err := s.bidTransport(target, plan)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "bid target, exact precision or native identity unavailable")
	}
	snapshot, err := native.readBid(ctx)
	if err != nil || snapshot == nil || snapshot.Campaign.ExternalID != target.campaign.ExternalID || snapshot.Bid.ProductID != product || snapshot.Bid.Placement != placement {
		return nil, apperror.New(errManagerAdsUnavailable, "native product bid snapshot unavailable")
	}
	plan.Before = snapshot.Bid
	plan.Constraints, err = managerBidConstraints(canonical, snapshot.Bid, constraints)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := "WB Advertising API"
	if target.cabinet.Marketplace == "ozon" {
		source = "Ozon Performance API"
	}
	o := domain.ManagerAdOperation{ID: uuid.New(), WorkspaceID: workspace, ActorID: actor, ExternalUserID: fresh.ExternalUserID, ExternalWorkspaceID: fresh.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: target.integration.Name, CabinetID: target.cabinet.ID, CampaignID: campaign, ExternalID: target.campaign.ExternalID, Marketplace: target.cabinet.Marketplace, OperationType: "ad_change", Action: "set_bid", Snapshot: snapshot.Campaign, TargetStatus: snapshot.Campaign.Status, Source: source, Request: native.request, CredentialDigest: native.digest, BidPlan: plan, Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	previous, err := s.q.CurrentManagerAdControl(ctx, native.request)
	if err != nil {
		return nil, apperror.New(apperror.ErrConflict, "campaign has unresolved or nontransferable control")
	}
	if previous != nil {
		if previous.Fingerprint != adOperationFingerprint(*previous) {
			return nil, apperror.New(apperror.ErrConflict, "previous control differs")
		}
		o.PreviousOperationID = &previous.ID
		o.PreviousFingerprint = previous.Fingerprint
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

func (s *ManagerAdOperationsService) validateCurrentBid(ctx context.Context, o domain.ManagerAdOperation, fresh domain.ManagerAccess, token string) (managerAdTransport, error) {
	var native managerAdTransport
	if o.BidPlan == nil {
		return native, apperror.New(apperror.ErrConflict, "immutable bid proposal unavailable")
	}
	target, err := s.context.resolveTarget(ctx, fresh, o.WorkspaceID, token, o.IntegrationID, o.CampaignID)
	if err != nil {
		return native, err
	}
	native, err = s.bidTransport(target, o.BidPlan)
	if err != nil {
		return native, err
	}
	snapshot, err := native.readBid(ctx)
	if err != nil || snapshot == nil {
		return native, errors.New("current native product bid unavailable")
	}
	limits, err := managerBidConstraints(o.BidPlan.TargetBidRub, snapshot.Bid, o.BidPlan.Constraints)
	if err != nil {
		return native, err
	}
	candidate := o
	plan := *o.BidPlan
	plan.Before = snapshot.Bid
	plan.Constraints = limits
	candidate.BidPlan = &plan
	candidate.Snapshot = snapshot.Campaign
	candidate.TargetStatus = snapshot.Campaign.Status
	candidate.Request = native.request
	candidate.CredentialDigest = native.digest
	candidate.CabinetID = target.cabinet.ID
	candidate.IntegrationName = target.integration.Name
	candidate.ExternalID = target.campaign.ExternalID
	candidate.Marketplace = target.cabinet.Marketplace
	if adOperationFingerprint(candidate) != o.Fingerprint {
		return native, apperror.New(apperror.ErrConflict, "bid source or constraints changed; prepare a new proposal")
	}
	if err = s.q.ValidateManagerAdControl(ctx, o); err != nil {
		return native, apperror.New(apperror.ErrConflict, "managed bid control changed")
	}
	return native, nil
}

func managerBidObserved(plan *domain.ManagerAdBidPlan, native domain.NativeAdBidContext) bool {
	if plan == nil || native.Bid.ProductID != plan.Before.ProductID || native.Bid.Placement != plan.Before.Placement || native.Bid.CurrentBidRub != plan.TargetBidRub || native.Bid.Strategy != plan.Before.Strategy || native.Bid.PaymentType != plan.Before.PaymentType || len(native.Bid.PeerBidsRub) != len(plan.Before.PeerBidsRub) {
		return false
	}
	for placement, bid := range plan.Before.PeerBidsRub {
		if placement != plan.Before.Placement && native.Bid.PeerBidsRub[placement] != bid {
			return false
		}
	}
	return true
}
