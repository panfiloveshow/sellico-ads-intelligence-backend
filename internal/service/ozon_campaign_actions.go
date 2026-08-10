package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	// ozonBidItemsLimit caps one manual bid write (Ozon allows 500 SKU/campaign).
	ozonBidItemsLimit = 500
	// ozonCPOSkusLimit caps one CPO enable/disable/bid request.
	ozonCPOSkusLimit = 1000
	// ozonBidLimitsCacheTTL is how long the limits/list passthrough is cached.
	ozonBidLimitsCacheTTL = time.Hour
)

// OzonBidInput is one manual per-SKU bid coming from the API layer.
type OzonBidInput struct {
	SKU       int64    `json:"sku"`
	BidRub    float64  `json:"bid_rub"`
	TargetCIR *float64 `json:"target_cir,omitempty"`
}

// OzonCPOBidInput is one manual CPO bid coming from the API layer (rubles).
type OzonCPOBidInput struct {
	SKU    int64   `json:"sku"`
	BidRub float64 `json:"bid"`
}

type ozonBidLimitsCacheEntry struct {
	payload   json.RawMessage
	expiresAt time.Time
}

// ozonCampaignPerfClient is the narrow Performance-API surface the campaign
// actions service needs. *ozon.PerfClient satisfies it in production; tests
// supply a fake so the audited write paths (activate/budget/bids/CPO) can be
// exercised without a live Ozon call.
type ozonCampaignPerfClient interface {
	ActivateCampaign(ctx context.Context, creds ozon.Credentials, campaignID int64) error
	DeactivateCampaign(ctx context.Context, creds ozon.Credentials, campaignID int64) error
	UpdateCampaign(ctx context.Context, creds ozon.Credentials, campaignID int64, patch ozon.CampaignPatch) error
	SetCampaignProductBids(ctx context.Context, creds ozon.Credentials, campaignID int64, bids []ozon.ProductBid) error
	GetCompetitiveBids(ctx context.Context, creds ozon.Credentials, campaignID int64, skus []int64) ([]ozon.CompetitiveBid, error)
	ListSearchPromoProducts(ctx context.Context, creds ozon.Credentials) ([]ozon.CPOProduct, error)
	EnableSearchPromo(ctx context.Context, creds ozon.Credentials, skus []int64) error
	DisableSearchPromo(ctx context.Context, creds ozon.Credentials, skus []int64) error
	SetSearchPromoBids(ctx context.Context, creds ozon.Credentials, bids []ozon.CPOBid) error
	GetCPOMinBids(ctx context.Context, creds ozon.Credentials, skus []int64) ([]ozon.MinSKUBid, error)
	GetAllSKUPromoRate(ctx context.Context, creds ozon.Credentials) (int, error)
	SetAllSKUPromoRate(ctx context.Context, creds ozon.Credentials, ratePct int) error
	DeactivateAllSKUPromo(ctx context.Context, creds ozon.Credentials) error
	GetBidLimits(ctx context.Context, creds ozon.Credentials) (json.RawMessage, error)
}

// OzonCampaignActionsService performs phase-2 writes towards Ozon: campaign
// activate/deactivate, budget updates, manual per-SKU bids, and CPO (search
// promo) management. Every bid write is audited in ozon_bid_changes
// (source='manual', pending → applied/failed).
type OzonCampaignActionsService struct {
	queries       *sqlcgen.Queries
	perfClient    ozonCampaignPerfClient
	encryptionKey []byte
	logger        zerolog.Logger

	limitsMu    sync.Mutex
	limitsCache map[uuid.UUID]ozonBidLimitsCacheEntry
}

// NewOzonCampaignActionsService wires the Ozon campaign actions service.
func NewOzonCampaignActionsService(queries *sqlcgen.Queries, perfClient *ozon.PerfClient, encryptionKey []byte, logger zerolog.Logger) *OzonCampaignActionsService {
	return &OzonCampaignActionsService{
		queries:       queries,
		perfClient:    perfClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ozon_campaign_actions").Logger(),
		limitsCache:   make(map[uuid.UUID]ozonBidLimitsCacheEntry),
	}
}

// --- tenancy helpers (same gates as the read service) ---

