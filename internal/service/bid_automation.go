package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// BidAutomationService runs the bid engine for all active strategies in a workspace.
type BidAutomationService struct {
	queries       *sqlcgen.Queries
	strategies    *StrategyService
	engine        *BidEngine
	wbClient      *wb.Client
	encryptionKey []byte
	logger        zerolog.Logger
}

func NewBidAutomationService(
	queries *sqlcgen.Queries,
	strategies *StrategyService,
	engine *BidEngine,
	wbClient *wb.Client,
	encryptionKey []byte,
	logger zerolog.Logger,
) *BidAutomationService {
	return &BidAutomationService{
		queries:       queries,
		strategies:    strategies,
		engine:        engine,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "bid_automation").Logger(),
	}
}

// RunForWorkspace executes all active strategies for a workspace. Returns number of bid changes applied.
func (s *BidAutomationService) RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	activeStrategies, err := s.strategies.ListActive(ctx, workspaceID)
	if err != nil {
		return 0, err
	}

	if len(activeStrategies) == 0 {
		return 0, nil
	}

	totalChanges := 0

	for _, strategy := range activeStrategies {
		changes, strategyErr := s.executeStrategy(ctx, workspaceID, strategy)
		if strategyErr != nil {
			s.logger.Error().
				Err(strategyErr).
				Str("strategy_id", strategy.ID.String()).
				Str("strategy_type", strategy.Type).
				Msg("strategy execution failed")
			continue
		}
		totalChanges += changes
	}

	return totalChanges, nil
}

func (s *BidAutomationService) executeStrategy(ctx context.Context, workspaceID uuid.UUID, strategy domain.Strategy) (int, error) {
	if len(strategy.Bindings) == 0 {
		return 0, nil
	}

	// Get WB API token for this cabinet
	token, err := s.decryptCabinetToken(ctx, strategy.SellerCabinetID)
	if err != nil {
		return 0, err
	}

	changes := 0
	params := strategy.Params.Merged()
	dateFrom := time.Now().AddDate(0, 0, -params.LookbackDays)
	dateTo := time.Now()

	for _, binding := range strategy.Bindings {
		if binding.CampaignID == nil {
			continue
		}

		campaign, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(*binding.CampaignID))
		if err != nil {
			continue
		}

		// Get stats for lookback period
		stats, err := s.queries.GetCampaignStatsByDateRange(ctx, sqlcgen.GetCampaignStatsByDateRangeParams{
			CampaignID: uuidToPgtype(*binding.CampaignID),
			Date:       pgtype.Date{Time: dateFrom, Valid: true},
			Date_2:     pgtype.Date{Time: dateTo, Valid: true},
			Limit:      1000,
			Offset:     0,
		})
		if err != nil || len(stats) == 0 {
			continue
		}

		// Aggregate stats over lookback period
		var totalImpressions, totalClicks, totalOrders int64
		var totalSpend, totalRevenue float64
		for _, stat := range stats {
			totalImpressions += stat.Impressions
			totalClicks += stat.Clicks
			totalSpend += float64(stat.Spend)
			if stat.Orders.Valid {
				totalOrders += stat.Orders.Int64
			}
			if stat.Revenue.Valid {
				totalRevenue += float64(stat.Revenue.Int64)
			}
		}

		// Get current bid (use latest bid snapshot or phrase bid)
		currentBid := 100 // default fallback
		latestBid, bidErr := s.queries.GetLatestBidSnapshot(ctx, uuidToPgtype(*binding.CampaignID))
		if bidErr == nil {
			currentBid = int(latestBid.CompetitiveBid)
		}

		decision := s.engine.CalculateBid(strategy, BidContext{
			CurrentBid:  currentBid,
			Impressions: totalImpressions,
			Clicks:      totalClicks,
			Spend:       totalSpend,
			Revenue:     totalRevenue,
			Orders:      totalOrders,
			Placement:   "search",
		})

		if decision == nil {
			continue
		}

		// Apply to WB API
		wbErr := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, decision.Placement, decision.NewBid)
		wbStatus := "applied"
		if wbErr != nil {
			wbStatus = "failed"
			s.logger.Warn().
				Err(wbErr).
				Str("campaign", campaign.Name).
				Int("new_bid", decision.NewBid).
				Msg("failed to apply bid to WB")
		}

		// Record in bid_changes
		var acosVal, roasVal pgtype.Float8
		if decision.ACoS != nil {
			acosVal = pgtype.Float8{Float64: *decision.ACoS, Valid: true}
		}
		if decision.ROAS != nil {
			roasVal = pgtype.Float8{Float64: *decision.ROAS, Valid: true}
		}

		s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(strategy.SellerCabinetID),
			CampaignID:      uuidToPgtype(*binding.CampaignID),
			StrategyID:      uuidToPgtype(strategy.ID),
			Placement:       decision.Placement,
			OldBid:          int32(decision.OldBid),
			NewBid:          int32(decision.NewBid),
			Reason:          decision.Reason,
			Source:          domain.BidSourceStrategy,
			Acos:            acosVal,
			Roas:            roasVal,
			WbStatus:        wbStatus,
		})

		if wbStatus == "applied" {
			changes++
		}

		s.logger.Info().
			Str("strategy", strategy.Name).
			Str("campaign", campaign.Name).
			Int("old_bid", decision.OldBid).
			Int("new_bid", decision.NewBid).
			Str("reason", decision.Reason).
			Str("wb_status", wbStatus).
			Msg("bid change processed")
	}

	return changes, nil
}

func (s *BidAutomationService) decryptCabinetToken(ctx context.Context, cabinetID uuid.UUID) (string, error) {
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return "", err
	}
	return crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
}
