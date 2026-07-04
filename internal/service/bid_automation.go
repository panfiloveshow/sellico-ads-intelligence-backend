package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const minTodayClicksForBidIncrease = int64(20)

// BidAutomationService runs the bid engine for all active strategies in a workspace.
type BidAutomationService struct {
	queries       *sqlcgen.Queries
	strategies    *StrategyService
	engine        *BidEngine
	wbClient      *wb.Client
	encryptionKey []byte
	economics     UnitEconomicsReadinessProvider
	logger        zerolog.Logger
}

type BidAutomationOption func(*BidAutomationService)

func WithUnitEconomicsReadinessProvider(provider UnitEconomicsReadinessProvider) BidAutomationOption {
	return func(s *BidAutomationService) {
		s.economics = provider
	}
}

func NewBidAutomationService(
	queries *sqlcgen.Queries,
	strategies *StrategyService,
	engine *BidEngine,
	wbClient *wb.Client,
	encryptionKey []byte,
	logger zerolog.Logger,
	opts ...BidAutomationOption,
) *BidAutomationService {
	svc := &BidAutomationService{
		queries:       queries,
		strategies:    strategies,
		engine:        engine,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "bid_automation").Logger(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
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

	lastAutoSync, err := s.latestWorkspaceAutoSync(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	if reason := wbAPIAutomationGuardrailReason(lastAutoSync, time.Now()); reason != "" {
		s.logger.Warn().
			Str("workspace_id", workspaceID.String()).
			Str("reason", reason).
			Msg("skipping bid automation because latest WB sync is not safe for automated actions")
		return 0, nil
	}

	totalChanges := 0

	for _, strategy := range activeStrategies {
		// Repricer (price_*) strategies are handled by RepricerService, not the bid engine.
		if domain.IsPriceStrategy(strategy.Type) {
			continue
		}
		if reason := strategyAutomationSkipReason(strategy); reason != "" {
			s.logger.Info().
				Str("workspace_id", workspaceID.String()).
				Str("strategy_id", strategy.ID.String()).
				Str("strategy_type", strategy.Type).
				Str("reason", reason).
				Msg("skipping bid automation because strategy is not allowed to auto-apply actions")
			continue
		}
		if reason, guardErr := s.wbActionRateLimitGuardrail(ctx, strategy.SellerCabinetID); guardErr != nil {
			s.logger.Warn().
				Err(guardErr).
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", strategy.SellerCabinetID.String()).
				Msg("skipping bid automation because WB action rate limit guard could not be loaded")
			continue
		} else if reason != "" {
			s.logger.Warn().
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", strategy.SellerCabinetID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because WB campaign actions are cooling down")
			continue
		}

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

func strategyAutomationSkipReason(strategy domain.Strategy) string {
	level := strategy.Params.Merged().AutomationLevel
	if level < 1 || level > 4 {
		return fmt.Sprintf("automation_level %d is invalid", level)
	}
	if level < 3 {
		return fmt.Sprintf("automation_level %d is analytics/semi-auto only", level)
	}
	return ""
}

func (s *BidAutomationService) latestWorkspaceAutoSync(ctx context.Context, workspaceID uuid.UUID) (*domain.SellerCabinetAutoSyncSummary, error) {
	rows, err := s.queries.ListJobRunsByWorkspace(ctx, sqlcgen.ListJobRunsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          1,
		Offset:         0,
		TaskTypeFilter: textToPgtype("wb:sync_workspace"),
		StatusFilter:   pgtype.Text{},
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return sellerCabinetAutoSyncSummaryFromJobRun(rows[0]), nil
}

func wbAPIAutomationGuardrailReason(sync *domain.SellerCabinetAutoSyncSummary, now time.Time) string {
	if sync == nil {
		return ""
	}
	if sync.RateLimited {
		if sync.NextAllowedAt != nil && now.Before(*sync.NextAllowedAt) {
			return fmt.Sprintf("WB API rate limit is active until %s", sync.NextAllowedAt.UTC().Format(time.RFC3339))
		}
		return "latest WB auto-sync was rate limited"
	}
	if sync.WBErrors > 0 {
		return fmt.Sprintf("latest WB auto-sync reported %d WB API errors", sync.WBErrors)
	}
	if sync.ResultState == "failed" || sync.ResultState == "partial" {
		return fmt.Sprintf("latest WB auto-sync result is %s", sync.ResultState)
	}
	return ""
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
		if reason, guardErr := s.wbActionRateLimitGuardrail(ctx, strategy.SellerCabinetID); guardErr != nil {
			s.logger.Warn().
				Err(guardErr).
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because WB action rate limit guard could not be loaded")
			continue
		} else if reason != "" {
			s.logger.Warn().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).
				Msg("stopping bid automation strategy because WB campaign actions are cooling down")
			break
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
		if stale, age := campaignStatsAreStale(stats, params.MaxDataAgeHours, time.Now()); stale {
			s.logger.Warn().
				Str("campaign_id", binding.CampaignID.String()).
				Dur("age", age).
				Int("max_data_age_hours", params.MaxDataAgeHours).
				Msg("skipping bid automation because campaign stats are stale")
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

		currentBid, ok, bidErr := currentBidFromCampaignPhrases(ctx, s.queries, *binding.CampaignID)
		if bidErr != nil {
			s.logger.Warn().
				Err(bidErr).
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because current bid could not be loaded")
			continue
		}
		if !ok {
			s.logger.Warn().
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because real current bid is unavailable or ambiguous")
			continue
		}

		decision := s.engine.CalculateBid(strategy, BidContext{
			CurrentBid:        currentBid,
			Impressions:       totalImpressions,
			Clicks:            totalClicks,
			Spend:             totalSpend,
			Revenue:           totalRevenue,
			Orders:            totalOrders,
			Placement:         "search",
			IncreaseGuardrail: s.bidIncreaseGuardrail(ctx, strategy, binding, *binding.CampaignID, currentBid),
		})

		if decision == nil {
			continue
		}
		if reason := recentClicksIncreaseGuardrailReason(stats, decision, time.Now()); reason != "" {
			s.logger.Info().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because recent click guardrail blocked the increase")
			continue
		}
		if reason := dailyBudgetIncreaseGuardrailReason(campaign, stats, decision, time.Now()); reason != "" {
			s.logger.Info().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because daily budget guardrail blocked the increase")
			continue
		}
		if reason, err := s.bidActionGuardrail(ctx, strategy, binding, *binding.CampaignID, decision); err != nil {
			s.logger.Warn().
				Err(err).
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because guardrail state could not be loaded")
			continue
		} else if reason != "" {
			s.logger.Info().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because action guardrail blocked the change")
			continue
		}

		// Apply to WB API
		wbErr := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, decision.Placement, decision.NewBid)
		wbStatus := "applied"
		if wbErr != nil {
			wbStatus = "failed"
			s.recordWBActionRateLimitFromError(ctx, strategy.SellerCabinetID, wbErr)
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
			ProductID:       uuidToPgtypePtr(binding.ProductID),
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

		if isRateLimitIssueFromError(wbErr) {
			s.logger.Warn().
				Str("strategy_id", strategy.ID.String()).
				Str("seller_cabinet_id", strategy.SellerCabinetID.String()).
				Msg("stopping bid automation strategy after WB rate limit response")
			break
		}
	}

	return changes, nil
}

func (s *BidAutomationService) wbActionRateLimitGuardrail(ctx context.Context, sellerCabinetID uuid.UUID) (string, error) {
	limit, err := s.queries.GetWBAPIRateLimit(ctx, uuidToPgtype(sellerCabinetID), wbEndpointCampaignActions)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return wbEndpointRateLimitBlockReason(wbEndpointCampaignActions, limit, time.Now().UTC()), nil
}

func (s *BidAutomationService) recordWBActionRateLimitFromError(ctx context.Context, sellerCabinetID uuid.UUID, err error) {
	if !isRateLimitIssueFromError(err) {
		return
	}
	delay := wbEndpointFallbackDelay(wbEndpointCampaignActions)
	next := time.Now().UTC().Add(delay)
	lastError := strings.TrimSpace(err.Error())
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}
	if upsertErr := s.queries.UpsertWBAPIRateLimit(ctx, sqlcgen.UpsertWBAPIRateLimitParams{
		SellerCabinetID:   uuidToPgtype(sellerCabinetID),
		EndpointKey:       wbEndpointCampaignActions,
		NextAllowedAt:     pgtype.Timestamptz{Time: next, Valid: true},
		RetryAfterSeconds: int32(delay.Seconds()),
		LastStatus:        429,
		LastError:         pgtype.Text{String: lastError, Valid: lastError != ""},
	}); upsertErr != nil {
		s.logger.Warn().
			Err(upsertErr).
			Str("seller_cabinet_id", sellerCabinetID.String()).
			Str("endpoint", wbEndpointCampaignActions).
			Msg("failed to persist WB action rate limit")
	}
}

func isRateLimitIssueFromError(err error) bool {
	return err != nil && isRateLimitIssue(err.Error())
}

func campaignStatsAreStale(stats []sqlcgen.CampaignStat, maxAgeHours int, now time.Time) (bool, time.Duration) {
	if maxAgeHours <= 0 {
		return false, 0
	}
	latest, ok := latestCampaignStatsCreatedAt(stats)
	if !ok {
		return true, 0
	}
	age := now.Sub(latest)
	return age > time.Duration(maxAgeHours)*time.Hour, age
}

func latestCampaignStatsCreatedAt(stats []sqlcgen.CampaignStat) (time.Time, bool) {
	var latest time.Time
	for _, stat := range stats {
		if !stat.CreatedAt.Valid {
			continue
		}
		candidate := stat.CreatedAt.Time
		if latest.IsZero() || candidate.After(latest) {
			latest = candidate
		}
	}
	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func (s *BidAutomationService) bidActionGuardrail(ctx context.Context, strategy domain.Strategy, binding domain.StrategyBinding, campaignID uuid.UUID, decision *BidDecision) (string, error) {
	params := strategy.Params.Merged()
	if params.CooldownMinutes <= 0 && params.MaxChangesPerDay <= 0 {
		return "", nil
	}

	productIDs, err := s.productIDsForBidGuardrail(ctx, binding, campaignID)
	if err != nil {
		return "", err
	}

	now := time.Now()
	earliestSince := now
	if params.CooldownMinutes > 0 {
		earliestSince = minTime(earliestSince, now.Add(-time.Duration(params.CooldownMinutes)*time.Minute))
	}
	if params.MaxChangesPerDay > 0 {
		earliestSince = minTime(earliestSince, now.Add(-24*time.Hour))
	}

	changes, err := s.queries.ListBidChangesByWorkspaceSince(ctx, sqlcgen.ListBidChangesByWorkspaceSinceParams{
		WorkspaceID: uuidToPgtype(strategy.WorkspaceID),
		Since:       pgtype.Timestamptz{Time: earliestSince, Valid: true},
		Limit:       2000,
		Offset:      0,
	})
	if err != nil {
		return "", err
	}

	if params.CooldownMinutes > 0 {
		cooldownSince := now.Add(-time.Duration(params.CooldownMinutes) * time.Minute)
		if recentBidChangeExists(changes, productIDs, campaignID, decision.Placement, cooldownSince) {
			return "bid cooldown is active for campaign/product", nil
		}
	}

	if params.MaxChangesPerDay > 0 {
		daySince := now.Add(-24 * time.Hour)
		if countRecentBidChanges(changes, productIDs, campaignID, decision.Placement, daySince) >= params.MaxChangesPerDay {
			return "max_changes_per_day reached for campaign/product", nil
		}
	}

	return "", nil
}

func recentClicksIncreaseGuardrailReason(stats []sqlcgen.CampaignStat, decision *BidDecision, today time.Time) string {
	if decision == nil || decision.NewBid <= decision.OldBid {
		return ""
	}
	clicks, ok := campaignClicksForDate(stats, today)
	if !ok {
		return "today campaign click evidence is unavailable for bid increase"
	}
	if clicks < minTodayClicksForBidIncrease {
		return fmt.Sprintf("today clicks %d below bid increase minimum %d", clicks, minTodayClicksForBidIncrease)
	}
	return ""
}

func dailyBudgetIncreaseGuardrailReason(campaign sqlcgen.Campaign, stats []sqlcgen.CampaignStat, decision *BidDecision, today time.Time) string {
	if decision == nil || decision.NewBid <= decision.OldBid {
		return ""
	}
	if !campaign.DailyBudget.Valid || campaign.DailyBudget.Int64 <= 0 {
		return ""
	}

	todaySpend, ok := campaignSpendForDate(stats, today)
	if !ok {
		return "today campaign spend is unavailable for daily budget guardrail"
	}
	if todaySpend >= campaign.DailyBudget.Int64 {
		return fmt.Sprintf("today spend %d reached daily budget %d", todaySpend, campaign.DailyBudget.Int64)
	}
	if projectedSpend, _ := projectedTodayBudgetPace(campaign.DailyBudget.Int64, todaySpend, today, today, today); projectedSpend != nil && *projectedSpend >= campaign.DailyBudget.Int64 {
		return fmt.Sprintf("projected today spend %d reaches daily budget %d", *projectedSpend, campaign.DailyBudget.Int64)
	}
	return ""
}

func campaignClicksForDate(stats []sqlcgen.CampaignStat, target time.Time) (int64, bool) {
	targetDay := normalizeStatDate(target)
	var clicks int64
	found := false
	for _, stat := range stats {
		if !stat.Date.Valid || !normalizeStatDate(stat.Date.Time).Equal(targetDay) {
			continue
		}
		clicks += stat.Clicks
		found = true
	}
	return clicks, found
}

func campaignSpendForDate(stats []sqlcgen.CampaignStat, target time.Time) (int64, bool) {
	targetDay := normalizeStatDate(target)
	var spend int64
	found := false
	for _, stat := range stats {
		if !stat.Date.Valid || !normalizeStatDate(stat.Date.Time).Equal(targetDay) {
			continue
		}
		spend += stat.Spend
		found = true
	}
	return spend, found
}

func (s *BidAutomationService) productIDsForBidGuardrail(ctx context.Context, binding domain.StrategyBinding, campaignID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if binding.ProductID != nil {
		return map[uuid.UUID]struct{}{*binding.ProductID: {}}, nil
	}
	products, err := s.productsForBidGuardrail(ctx, binding, campaignID)
	if err != nil {
		return nil, err
	}
	result := make(map[uuid.UUID]struct{}, len(products))
	for _, product := range products {
		result[uuidFromPgtype(product.ID)] = struct{}{}
	}
	return result, nil
}

func recentBidChangeExists(changes []sqlcgen.BidChange, productIDs map[uuid.UUID]struct{}, campaignID uuid.UUID, placement string, since time.Time) bool {
	return countRecentBidChanges(changes, productIDs, campaignID, placement, since) > 0
}

func countRecentBidChanges(changes []sqlcgen.BidChange, productIDs map[uuid.UUID]struct{}, campaignID uuid.UUID, placement string, since time.Time) int {
	count := 0
	for _, change := range changes {
		if change.WbStatus != "applied" || change.Placement != placement || !change.CreatedAt.Valid || change.CreatedAt.Time.Before(since) {
			continue
		}
		if bidChangeAffectsProducts(change, productIDs, campaignID) {
			count++
		}
	}
	return count
}

func bidChangeAffectsProducts(change sqlcgen.BidChange, productIDs map[uuid.UUID]struct{}, campaignID uuid.UUID) bool {
	if !change.ProductID.Valid {
		return change.CampaignID.Valid && uuidFromPgtype(change.CampaignID) == campaignID
	}
	if len(productIDs) == 0 {
		return true
	}
	_, ok := productIDs[uuidFromPgtype(change.ProductID)]
	return ok
}

func (s *BidAutomationService) bidIncreaseGuardrail(ctx context.Context, strategy domain.Strategy, binding domain.StrategyBinding, campaignID uuid.UUID, currentBid int) *BidIncreaseGuardrail {
	params := strategy.Params.Merged()
	if reason, err := s.extensionBidMismatchGuardrail(ctx, strategy.WorkspaceID, campaignID, currentBid); err != nil {
		s.logger.Warn().
			Err(err).
			Str("strategy_id", strategy.ID.String()).
			Str("campaign_id", campaignID.String()).
			Msg("bid increase blocked because live cabinet bid evidence could not be loaded")
		return &BidIncreaseGuardrail{Reason: "live cabinet bid evidence could not be loaded"}
	} else if reason != "" {
		return &BidIncreaseGuardrail{Reason: reason}
	}

	if params.AllowIncreaseWithoutStock {
		return &BidIncreaseGuardrail{Allowed: true}
	}

	products, err := s.productsForBidGuardrail(ctx, binding, campaignID)
	if err != nil {
		s.logger.Warn().
			Err(err).
			Str("strategy_id", strategy.ID.String()).
			Str("campaign_id", campaignID.String()).
			Msg("bid increase blocked because product stock links could not be loaded")
		return &BidIncreaseGuardrail{Reason: "real product stock could not be loaded"}
	}
	if len(products) == 0 {
		return &BidIncreaseGuardrail{Reason: "campaign has no real product link with stock data"}
	}

	productIDs := make([]uuid.UUID, 0, len(products))
	wbProductIDs := make([]int64, 0, len(products))
	for _, product := range products {
		productID := uuidFromPgtype(product.ID)
		productIDs = append(productIDs, productID)
		wbProductIDs = append(wbProductIDs, product.WbProductID)
		ev := latestProductStockEvidence(ctx, s.queries, product.ID)
		if !ev.OK {
			return &BidIncreaseGuardrail{Reason: "real product stock evidence is unavailable"}
		}
		// When the quantity is unknown (delivery_data confirms presence but not count)
		// we cannot evaluate MinStockForIncrease; presence alone clears this gate.
		if ev.QuantityKnown && int(ev.Stock) < params.MinStockForIncrease {
			return &BidIncreaseGuardrail{Reason: "real product stock is below min_stock_for_increase"}
		}
		if reason := productReputationBidIncreaseBlockReason(product); reason != "" {
			return &BidIncreaseGuardrail{Reason: reason}
		}
	}

	if s.economics == nil {
		return &BidIncreaseGuardrail{Reason: "unit economics readiness provider is not configured"}
	}

	readiness, err := s.economics.CheckBidIncreaseReadiness(ctx, UnitEconomicsReadinessInput{
		WorkspaceID:     strategy.WorkspaceID,
		SellerCabinetID: strategy.SellerCabinetID,
		ProductIDs:      productIDs,
		WBProductIDs:    wbProductIDs,
	})
	if err != nil {
		s.logger.Warn().
			Err(err).
			Str("strategy_id", strategy.ID.String()).
			Str("campaign_id", campaignID.String()).
			Msg("bid increase blocked because unit economics readiness could not be loaded")
		return &BidIncreaseGuardrail{Reason: "unit economics readiness could not be loaded"}
	}
	if !readiness.AllowsBidIncrease() {
		return &BidIncreaseGuardrail{Reason: readiness.BlockReason()}
	}

	return &BidIncreaseGuardrail{Allowed: true}
}

func (s *BidAutomationService) extensionBidMismatchGuardrail(ctx context.Context, workspaceID, campaignID uuid.UUID, currentBid int) (string, error) {
	rows, err := s.queries.ListExtensionBidSnapshotsFiltered(ctx, sqlcgen.ListExtensionBidSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            20,
		Offset:           0,
		CampaignIDFilter: uuidToPgtype(campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return "", err
	}
	bids := make([]domain.ExtensionBidSnapshot, len(rows))
	for i, row := range rows {
		bids[i] = extensionBidSnapshotFromSqlc(row)
	}
	return extensionBidMismatchGuardrailReason(currentBid, bids), nil
}

func extensionBidMismatchGuardrailReason(currentBid int, bids []domain.ExtensionBidSnapshot) string {
	if currentBid <= 0 {
		return ""
	}
	latestBid, ok := latestVisibleExtensionBid(bids)
	if !ok || latestBid == int64(currentBid) {
		return ""
	}
	return fmt.Sprintf("live cabinet bid %d differs from synced WB API bid %d; refresh sync or recapture cabinet evidence before bid increase", latestBid, currentBid)
}

func latestVisibleExtensionBid(bids []domain.ExtensionBidSnapshot) (int64, bool) {
	var latest domain.ExtensionBidSnapshot
	found := false
	for _, item := range bids {
		if item.VisibleBid == nil || *item.VisibleBid <= 0 {
			continue
		}
		if !found || item.CapturedAt.After(latest.CapturedAt) {
			latest = item
			found = true
		}
	}
	if !found || latest.VisibleBid == nil {
		return 0, false
	}
	return *latest.VisibleBid, true
}

func (s *BidAutomationService) productsForBidGuardrail(ctx context.Context, binding domain.StrategyBinding, campaignID uuid.UUID) ([]sqlcgen.Product, error) {
	if binding.ProductID != nil {
		product, err := s.queries.GetProductByID(ctx, uuidToPgtype(*binding.ProductID))
		if err != nil {
			return nil, err
		}
		return []sqlcgen.Product{product}, nil
	}

	links, err := s.queries.ListCampaignProductsByCampaign(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return nil, err
	}

	products := make([]sqlcgen.Product, 0, len(links))
	for _, link := range links {
		if !link.ProductID.Valid {
			continue
		}
		product, err := s.queries.GetProductByID(ctx, link.ProductID)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, nil
}

func minTime(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

func (s *BidAutomationService) decryptCabinetToken(ctx context.Context, cabinetID uuid.UUID) (string, error) {
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return "", err
	}
	return crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
}