func (s *OzonCampaignActionsService) resolveCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, ozon.Credentials, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, ozon.Credentials{}, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon || cabinet.WorkspaceID != workspaceID {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return nil, ozon.Credentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	if !creds.HasPerformanceAPI() {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrValidation, "performance api credentials are missing for this cabinet")
	}
	return &cabinet, ozonClientCreds(creds), nil
}

func (s *OzonCampaignActionsService) ownedCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (sqlcgen.OzonCampaign, ozon.Credentials, error) {
	row, err := s.queries.GetOzonCampaignByID(ctx, uuidToPgtype(campaignID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.OzonCampaign{}, ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "ozon campaign not found")
	}
	if err != nil {
		return sqlcgen.OzonCampaign{}, ozon.Credentials{}, apperror.New(apperror.ErrInternal, "failed to load ozon campaign")
	}
	_, creds, err := s.resolveCabinet(ctx, workspaceID, uuidFromPgtype(row.SellerCabinetID))
	if err != nil {
		if apperror.Is(err, apperror.ErrNotFound) {
			return sqlcgen.OzonCampaign{}, ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "ozon campaign not found")
		}
		return sqlcgen.OzonCampaign{}, ozon.Credentials{}, err
	}
	return row, creds, nil
}

// --- campaign lifecycle ---

// ActivateCampaign starts the campaign on Ozon and optimistically mirrors the
// running state locally; the next scheduled sync reconciles the truth.
func (s *OzonCampaignActionsService) ActivateCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	return s.setCampaignState(ctx, workspaceID, campaignID, true)
}

// DeactivateCampaign stops the campaign on Ozon (state mirrored optimistically).
func (s *OzonCampaignActionsService) DeactivateCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	return s.setCampaignState(ctx, workspaceID, campaignID, false)
}

func (s *OzonCampaignActionsService) setCampaignState(ctx context.Context, workspaceID, campaignID uuid.UUID, activate bool) error {
	campaign, creds, err := s.ownedCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	newState := "CAMPAIGN_STATE_INACTIVE"
	if activate {
		newState = "CAMPAIGN_STATE_RUNNING"
		err = s.perfClient.ActivateCampaign(ctx, creds, campaign.OzonCampaignID)
	} else {
		err = s.perfClient.DeactivateCampaign(ctx, creds, campaign.OzonCampaignID)
	}
	if err != nil {
		return fmt.Errorf("ozon campaign state change: %w", err)
	}
	if stateErr := s.queries.UpdateOzonCampaignState(ctx, sqlcgen.UpdateOzonCampaignStateParams{
		ID:    campaign.ID,
		State: textToPgtype(newState),
	}); stateErr != nil {
		s.logger.Warn().Err(stateErr).Str("campaign_id", campaignID.String()).Msg("failed to mirror ozon campaign state")
	}
	s.logger.Info().
		Str("campaign_id", campaignID.String()).
		Int64("ozon_campaign_id", campaign.OzonCampaignID).
		Bool("activate", activate).
		Msg("ozon campaign state changed")
	return nil
}

// UpdateBudget patches daily/weekly budgets (whole rubles) on Ozon and
// mirrors them locally.
func (s *OzonCampaignActionsService) UpdateBudget(ctx context.Context, workspaceID, campaignID uuid.UUID, dailyRub, weeklyRub *int64) error {
	if dailyRub == nil && weeklyRub == nil {
		return apperror.New(apperror.ErrValidation, "at least one of daily_budget_rub or weekly_budget_rub is required")
	}
	if (dailyRub != nil && *dailyRub < 0) || (weeklyRub != nil && *weeklyRub < 0) {
		return apperror.New(apperror.ErrValidation, "budgets must be non-negative")
	}
	campaign, creds, err := s.ownedCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if err := s.perfClient.UpdateCampaign(ctx, creds, campaign.OzonCampaignID, ozon.CampaignPatch{
		DailyBudgetRub:  dailyRub,
		WeeklyBudgetRub: weeklyRub,
	}); err != nil {
		return fmt.Errorf("ozon campaign budget update: %w", err)
	}
	if mirrorErr := s.queries.UpdateOzonCampaignBudgets(ctx, sqlcgen.UpdateOzonCampaignBudgetsParams{
		ID:              campaign.ID,
		DailyBudgetRub:  int64PtrToPgInt8(dailyRub),
		WeeklyBudgetRub: int64PtrToPgInt8(weeklyRub),
	}); mirrorErr != nil {
		s.logger.Warn().Err(mirrorErr).Str("campaign_id", campaignID.String()).Msg("failed to mirror ozon campaign budgets")
	}
	return nil
}

