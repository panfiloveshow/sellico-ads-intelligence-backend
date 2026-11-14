package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type ManagerAdContextService struct {
	queries *sqlcgen.Queries
	scope   *ManagerAdsService
	wb      *wb.Client
	ozon    *ozon.PerfClient
	key     []byte
}

func NewManagerAdContextService(q *sqlcgen.Queries, crm ManagerIntegrationVerifier, wbClient *wb.Client, ozonClient *ozon.PerfClient, key []byte) *ManagerAdContextService {
	return &ManagerAdContextService{q, NewManagerAdsService(q, crm), wbClient, ozonClient, append([]byte(nil), key...)}
}

type managerAdTarget struct {
	integration *domain.ManagerIntegration
	cabinet     *domain.ManagerAdsCabinet
	campaign    *domain.ManagerAdsCampaign
	credentials domain.SellerCabinet
	externalID  int64
}

func (s *ManagerAdContextService) resolveTarget(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integrationID string, campaignID uuid.UUID) (*managerAdTarget, error) {
	if s == nil || s.queries == nil || s.scope == nil {
		return nil, apperror.New(errManagerAdsUnavailable, "native advertising context is not configured")
	}
	if campaignID == uuid.Nil {
		return nil, apperror.New(apperror.ErrValidation, "specific campaign required")
	}
	integration, cabinet, err := s.scope.resolveCabinet(ctx, identity, workspace, token, integrationID)
	if err != nil {
		return nil, err
	}
	if cabinet.Status != "active" {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising cabinet is not active")
	}
	target, err := s.queries.ManagerAdsCampaignByID(ctx, *cabinet, campaignID)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "campaign mapping unavailable")
	}
	if target == nil {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found in selected store")
	}
	externalID, err := strconv.ParseInt(target.ExternalID, 10, 64)
	if err != nil || externalID <= 0 {
		return nil, apperror.New(errManagerAdsUnavailable, "unverified native campaign identity")
	}
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinet.ID))
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "advertising credentials unavailable")
	}
	credentials := sellerCabinetFromSqlc(row)
	if credentials.WorkspaceID != workspace || string(credentials.Marketplace) != cabinet.Marketplace || credentials.Status != "active" || pgTextValue(row.ExternalIntegrationID) != integrationID || row.DeletedAt.Valid {
		return nil, apperror.New(apperror.ErrForbidden, "advertising cabinet scope changed")
	}
	return &managerAdTarget{integration, cabinet, target, credentials, externalID}, nil
}

func (s *ManagerAdContextService) CampaignContext(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integrationID string, campaignID uuid.UUID) (*domain.ManagerAdContext, error) {
	resolved, err := s.resolveTarget(ctx, identity, workspace, token, integrationID, campaignID)
	if err != nil {
		return nil, err
	}
	integration, cabinet, target, credentials, externalID := resolved.integration, resolved.cabinet, resolved.campaign, resolved.credentials, resolved.externalID
	var native *domain.NativeAdCampaign
	source := ""
	switch cabinet.Marketplace {
	case "wb":
		if s.wb == nil {
			return nil, apperror.New(errManagerAdsUnavailable, "WB context reader unavailable")
		}
		var sellerToken string
		sellerToken, err = crypto.Decrypt(credentials.EncryptedToken, s.key)
		if err == nil && sellerToken != "" {
			native, err = s.wb.ReadCampaignContext(ctx, sellerToken, externalID)
		} else {
			err = errors.New("WB credentials unreadable")
		}
		source = "WB Advertising API"
	case "ozon":
		if s.ozon == nil {
			return nil, apperror.New(errManagerAdsUnavailable, "Ozon context reader unavailable")
		}
		var creds domain.OzonCredentials
		creds, err = decryptOzonCredentialsBlob(credentials.EncryptedCredentials, s.key)
		if err == nil && creds.HasPerformanceAPI() {
			native, err = s.ozon.ReadCampaignContext(ctx, ozonClientCreds(creds), externalID)
		} else {
			err = errors.New("Ozon Performance credentials unavailable")
		}
		source = "Ozon Performance API"
	default:
		err = errors.New("unsupported advertising marketplace")
	}
	if err != nil || native == nil || native.ExternalID != target.ExternalID || native.Status == "" {
		return nil, apperror.New(errManagerAdsUnavailable, "native campaign context unavailable; stored state is not a fallback")
	}
	return &domain.ManagerAdContext{NativeAdCampaign: *native, WorkspaceID: identity.ExternalWorkspaceID, IntegrationID: integrationID, IntegrationName: integration.Name, Marketplace: cabinet.Marketplace,
		CampaignID: campaignID, Currency: "RUB", Source: source, RetrievedAt: time.Now().UTC(), Partial: true, ExecutionPolicy: "not_configured", AvailableActions: []string{}}, nil
}