// --- manual per-SKU bids ---

// SetProductBids writes manual per-SKU bids for one campaign. Each SKU gets
// an ozon_bid_changes audit row (source='manual'): pending before the API
// call, applied/failed after; the ozon_campaign_products mirror is updated on
// success.
func (s *OzonCampaignActionsService) SetProductBids(ctx context.Context, workspaceID, campaignID uuid.UUID, items []OzonBidInput) ([]domain.OzonBidChange, error) {
	return s.SetProductBidsWithSource(ctx, workspaceID, campaignID, items, domain.BidSourceManual, "manual bid update")
}

// SetProductBidsWithSource is the source-aware variant: the AI manager applies
// approved/auto decisions through the exact same audited path with source='ai'.
func (s *OzonCampaignActionsService) SetProductBidsWithSource(ctx context.Context, workspaceID, campaignID uuid.UUID, items []OzonBidInput, source, reason string) ([]domain.OzonBidChange, error) {
	if len(items) == 0 {
		return nil, apperror.New(apperror.ErrValidation, "items are required")
	}
	if len(items) > ozonBidItemsLimit {
		return nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("at most %d bids per request", ozonBidItemsLimit))
	}
	for _, item := range items {
		if item.SKU <= 0 || item.BidRub <= 0 {
			return nil, apperror.New(apperror.ErrValidation, "every item needs a positive sku and bid_rub")
		}
	}
	campaign, creds, err := s.ownedCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, err
	}

	// Current mirror bids become old_bid_rub in the audit rows.
	oldBids := map[int64]*float64{}
	if productRows, listErr := s.queries.ListOzonCampaignProducts(ctx, campaign.ID); listErr == nil {
		for _, row := range productRows {
			oldBids[row.Sku] = pgNumericToFloatPtr(row.BidRub)
		}
	}

	changeIDs := make([]pgtype.UUID, 0, len(items))
	result := make([]domain.OzonBidChange, 0, len(items))
	for _, item := range items {
		var oldBid pgtype.Numeric
		if prev, ok := oldBids[item.SKU]; ok && prev != nil {
			oldBid = floatToPgNumeric(*prev)
		}
		row, insertErr := s.queries.InsertOzonBidChange(ctx, sqlcgen.InsertOzonBidChangeParams{
			CampaignID: campaign.ID,
			Kind:       domain.OzonBidKindCPC,
			Sku:        pgtype.Int8{Int64: item.SKU, Valid: true},
			OldBidRub:  oldBid,
			NewBidRub:  floatToPgNumeric(item.BidRub),
			Reason:     textToPgtype(reason),
			Source:     source,
			Status:     domain.OzonBidStatusPending,
		})
		if insertErr != nil {
			return nil, fmt.Errorf("record ozon bid change: %w", insertErr)
		}
		changeIDs = append(changeIDs, row.ID)
		result = append(result, ozonBidChangeFromSqlc(row))
	}

	bids := make([]ozon.ProductBid, 0, len(items))
	for _, item := range items {
		bids = append(bids, ozon.ProductBid{SKU: item.SKU, BidRub: item.BidRub, TargetCIR: item.TargetCIR})
	}
	writeErr := s.perfClient.SetCampaignProductBids(ctx, creds, campaign.OzonCampaignID, bids)

	status := domain.OzonBidStatusApplied
	var errText pgtype.Text
	if writeErr != nil {
		status = domain.OzonBidStatusFailed
		errText = textToPgtype(truncateError(writeErr.Error()))
	}
	for i, id := range changeIDs {
		if markErr := s.queries.MarkOzonBidChangeResult(ctx, sqlcgen.MarkOzonBidChangeResultParams{
			ID:     id,
			Status: status,
			Error:  errText,
		}); markErr != nil {
			s.logger.Warn().Err(markErr).Msg("failed to finalize ozon bid change audit row")
		}
		result[i].Status = status
		if writeErr != nil {
			result[i].Error = errText.String
		}
	}
	if writeErr != nil {
		return result, fmt.Errorf("ozon set product bids: %w", writeErr)
	}

	for _, item := range items {
		var targetCIR pgtype.Numeric
		if item.TargetCIR != nil {
			targetCIR = floatToPgNumeric(*item.TargetCIR)
		}
		if mirrorErr := s.queries.UpdateOzonCampaignProductBid(ctx, sqlcgen.UpdateOzonCampaignProductBidParams{
			CampaignID: campaign.ID,
			Sku:        item.SKU,
			BidRub:     floatToPgNumeric(item.BidRub),
			TargetCir:  targetCIR,
		}); mirrorErr != nil {
			s.logger.Warn().Err(mirrorErr).Int64("sku", item.SKU).Msg("failed to mirror ozon product bid")
		}
	}
	return result, nil
}

// GetCompetitiveBids returns competitor bid levels for the campaign's SKUs.
func (s *OzonCampaignActionsService) GetCompetitiveBids(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]ozon.CompetitiveBid, error) {
	campaign, creds, err := s.ownedCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, err
	}
	productRows, err := s.queries.ListOzonCampaignProducts(ctx, campaign.ID)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list ozon campaign products")
	}
	skus := make([]int64, 0, len(productRows))
	for _, row := range productRows {
		skus = append(skus, row.Sku)
	}
	if len(skus) == 0 {
		return []ozon.CompetitiveBid{}, nil
	}
	bids, err := s.perfClient.GetCompetitiveBids(ctx, creds, campaign.OzonCampaignID, skus)
	if err != nil {
		s.logger.Warn().Err(err).
			Int64("ozon_campaign_id", campaign.OzonCampaignID).
			Int("skus", len(skus)).
			Msg("ozon competitive bids request failed")
		// Ozon отдаёт конкурентные ставки не для всех типов кампаний/стратегий —
		// это ошибка апстрима, а не наша: 502 с читаемым сообщением вместо 500.
		return nil, apperror.New(apperror.ErrOzonAPIError, "Ozon не отдал конкурентные ставки для этой кампании")
	}
	return bids, nil
}

// ListBidChanges returns the ozon_bid_changes audit page for a cabinet.
// cabinetID may be uuid.Nil when campaignID is given — the cabinet is then
// resolved from the campaign (campaign detail page passes only campaign_id).
func (s *OzonCampaignActionsService) ListBidChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaignID *uuid.UUID, limit, offset int32) ([]domain.OzonBidChange, int64, error) {
	if cabinetID == uuid.Nil {
		if campaignID == nil {
			return nil, 0, apperror.New(apperror.ErrValidation, "cabinet_id or campaign_id is required")
		}
		row, err := s.queries.GetOzonCampaignByID(ctx, uuidToPgtype(*campaignID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, apperror.New(apperror.ErrNotFound, "ozon campaign not found")
		}
		if err != nil {
			return nil, 0, apperror.New(apperror.ErrInternal, "failed to load ozon campaign")
		}
		cabinetID = uuidFromPgtype(row.SellerCabinetID)
	}
	if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	total, err := s.queries.CountOzonBidChangesByCabinet(ctx, sqlcgen.CountOzonBidChangesByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		CampaignID:      uuidToPgtypePtr(campaignID),
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ozon bid changes")
	}
	rows, err := s.queries.ListOzonBidChangesByCabinet(ctx, sqlcgen.ListOzonBidChangesByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		CampaignID:      uuidToPgtypePtr(campaignID),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ozon bid changes")
	}
	skus := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.Sku.Valid {
			skus = append(skus, row.Sku.Int64)
		}
	}
	names := ozonProductInfoBySKU(ctx, s.queries, s.logger, cabinetID, skus)

	result := make([]domain.OzonBidChange, 0, len(rows))
	for _, row := range rows {
		change := ozonBidChangeFromSqlc(row)
		if row.Sku.Valid {
			if info, ok := names[row.Sku.Int64]; ok {
				change.Name = pgTextValue(info.Name)
			}
		}
		result = append(result, change)
	}
	return result, total, nil
}