// ProductBidContext is read-only: it does not need a target amount or invent
// personal limits merely to answer a question about the selected product.
func (s *ManagerAdContextService) ProductBidContext(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration string, campaign uuid.UUID, product, placement string) (*domain.ManagerAdProductBidContext, error) {
	target, err := s.resolveTarget(ctx, identity, workspace, token, integration, campaign)
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(product, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != product {
		return nil, apperror.New(apperror.ErrValidation, "exact native product ID required")
	}
	var snapshot *domain.NativeAdBidContext
	source := ""
	switch target.cabinet.Marketplace {
	case "wb":
		if s.wb == nil {
			return nil, apperror.New(errManagerAdsUnavailable, "WB bid reader unavailable")
		}
		sellerToken, decodeErr := crypto.Decrypt(target.credentials.EncryptedToken, s.key)
		if decodeErr != nil || sellerToken == "" {
			return nil, apperror.New(errManagerAdsUnavailable, "WB identity unavailable")
		}
		snapshot, err = s.wb.ReadManagerProductBid(ctx, sellerToken, target.externalID, id, placement)
		source = "WB Advertising API"
	case "ozon":
		if s.ozon == nil || placement != "campaign_product" {
			return nil, apperror.New(apperror.ErrValidation, "specific Ozon campaign product required")
		}
		creds, decodeErr := decryptOzonCredentialsBlob(target.credentials.EncryptedCredentials, s.key)
		if decodeErr != nil || !creds.HasPerformanceAPI() {
			return nil, apperror.New(errManagerAdsUnavailable, "Ozon identity unavailable")
		}
		snapshot, err = s.ozon.ReadManagerProductBid(ctx, ozonClientCreds(creds), target.externalID, id)
		source = "Ozon Performance API"
	default:
		return nil, apperror.New(apperror.ErrValidation, "marketplace bid reader unavailable")
	}
	if err != nil || snapshot == nil || snapshot.Campaign.ExternalID != target.campaign.ExternalID || snapshot.Bid.ProductID != product || snapshot.Bid.Placement != placement {
		return nil, apperror.New(errManagerAdsUnavailable, "native product bid context unavailable")
	}
	return &domain.ManagerAdProductBidContext{NativeAdBidContext: *snapshot, WorkspaceID: identity.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: target.integration.Name, Marketplace: target.cabinet.Marketplace, CampaignID: campaign, Currency: "RUB", Source: source, RetrievedAt: time.Now().UTC(), Partial: true, ExecutionPolicy: "personal_confirmation_required", ExecutionAllowed: false}, nil
}

func (s *ManagerAdContextService) CampaignBudgetContext(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration string, campaign uuid.UUID, period string) (*domain.ManagerAdCampaignBudgetContext, error) {
	if period != "daily" && period != "weekly" {
		return nil, apperror.New(apperror.ErrValidation, "explicit daily or weekly budget period required")
	}
	target, err := s.resolveTarget(ctx, identity, workspace, token, integration, campaign)
	if err != nil {
		return nil, err
	}
	var snapshot *domain.NativeAdBudgetContext
	source := ""
	switch target.cabinet.Marketplace {
	case "wb":
		if s.wb == nil || period != "daily" {
			return nil, apperror.New(apperror.ErrValidation, "WB CPC daily-limit reader required")
		}
		sellerToken, decodeErr := crypto.Decrypt(target.credentials.EncryptedToken, s.key)
		if decodeErr != nil || sellerToken == "" {
			return nil, apperror.New(errManagerAdsUnavailable, "WB identity unavailable")
		}
		snapshot, err = s.wb.ReadManagerCampaignBudget(ctx, sellerToken, target.externalID, period)
		source = "WB Advertising API"
	case "ozon":
		if s.ozon == nil {
			return nil, apperror.New(errManagerAdsUnavailable, "Ozon budget reader unavailable")
		}
		creds, decodeErr := decryptOzonCredentialsBlob(target.credentials.EncryptedCredentials, s.key)
		if decodeErr != nil || !creds.HasPerformanceAPI() {
			return nil, apperror.New(errManagerAdsUnavailable, "Ozon identity unavailable")
		}
		snapshot, err = s.ozon.ReadManagerCampaignBudget(ctx, ozonClientCreds(creds), target.externalID, period)
		source = "Ozon Performance API"
	default:
		return nil, apperror.New(apperror.ErrValidation, "marketplace budget reader unavailable")
	}
	if err != nil || snapshot == nil || snapshot.Campaign.ExternalID != target.campaign.ExternalID || snapshot.Budget.Period != period || snapshot.Budget.Currency != "RUB" {
		return nil, apperror.New(errManagerAdsUnavailable, "native campaign budget unavailable")
	}
	return &domain.ManagerAdCampaignBudgetContext{NativeAdBudgetContext: *snapshot, WorkspaceID: identity.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: target.integration.Name, Marketplace: target.cabinet.Marketplace, CampaignID: campaign, Currency: "RUB", Source: source, RetrievedAt: time.Now().UTC(), Partial: true, ExecutionPolicy: "not_configured", ExecutionAllowed: false, AvailableActions: []string{}}, nil
}