// resolveCabinetReadOnly checks tenancy without requiring Performance creds
// (list endpoints must work for prices-only cabinets too).
func (s *OzonCampaignActionsService) resolveCabinetReadOnly(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, domain.OzonCredentials, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.OzonCredentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, domain.OzonCredentials{}, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon || cabinet.WorkspaceID != workspaceID {
		return nil, domain.OzonCredentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return nil, domain.OzonCredentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	return &cabinet, creds, nil
}

// --- CPO (search promo) ---

// ListCPOProducts refreshes the CPO mirror from the API and returns a page.
// The API sync is best-effort: a stale mirror still serves the page.
func (s *OzonCampaignActionsService) ListCPOProducts(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCPOProduct, int64, error) {
	cabinet, rawCreds, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, 0, err
	}
	if rawCreds.HasPerformanceAPI() {
		creds := ozonClientCreds(rawCreds)
		products, listErr := s.perfClient.ListSearchPromoProducts(ctx, creds)
		if listErr != nil {
			s.logger.Warn().Err(listErr).Str("cabinet_id", cabinet.ID.String()).Msg("cpo product sync failed; serving mirror")
		} else {
			for _, product := range products {
				var bid pgtype.Numeric
				var bidKind pgtype.Text
				if product.BidRub > 0 {
					bid = floatToPgNumeric(product.BidRub)
					bidKind = textToPgtype("fixed_rub")
				}
				if upsertErr := s.queries.UpsertOzonCpoProduct(ctx, sqlcgen.UpsertOzonCpoProductParams{
					SellerCabinetID: uuidToPgtype(cabinet.ID),
					Sku:             product.SKU,
					Enabled:         product.Enabled,
					Bid:             bid,
					BidKind:         bidKind,
					OfferID:         textToPgtype(product.OfferID),
					Name:            textToPgtype(product.Name),
					PriceRub:        positiveFloatToPgNumeric(product.PriceRub),
					BidPriceRub:     positiveFloatToPgNumeric(product.BidPriceRub),
					ImageUrl:        textToPgtype(product.ImageURL),
					VisibilityIndex: textToPgtype(product.VisibilityIndex),
					PrevBidPct:      positiveFloatToPgNumeric(product.PrevBidPct),
					ViewsThisWeek:   int64PtrToPgInt8(product.ViewsThisWeek),
					ViewsPrevWeek:   int64PtrToPgInt8(product.ViewsPrevWeek),
				}); upsertErr != nil {
					return nil, 0, fmt.Errorf("upsert cpo product %d: %w", product.SKU, upsertErr)
				}
			}
		}
	}

	total, err := s.queries.CountOzonCpoProducts(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count cpo products")
	}
	rows, err := s.queries.ListOzonCpoProducts(ctx, sqlcgen.ListOzonCpoProductsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list cpo products")
	}
	skus := make([]int64, 0, len(rows))
	for _, row := range rows {
		skus = append(skus, row.Sku)
	}
	names := ozonProductInfoBySKU(ctx, s.queries, s.logger, cabinetID, skus)
	minBids := s.cpoMinBidsBySKU(ctx, cabinet, rawCreds, skus)

	result := make([]domain.OzonCPOProduct, 0, len(rows))
	for _, row := range rows {
		product := domain.OzonCPOProduct{
			ID:              uuidFromPgtype(row.ID),
			SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
			SKU:             row.Sku,
			Enabled:         row.Enabled,
			Bid:             pgNumericToFloatPtr(row.Bid),
			BidKind:         pgTextValue(row.BidKind),
			// Name/OfferID prefer the ozon_products mapping (below); the CPO row's
			// own title/sourceSku is the fallback when the products sync has not
			// yet seen the SKU.
			Name:            pgTextValue(row.Name),
			OfferID:         pgTextValue(row.OfferID),
			PriceRub:        pgNumericToFloatPtr(row.PriceRub),
			BidPriceRub:     pgNumericToFloatPtr(row.BidPriceRub),
			ImageURL:        pgTextValue(row.ImageUrl),
			VisibilityIndex: pgTextValue(row.VisibilityIndex),
			PrevBidPct:      pgNumericToFloatPtr(row.PrevBidPct),
			UpdatedAt:       row.UpdatedAt.Time,
		}
		if row.ViewsThisWeek.Valid {
			views := row.ViewsThisWeek.Int64
			product.ViewsThisWeek = &views
		}
		if row.ViewsPrevWeek.Valid {
			views := row.ViewsPrevWeek.Int64
			product.ViewsPrevWeek = &views
		}
		if minBid, ok := minBids[row.Sku]; ok {
			bid := minBid
			product.MinBidRub = &bid
		}
		if info, ok := names[row.Sku]; ok {
			if name := pgTextValue(info.Name); name != "" {
				product.Name = name
			}
			if offerID := pgTextValue(info.OfferID); offerID != "" {
				product.OfferID = offerID
			}
		}
		result = append(result, product)
	}
	return result, total, nil
}

// cpoMinBidsBySKU fetches minimum fixed CPO bids for one page of SKUs
// (get_cpo_min_bids, chunked in the client). Best-effort live enrichment:
// missing Performance creds or an API failure yield an empty map, never an
// error — the products list must render from the mirror regardless.
func (s *OzonCampaignActionsService) cpoMinBidsBySKU(ctx context.Context, cabinet *domain.SellerCabinet, rawCreds domain.OzonCredentials, skus []int64) map[int64]float64 {
	result := map[int64]float64{}
	if len(skus) == 0 || !rawCreds.HasPerformanceAPI() {
		return result
	}
	bids, err := s.perfClient.GetCPOMinBids(ctx, ozonClientCreds(rawCreds), skus)
	if err != nil {
		s.logger.Warn().Err(err).Str("cabinet_id", cabinet.ID.String()).Int("skus", len(skus)).
			Msg("cpo min bids enrichment failed; serving products without min_bid_rub")
		return result
	}
	for _, bid := range bids {
		result[bid.SKU] = bid.BidRub
	}
	return result
}

// positiveFloatToPgNumeric returns a NUMERIC for value>0 and a NULL (invalid)
// numeric otherwise, so a missing/zero enriched value never overwrites a
// previously mirrored one through the COALESCE upsert.
func positiveFloatToPgNumeric(value float64) pgtype.Numeric {
	if value <= 0 {
		return pgtype.Numeric{}
	}
	return floatToPgNumeric(value)
}

// EnableCPO enables search promo for the SKUs (audited, mirror updated).
func (s *OzonCampaignActionsService) EnableCPO(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error {
	return s.SetCPOEnabledWithSource(ctx, workspaceID, cabinetID, skus, true, domain.BidSourceManual, "manual cpo enable")
}

// DisableCPO disables search promo for the SKUs (audited, mirror updated).
func (s *OzonCampaignActionsService) DisableCPO(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error {
	return s.SetCPOEnabledWithSource(ctx, workspaceID, cabinetID, skus, false, domain.BidSourceManual, "manual cpo disable")
}

// SetCPOEnabledWithSource is the source-aware CPO toggle used by both the
// manual endpoints and the AI manager (source='ai').
func (s *OzonCampaignActionsService) SetCPOEnabledWithSource(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64, enabled bool, source, reason string) error {
	if err := validateCPOSkus(skus); err != nil {
		return err
	}
	cabinet, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return err
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	changeIDs := make([]pgtype.UUID, 0, len(skus))
	for _, sku := range skus {
		row, insertErr := s.queries.InsertOzonBidChange(ctx, sqlcgen.InsertOzonBidChangeParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Kind:            domain.OzonBidKindCPO,
			Sku:             pgtype.Int8{Int64: sku, Valid: true},
			Reason:          textToPgtype(reason),
			Source:          source,
			Status:          domain.OzonBidStatusPending,
		})
		if insertErr != nil {
			return fmt.Errorf("record cpo %s audit: %w", action, insertErr)
		}
		changeIDs = append(changeIDs, row.ID)
	}

	var writeErr error
	if enabled {
		writeErr = s.perfClient.EnableSearchPromo(ctx, creds, skus)
	} else {
		writeErr = s.perfClient.DisableSearchPromo(ctx, creds, skus)
	}
	s.finalizeBidChanges(ctx, changeIDs, writeErr)
	if writeErr != nil {
		return fmt.Errorf("ozon cpo %s: %w", action, writeErr)
	}

	for _, sku := range skus {
		if upsertErr := s.queries.UpsertOzonCpoProduct(ctx, sqlcgen.UpsertOzonCpoProductParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Sku:             sku,
			Enabled:         enabled,
		}); upsertErr != nil {
			s.logger.Warn().Err(upsertErr).Int64("sku", sku).Msg("failed to mirror cpo enabled state")
		}
	}
	return nil
}

// SetCPOBids writes fixed CPO bids (rubles) for a cabinet, audited per SKU.
func (s *OzonCampaignActionsService) SetCPOBids(ctx context.Context, workspaceID, cabinetID uuid.UUID, bids []OzonCPOBidInput) error {
	return s.SetCPOBidsWithSource(ctx, workspaceID, cabinetID, bids, domain.BidSourceManual, "manual cpo bid update")
}

// SetCPOBidsWithSource is the source-aware variant used by the AI manager.
func (s *OzonCampaignActionsService) SetCPOBidsWithSource(ctx context.Context, workspaceID, cabinetID uuid.UUID, bids []OzonCPOBidInput, source, reason string) error {
	if len(bids) == 0 {
		return apperror.New(apperror.ErrValidation, "bids are required")
	}
	if len(bids) > ozonCPOSkusLimit {
		return apperror.New(apperror.ErrValidation, fmt.Sprintf("at most %d bids per request", ozonCPOSkusLimit))
	}
	for _, bid := range bids {
		if bid.SKU <= 0 || bid.BidRub <= 0 {
			return apperror.New(apperror.ErrValidation, "every bid needs a positive sku and bid")
		}
	}
	cabinet, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return err
	}

	oldBids := map[int64]pgtype.Numeric{}
	if rows, listErr := s.queries.ListOzonCpoProducts(ctx, sqlcgen.ListOzonCpoProductsParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID), Limit: int32(ozonCPOSkusLimit), Offset: 0,
	}); listErr == nil {
		for _, row := range rows {
			oldBids[row.Sku] = row.Bid
		}
	}

	changeIDs := make([]pgtype.UUID, 0, len(bids))
	for _, bid := range bids {
		row, insertErr := s.queries.InsertOzonBidChange(ctx, sqlcgen.InsertOzonBidChangeParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Kind:            domain.OzonBidKindCPO,
			Sku:             pgtype.Int8{Int64: bid.SKU, Valid: true},
			OldBidRub:       oldBids[bid.SKU],
			NewBidRub:       floatToPgNumeric(bid.BidRub),
			Reason:          textToPgtype(reason),
			Source:          source,
			Status:          domain.OzonBidStatusPending,
		})
		if insertErr != nil {
			return fmt.Errorf("record cpo bid audit: %w", insertErr)
		}
		changeIDs = append(changeIDs, row.ID)
	}

	wireBids := make([]ozon.CPOBid, 0, len(bids))
	for _, bid := range bids {
		wireBids = append(wireBids, ozon.CPOBid{SKU: bid.SKU, BidRub: bid.BidRub})
	}
	writeErr := s.perfClient.SetSearchPromoBids(ctx, creds, wireBids)
	s.finalizeBidChanges(ctx, changeIDs, writeErr)
	if writeErr != nil {
		return fmt.Errorf("ozon cpo bids: %w", writeErr)
	}

	for _, bid := range bids {
		if mirrorErr := s.queries.UpdateOzonCpoProductBid(ctx, sqlcgen.UpdateOzonCpoProductBidParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Sku:             bid.SKU,
			Bid:             floatToPgNumeric(bid.BidRub),
			BidKind:         textToPgtype("fixed_rub"),
		}); mirrorErr != nil {
			s.logger.Warn().Err(mirrorErr).Int64("sku", bid.SKU).Msg("failed to mirror cpo bid")
		}
	}
	return nil
}

// --- CPO rate / activation (whole-cabinet «Оплата за заказ») ---

// SetCPORate sets the whole-cabinet CPO rate (5/7/9 %) via all_sku_promo/set_bid.
// Tenancy-gated; requires Performance API credentials.
func (s *OzonCampaignActionsService) SetCPORate(ctx context.Context, workspaceID, cabinetID uuid.UUID, ratePct int) error {
	if ratePct != 5 && ratePct != 7 && ratePct != 9 {
		return apperror.New(apperror.ErrValidation, "rate_pct must be one of 5, 7 or 9")
	}
	_, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return err
	}
	if err := s.perfClient.SetAllSKUPromoRate(ctx, creds, ratePct); err != nil {
		return fmt.Errorf("ozon cpo rate: %w", err)
	}
	s.logger.Info().Str("cabinet_id", cabinetID.String()).Int("rate_pct", ratePct).Msg("ozon cpo rate changed")
	return nil
}

// SetCPOActive turns the whole-cabinet CPO promo on or off. Activating goes
// through GetAllSKUPromoRate (the activate endpoint enables the promo and
// reports the current rate, idempotent when already active); deactivating calls
// all_sku_promo/deactivate. Tenancy-gated; requires Performance API creds.
func (s *OzonCampaignActionsService) SetCPOActive(ctx context.Context, workspaceID, cabinetID uuid.UUID, active bool) error {
	_, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return err
	}
	if active {
		rate, actErr := s.perfClient.GetAllSKUPromoRate(ctx, creds)
		if actErr != nil {
			return fmt.Errorf("ozon cpo activate: %w", actErr)
		}
		s.logger.Info().Str("cabinet_id", cabinetID.String()).Int("rate_pct", rate).Msg("ozon cpo promo activated")
		return nil
	}
	if err := s.perfClient.DeactivateAllSKUPromo(ctx, creds); err != nil {
		return fmt.Errorf("ozon cpo deactivate: %w", err)
	}
	s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("ozon cpo promo deactivated")
	return nil
}

// GetBidLimits returns GET /api/client/limits/list, cached per cabinet for an
// hour — the payload is a slow-moving reference table.
func (s *OzonCampaignActionsService) GetBidLimits(ctx context.Context, workspaceID, cabinetID uuid.UUID) (json.RawMessage, error) {
	s.limitsMu.Lock()
	if entry, ok := s.limitsCache[cabinetID]; ok && time.Now().Before(entry.expiresAt) {
		payload := entry.payload
		s.limitsMu.Unlock()
		// Tenancy still applies to cache hits.
		if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID); err != nil {
			return nil, err
		}
		return payload, nil
	}
	s.limitsMu.Unlock()

	_, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, err
	}
	payload, err := s.perfClient.GetBidLimits(ctx, creds)
	if err != nil {
		return nil, fmt.Errorf("ozon bid limits: %w", err)
	}
	s.limitsMu.Lock()
	s.limitsCache[cabinetID] = ozonBidLimitsCacheEntry{payload: payload, expiresAt: time.Now().Add(ozonBidLimitsCacheTTL)}
	s.limitsMu.Unlock()
	return payload, nil
}

// --- helpers ---

func (s *OzonCampaignActionsService) finalizeBidChanges(ctx context.Context, ids []pgtype.UUID, writeErr error) {
	status := domain.OzonBidStatusApplied
	var errText pgtype.Text
	if writeErr != nil {
		status = domain.OzonBidStatusFailed
		errText = textToPgtype(truncateError(writeErr.Error()))
	}
	for _, id := range ids {
		if markErr := s.queries.MarkOzonBidChangeResult(ctx, sqlcgen.MarkOzonBidChangeResultParams{
			ID:     id,
			Status: status,
			Error:  errText,
		}); markErr != nil {
			s.logger.Warn().Err(markErr).Msg("failed to finalize ozon bid change audit row")
		}
	}
}

func validateCPOSkus(skus []int64) error {
	if len(skus) == 0 {
		return apperror.New(apperror.ErrValidation, "skus are required")
	}
	if len(skus) > ozonCPOSkusLimit {
		return apperror.New(apperror.ErrValidation, fmt.Sprintf("at most %d skus per request", ozonCPOSkusLimit))
	}
	for _, sku := range skus {
		if sku <= 0 {
			return apperror.New(apperror.ErrValidation, "skus must be positive")
		}
	}
	return nil
}

func truncateError(message string) string {
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

func ozonBidChangeFromSqlc(row sqlcgen.OzonBidChange) domain.OzonBidChange {
	change := domain.OzonBidChange{
		ID:        uuidFromPgtype(row.ID),
		Kind:      row.Kind,
		OldBidRub: pgNumericToFloatPtr(row.OldBidRub),
		NewBidRub: pgNumericToFloatPtr(row.NewBidRub),
		Reason:    pgTextValue(row.Reason),
		Source:    row.Source,
		Status:    row.Status,
		Error:     pgTextValue(row.Error),
		CreatedAt: row.CreatedAt.Time,
	}
	if row.CampaignID.Valid {
		id := uuidFromPgtype(row.CampaignID)
		change.CampaignID = &id
	}
	if row.SellerCabinetID.Valid {
		id := uuidFromPgtype(row.SellerCabinetID)
		change.SellerCabinetID = &id
	}
	if row.Sku.Valid {
		sku := row.Sku.Int64
		change.SKU = &sku
	}
	if row.AppliedAt.Valid {
		appliedAt := row.AppliedAt.Time
		change.AppliedAt = &appliedAt
	}
	if len(row.DecisionContext) > 0 {
		var decisionContext any
		if err := json.Unmarshal(row.DecisionContext, &decisionContext); err == nil {
			change.DecisionContext = decisionContext
		}
	}
	return change
}
