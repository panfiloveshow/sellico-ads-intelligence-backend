package service

import (
	"context"
	"encoding/json"
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

	settingsRaw, err := s.queries.GetWorkspaceSettings(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, fmt.Errorf("load workspace automation settings: %w", err)
	}
	workspaceAutoApplyBlockReason, settingsErr := workspaceAutomationGuardrailReason(settingsRaw)
	if settingsErr != nil {
		return 0, settingsErr
	}

	totalChanges := 0
	var runErrors []error

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
		shadowMode := strategy.Params.Merged().AutomationLevel < 3
		if !shadowMode && workspaceAutoApplyBlockReason != "" {
			s.logger.Warn().
				Str("workspace_id", workspaceID.String()).
				Str("strategy_id", strategy.ID.String()).
				Str("reason", workspaceAutoApplyBlockReason).
				Msg("skipping live bid automation because workspace auto-apply is disabled")
			continue
		}
		state, stateErr := s.queries.GetSellerCabinetSyncState(ctx, uuidToPgtype(strategy.SellerCabinetID))
		if stateErr != nil && !errors.Is(stateErr, pgx.ErrNoRows) {
			runErrors = append(runErrors, fmt.Errorf("strategy %s cabinet sync state: %w", strategy.ID, stateErr))
			continue
		}
		if reason := sellerCabinetAutomationGuardrailReason(state, stateErr == nil, strategy.Params.Merged().MaxDataAgeHours, time.Now()); reason != "" {
			s.logger.Warn().Str("workspace_id", workspaceID.String()).Str("seller_cabinet_id", strategy.SellerCabinetID.String()).
				Str("reason", reason).Msg("skipping bid automation because cabinet sync state is not ready")
			continue
		}
		if reason, guardErr := s.wbActionRateLimitGuardrail(ctx, strategy.SellerCabinetID); !shadowMode && guardErr != nil {
			s.logger.Warn().
				Err(guardErr).
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", strategy.SellerCabinetID.String()).
				Msg("skipping bid automation because WB action rate limit guard could not be loaded")
			continue
		} else if !shadowMode && reason != "" {
			s.logger.Warn().
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", strategy.SellerCabinetID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because WB campaign actions are cooling down")
			continue
		}

		changes, strategyErr := s.executeStrategy(ctx, workspaceID, strategy, !shadowMode)
		if strategyErr != nil {
			s.logger.Error().
				Err(strategyErr).
				Str("strategy_id", strategy.ID.String()).
				Str("strategy_type", strategy.Type).
				Msg("strategy execution failed")
			runErrors = append(runErrors, fmt.Errorf("strategy %s: %w", strategy.ID, strategyErr))
			continue
		}
		totalChanges += changes
	}

	return totalChanges, errors.Join(runErrors...)
}

func sellerCabinetAutomationGuardrailReason(state sqlcgen.SellerCabinetSyncState, found bool, maxAgeHours int, now time.Time) string {
	if !found {
		return "seller cabinet has no completed sync state"
	}
	if state.Status != "ready" {
		return fmt.Sprintf("seller cabinet sync status is %s", state.Status)
	}
	if state.RateLimited || state.WBErrorCount > 0 || state.IssueCount > 0 {
		return "seller cabinet sync completed with unresolved issues"
	}
	if !state.CompletedAt.Valid {
		return "seller cabinet sync completion time is unavailable"
	}
	if maxAgeHours <= 0 {
		maxAgeHours = domain.DefaultStrategyParams().MaxDataAgeHours
	}
	cutoff := now.Add(-time.Duration(maxAgeHours) * time.Hour)
	if state.CompletedAt.Time.Before(cutoff) {
		return "seller cabinet sync state is stale"
	}
	if !state.DataThroughDate.Valid {
		return "seller cabinet sync data-through date is unavailable"
	}
	cutoffDate := normalizeStatDate(cutoff)
	if normalizeStatDate(state.DataThroughDate.Time).Before(cutoffDate) {
		return "seller cabinet synced data does not cover the required freshness window"
	}
	return ""
}

func workspaceAutomationGuardrailReason(raw []byte) (string, error) {
	var settings domain.WorkspaceSettings
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", fmt.Errorf("parse workspace automation settings: %w", err)
		}
	}
	if settings.Automation == nil || !settings.Automation.Enabled {
		return "workspace automation is not explicitly enabled", nil
	}
	if settings.Automation.ManualHold {
		reason := strings.TrimSpace(settings.Automation.HoldReason)
		if reason == "" {
			reason = "manual hold is active"
		}
		return reason, nil
	}
	return "", nil
}

func strategyAutomationSkipReason(strategy domain.Strategy) string {
	if strategy.Type == domain.StrategyTypeRecommendation {
		return "recommendation strategy is not executable; explicit approval is required"
	}
	level := strategy.Params.Merged().AutomationLevel
	if level < 1 || level > 4 {
		return fmt.Sprintf("automation_level %d is invalid", level)
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

func (s *BidAutomationService) executeStrategy(ctx context.Context, workspaceID uuid.UUID, strategy domain.Strategy, applyToWB bool) (int, error) {
	if len(strategy.Bindings) == 0 {
		return 0, nil
	}

	// Get WB API token for this cabinet
	token, err := s.decryptCabinetToken(ctx, strategy.SellerCabinetID)
	if err != nil {
		return 0, err
	}

	changes := 0
	var bindingErrors []error
	params := strategy.Params.Merged()
	now := time.Now()
	dateFrom := now.AddDate(0, 0, -params.LookbackDays)
	dateTo := now
	decisionDateTo := lastClosedCampaignStatDate(now)

	for _, binding := range strategy.Bindings {
		if binding.CampaignID == nil {
			continue
		}
		if applyToWB {
			if reason, guardErr := s.wbActionRateLimitGuardrail(ctx, strategy.SellerCabinetID); guardErr != nil {
				s.logger.Warn().
					Err(guardErr).
					Str("strategy_id", strategy.ID.String()).
					Str("campaign_id", binding.CampaignID.String()).
					Msg("skipping bid automation because WB action rate limit guard could not be loaded")
				bindingErrors = append(bindingErrors, fmt.Errorf("binding %s rate-limit guard: %w", binding.ID, guardErr))
				continue
			} else if reason != "" {
				s.logger.Warn().
					Str("strategy_id", strategy.ID.String()).
					Str("campaign_id", binding.CampaignID.String()).
					Str("reason", reason).
					Msg("stopping bid automation strategy because WB campaign actions are cooling down")
				break
			}
		}

		campaign, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(*binding.CampaignID))
		if err != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s campaign: %w", binding.ID, err))
			continue
		}
		if reason := automationCampaignSkipReason(campaign); reason != "" {
			s.logger.Info().Str("campaign_id", binding.CampaignID.String()).Str("reason", reason).
				Msg("skipping campaign that is not safe for automatic product bid updates")
			continue
		}
		targetNMID, targetErr := s.bidTargetNMID(ctx, strategy, campaign, binding)
		if targetErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s target: %w", binding.ID, targetErr))
			continue
		}
		placement, placementErr := automationBidPlacement(campaign)
		if placementErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s placement: %w", binding.ID, placementErr))
			continue
		}

		// Keep the campaign aggregate for campaign-wide financial limits. The
		// decision itself must use exact product attribution whenever the binding
		// targets a product, otherwise one strong SKU can raise another SKU's bid.
		campaignStats, err := s.queries.GetCampaignStatsByDateRange(ctx, sqlcgen.GetCampaignStatsByDateRangeParams{
			CampaignID: uuidToPgtype(*binding.CampaignID),
			Date:       pgtype.Date{Time: dateFrom, Valid: true},
			Date_2:     pgtype.Date{Time: dateTo, Valid: true},
			Limit:      1000,
			Offset:     0,
		})
		if err != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s stats: %w", binding.ID, err))
			continue
		}
		stats := campaignStats
		if binding.ProductID != nil {
			productStats, productStatsErr := s.queries.GetProductStatsByProductCampaignDateRange(ctx, sqlcgen.GetProductStatsByProductCampaignDateRangeParams{
				ProductID:  uuidToPgtype(*binding.ProductID),
				CampaignID: uuidToPgtype(*binding.CampaignID),
				DateFrom:   pgtype.Date{Time: dateFrom, Valid: true},
				DateTo:     pgtype.Date{Time: dateTo, Valid: true},
			})
			if productStatsErr != nil {
				bindingErrors = append(bindingErrors, fmt.Errorf("binding %s product stats: %w", binding.ID, productStatsErr))
				continue
			}
			stats = campaignStatsFromProductStats(productStats)
		}
		if len(stats) == 0 {
			continue
		}

		// Efficiency metrics must use completed calendar days only. WB's current-day
		// orders and revenue can lag spend, so including today would bias ACoS, ROAS,
		// CPO and anti-waste decisions toward unnecessary bid reductions. Keep the
		// full stats slice below for explicit intraday pacing/click guardrails.
		decisionStats := closedCampaignStats(stats, now)
		if len(decisionStats) == 0 {
			s.logger.Info().
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because no completed-day campaign stats are available")
			continue
		}
		if stale, age := campaignStatsAreStale(decisionStats, params.MaxDataAgeHours, now); stale {
			s.logger.Warn().
				Str("campaign_id", binding.CampaignID.String()).
				Dur("age", age).
				Int("max_data_age_hours", params.MaxDataAgeHours).
				Msg("skipping bid automation because completed-day campaign stats are stale")
			continue
		}

		// Aggregate completed-day stats over the lookback period.
		totalImpressions, totalClicks, totalOrders, totalSpend, totalRevenue := aggregateBidPerformance(decisionStats)

		currentBid, observedAt, ok, bidErr := currentBidObservation(ctx, s.queries, *binding.CampaignID, binding.ProductID, placement)
		if bidErr != nil {
			s.logger.Warn().
				Err(bidErr).
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because current bid could not be loaded")
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s current bid: %w", binding.ID, bidErr))
			continue
		}
		if !ok {
			s.logger.Warn().
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because real current bid is unavailable or ambiguous")
			continue
		}

		bidCtx := BidContext{
			CurrentBid:        currentBid,
			Impressions:       totalImpressions,
			Clicks:            totalClicks,
			Spend:             totalSpend,
			Revenue:           totalRevenue,
			Orders:            totalOrders,
			Placement:         placement,
			IncreaseGuardrail: s.bidIncreaseGuardrail(ctx, strategy, binding, *binding.CampaignID, currentBid),
			DecisionTime:      now,
		}
		var dayparting daypartingRunState
		if strategy.Type == domain.StrategyTypeDayparting {
			dayparting, err = s.loadDaypartingRunState(ctx, strategy, binding, *binding.CampaignID, placement, currentBid, now)
			if err != nil {
				bindingErrors = append(bindingErrors, fmt.Errorf("binding %s dayparting state: %w", binding.ID, err))
				continue
			}
			bidCtx.DaypartingBaselineBid = dayparting.BaselineBid
			bidCtx.DaypartingSlotApplied = dayparting.SlotApplied
		}
		if strategy.Type == domain.StrategyTypeSearchPlaybook {
			s.augmentSearchPlaybookContext(ctx, *binding.CampaignID, binding, decisionStats, &bidCtx)
		}

		decision := s.engine.CalculateBid(strategy, bidCtx)

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
		if reason := dailyBudgetIncreaseGuardrailReason(campaign, campaignStats, decision, time.Now()); reason != "" {
			s.logger.Info().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because daily budget guardrail blocked the increase")
			continue
		}
		if reason, financialErr := s.financialIncreaseGuardrail(ctx, campaign, campaignStats, decision, now); financialErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s financial guard: %w", binding.ID, financialErr))
			continue
		} else if reason != "" {
			s.logger.Info().Str("strategy_id", strategy.ID.String()).Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).Msg("skipping bid automation because financial guardrail blocked the increase")
			continue
		}
		if reason, err := s.bidActionGuardrail(ctx, strategy, binding, *binding.CampaignID, decision); err != nil {
			s.logger.Warn().
				Err(err).
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Msg("skipping bid automation because guardrail state could not be loaded")
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s action guard: %w", binding.ID, err))
			continue
		} else if reason != "" {
			s.logger.Info().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Str("reason", reason).
				Msg("skipping bid automation because action guardrail blocked the change")
			continue
		}

		if reason, minimumErr := s.minimumBidDecreaseGuardrail(ctx, token, campaign, targetNMID, decision); minimumErr != nil {
			s.recordWBActionRateLimitFromError(ctx, strategy.SellerCabinetID, minimumErr)
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s minimum bid guard: %w", binding.ID, minimumErr))
			continue
		} else if reason != "" {
			s.logger.Info().Str("campaign_id", binding.CampaignID.String()).Str("reason", reason).
				Msg("skipping bid decrease because dynamic WB minimum bid blocked it")
			continue
		}
		oldBid32, oldBidErr := checkedInt32(decision.OldBid)
		if oldBidErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s old bid: %w", binding.ID, oldBidErr))
			continue
		}
		newBid32, newBidErr := checkedInt32(decision.NewBid)
		if newBidErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s new bid: %w", binding.ID, newBidErr))
			continue
		}
		if !applyToWB {
			if shadowErr := s.recordShadowBidDecision(ctx, workspaceID, strategy, binding, campaign, targetNMID, decision, bidCtx, observedAt, dateFrom, decisionDateTo, oldBid32, newBid32); shadowErr != nil {
				bindingErrors = append(bindingErrors, fmt.Errorf("binding %s record shadow decision: %w", binding.ID, shadowErr))
				continue
			}
			s.logger.Info().
				Str("strategy_id", strategy.ID.String()).
				Str("campaign_id", binding.CampaignID.String()).
				Int("old_bid", decision.OldBid).
				Int("proposed_bid", decision.NewBid).
				Msg("recorded shadow bid decision without calling WB")
			continue
		}

		automationKey, observationKey := automationBidActionKeys(campaign, targetNMID, decision, observedAt)
		claimed, claimErr := s.queries.ClaimAutomationBidAction(ctx, sqlcgen.ClaimAutomationBidActionParams{
			AutomationKey: automationKey, AutomationObservationKey: observationKey,
			WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: campaign.SellerCabinetID,
			CampaignID: campaign.ID, ProductID: uuidToPgtypePtr(binding.ProductID), WBCampaignID: campaign.WbCampaignID,
			WBProductID: targetNMID, OldBid: int64(decision.OldBid), NewBid: int64(decision.NewBid), Reason: decision.Reason,
		})
		if claimErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s action claim: %w", binding.ID, claimErr))
			continue
		}
		if !claimed {
			s.logger.Info().Str("automation_key", automationKey).Msg("skipping duplicate automation action")
			continue
		}

		// Apply to WB API. A product binding is always sent as its concrete real NMID.
		wbErr := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), targetNMID, decision.Placement, decision.NewBid)
		wbStatus := "applied"
		var wbResponse []byte
		if wbErr != nil {
			wbStatus = "failed"
			wbResponse, _ = json.Marshal(map[string]string{"error": wbErr.Error()})
			s.recordWBActionRateLimitFromError(ctx, strategy.SellerCabinetID, wbErr)
			s.logger.Warn().
				Err(wbErr).
				Str("campaign", campaign.Name).
				Int("new_bid", decision.NewBid).
				Msg("failed to apply bid to WB")
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s WB bid update: %w", binding.ID, wbErr))
		}
		if completeErr := s.queries.CompleteAutomationBidAction(ctx, sqlcgen.CompleteAutomationBidActionParams{
			AutomationKey: automationKey, Status: wbStatus, WBResponse: wbResponse,
		}); completeErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s complete action claim: %w", binding.ID, completeErr))
		}

		// Record in bid_changes
		var acosVal, roasVal pgtype.Float8
		if decision.ACoS != nil {
			acosVal = pgtype.Float8{Float64: *decision.ACoS, Valid: true}
		}
		if decision.ROAS != nil {
			roasVal = pgtype.Float8{Float64: *decision.ROAS, Valid: true}
		}

		_, recordErr := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(strategy.SellerCabinetID),
			CampaignID:      uuidToPgtype(*binding.CampaignID),
			ProductID:       uuidToPgtypePtr(binding.ProductID),
			StrategyID:      uuidToPgtype(strategy.ID),
			Placement:       decision.Placement,
			OldBid:          oldBid32,
			NewBid:          newBid32,
			Reason:          decision.Reason,
			Source:          domain.BidSourceStrategy,
			Acos:            acosVal,
			Roas:            roasVal,
			WbStatus:        wbStatus,
		})
		if recordErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("binding %s record bid change: %w", binding.ID, recordErr))
		}

		if wbStatus == "applied" {
			changes++
			if strategy.Type == domain.StrategyTypeDayparting {
				if stateErr := s.saveDaypartingRunState(ctx, strategy, binding, *binding.CampaignID, decision, dayparting); stateErr != nil {
					bindingErrors = append(bindingErrors, fmt.Errorf("binding %s save dayparting state: %w", binding.ID, stateErr))
				}
			}
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

	return changes, errors.Join(bindingErrors...)
}

func automationCampaignSkipReason(campaign sqlcgen.Campaign) string {
	if strings.ToLower(strings.TrimSpace(campaign.Status)) != "active" {
		return "campaign is not active"
	}
	if campaign.PaymentType == domain.PaymentTypeCPM && campaign.BidType == domain.BidTypeManual {
		return "manual CPM campaigns require cluster-level automation"
	}
	return ""
}

func automationBidPlacement(campaign sqlcgen.Campaign) (string, error) {
	switch campaign.BidType {
	case domain.BidTypeUnified:
		return "combined", nil
	case domain.BidTypeManual:
		if !campaign.PlacementSearch.Valid || !campaign.PlacementSearch.Bool {
			return "", fmt.Errorf("manual campaign search placement is unavailable or disabled")
		}
		return "search", nil
	default:
		return "", fmt.Errorf("campaign bid type is unavailable; sync the campaign before automation")
	}
}

func (s *BidAutomationService) bidTargetNMID(ctx context.Context, strategy domain.Strategy, campaign sqlcgen.Campaign, binding domain.StrategyBinding) (int64, error) {
	if uuidFromPgtype(campaign.WorkspaceID) != strategy.WorkspaceID || uuidFromPgtype(campaign.SellerCabinetID) != strategy.SellerCabinetID {
		return 0, fmt.Errorf("campaign does not belong to strategy seller cabinet")
	}
	if binding.ProductID == nil {
		return 0, nil
	}
	product, err := s.queries.GetProductByID(ctx, uuidToPgtype(*binding.ProductID))
	if err != nil {
		return 0, err
	}
	if uuidFromPgtype(product.WorkspaceID) != strategy.WorkspaceID || uuidFromPgtype(product.SellerCabinetID) != strategy.SellerCabinetID {
		return 0, fmt.Errorf("product does not belong to strategy seller cabinet")
	}
	if product.WbProductID <= 0 {
		return 0, fmt.Errorf("product has no real WB product id")
	}
	links, err := s.queries.ListCampaignProductsByCampaign(ctx, campaign.ID)
	if err != nil {
		return 0, err
	}
	for _, link := range links {
		if link.ProductID.Valid && uuidFromPgtype(link.ProductID) == *binding.ProductID {
			return product.WbProductID, nil
		}
	}
	return 0, fmt.Errorf("product is not linked to campaign")
}

func currentBidObservation(ctx context.Context, queries *sqlcgen.Queries, campaignID uuid.UUID, productID *uuid.UUID, placement string) (int, time.Time, bool, error) {
	if productID == nil {
		snapshot, ok, err := campaignBidSnapshot(ctx, queries, campaignID, placement)
		return snapshot.Bid, snapshot.CapturedAt, ok, err
	}
	links, err := queries.ListCampaignProductsByCampaign(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return 0, time.Time{}, false, err
	}
	return productBidObservationFromLinks(links, *productID, placement)
}

func productBidObservationFromLinks(links []sqlcgen.CampaignProduct, productID uuid.UUID, placement string) (int, time.Time, bool, error) {
	for _, link := range links {
		if !link.ProductID.Valid || uuidFromPgtype(link.ProductID) != productID {
			continue
		}
		// Product-level automation currently supports the WB search/combined
		// product bid, which is persisted in bid_search for both bid modes.
		if placement != "search" && placement != "combined" {
			return 0, time.Time{}, false, nil
		}
		if !link.BidSearch.Valid || link.BidSearch.Int64 <= 0 || !link.UpdatedAt.Valid {
			return 0, time.Time{}, false, nil
		}
		return int(link.BidSearch.Int64), link.UpdatedAt.Time, true, nil
	}
	return 0, time.Time{}, false, nil
}

func (s *BidAutomationService) minimumBidDecreaseGuardrail(ctx context.Context, token string, campaign sqlcgen.Campaign, targetNMID int64, decision *BidDecision) (string, error) {
	if decision == nil || decision.NewBid >= decision.OldBid {
		return "", nil
	}
	nmIDs := make([]int64, 0, 8)
	if targetNMID > 0 {
		nmIDs = append(nmIDs, targetNMID)
	} else {
		links, err := s.queries.ListCampaignProductsByCampaign(ctx, campaign.ID)
		if err != nil {
			return "", err
		}
		seen := make(map[int64]struct{}, len(links))
		for _, link := range links {
			if !link.ProductID.Valid {
				continue
			}
			product, err := s.queries.GetProductByID(ctx, link.ProductID)
			if err != nil {
				return "", err
			}
			if product.WbProductID <= 0 {
				return "real WB product id is unavailable for dynamic minimum bid", nil
			}
			if _, ok := seen[product.WbProductID]; !ok {
				seen[product.WbProductID] = struct{}{}
				nmIDs = append(nmIDs, product.WbProductID)
			}
		}
	}
	if len(nmIDs) == 0 || strings.TrimSpace(campaign.PaymentType) == "" {
		return "dynamic WB minimum bid inputs are unavailable", nil
	}
	minimumPlacement := minimumBidPlacement(decision.Placement)
	minimums, err := s.wbClient.GetMinimumBids(ctx, token, wb.MinimumBidsRequest{
		AdvertID: campaign.WbCampaignID, NMIDs: nmIDs, PaymentType: campaign.PaymentType, PlacementTypes: []string{minimumPlacement},
	})
	if err != nil {
		return "", err
	}
	return dynamicMinimumBidGuardrailReason(minimums, nmIDs, minimumPlacement, decision.NewBid), nil
}

func minimumBidPlacement(placement string) string {
	if placement == "recommendations" {
		return "recommendation"
	}
	return placement
}

func dynamicMinimumBidGuardrailReason(minimums []wb.WBMinimumBidDTO, nmIDs []int64, placement string, newBid int) string {
	found := make(map[int64]bool, len(nmIDs))
	for _, minimum := range minimums {
		if minimum.Placement != placement || minimum.MinBid <= 0 {
			continue
		}
		found[minimum.NmID] = true
		if int64(newBid) < minimum.MinBid {
			return fmt.Sprintf("new bid %d is below WB minimum %d for nm_id %d", newBid, minimum.MinBid, minimum.NmID)
		}
	}
	for _, nmID := range nmIDs {
		if !found[nmID] {
			return fmt.Sprintf("dynamic WB minimum bid is unavailable for nm_id %d", nmID)
		}
	}
	return ""
}

type daypartingRunState struct {
	ScopeKey    string
	Slot        string
	BaselineBid int
	SlotApplied bool
}

func daypartingScopeKey(binding domain.StrategyBinding) string {
	if binding.ProductID != nil {
		return binding.ProductID.String()
	}
	return "campaign"
}

func daypartingSlot(params domain.StrategyParams, now time.Time) (string, error) {
	timezone := strings.TrimSpace(params.Merged().Timezone)
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", err
	}
	return now.In(location).Format("2006-01-02T15 MST"), nil
}

func (s *BidAutomationService) loadDaypartingRunState(ctx context.Context, strategy domain.Strategy, binding domain.StrategyBinding, campaignID uuid.UUID, placement string, currentBid int, now time.Time) (daypartingRunState, error) {
	slot, err := daypartingSlot(strategy.Params, now)
	if err != nil {
		return daypartingRunState{}, err
	}
	result := daypartingRunState{ScopeKey: daypartingScopeKey(binding), Slot: slot, BaselineBid: currentBid}
	state, err := s.queries.GetDaypartingState(ctx, sqlcgen.GetDaypartingStateParams{
		StrategyID: uuidToPgtype(strategy.ID), CampaignID: uuidToPgtype(campaignID), ScopeKey: result.ScopeKey, Placement: placement,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return daypartingRunState{}, err
	}
	if state.LastSlot == slot {
		result.BaselineBid = int(state.BaselineBid)
		result.SlotApplied = true
		return result, nil
	}
	if int(state.LastTargetBid) == currentBid {
		result.BaselineBid = int(state.BaselineBid)
	}
	return result, nil
}

func (s *BidAutomationService) saveDaypartingRunState(ctx context.Context, strategy domain.Strategy, binding domain.StrategyBinding, campaignID uuid.UUID, decision *BidDecision, state daypartingRunState) error {
	baselineBid, err := checkedInt32(state.BaselineBid)
	if err != nil {
		return fmt.Errorf("dayparting baseline bid: %w", err)
	}
	lastTargetBid, err := checkedInt32(decision.NewBid)
	if err != nil {
		return fmt.Errorf("dayparting target bid: %w", err)
	}
	return s.queries.UpsertDaypartingState(ctx, sqlcgen.UpsertDaypartingStateParams{
		StrategyID: uuidToPgtype(strategy.ID), CampaignID: uuidToPgtype(campaignID), ProductID: uuidToPgtypePtr(binding.ProductID),
		ScopeKey: state.ScopeKey, Placement: decision.Placement, BaselineBid: baselineBid,
		LastTargetBid: lastTargetBid, LastSlot: state.Slot,
	})
}

func automationBidActionKeys(campaign sqlcgen.Campaign, targetNMID int64, decision *BidDecision, observedAt time.Time) (transitionKey, observationKey string) {
	base := fmt.Sprintf("%s:%d:%d:%s:%d:%s", uuidFromPgtype(campaign.SellerCabinetID), campaign.WbCampaignID,
		targetNMID, decision.Placement, decision.OldBid, observedAt.UTC().Format(time.RFC3339Nano))
	return fmt.Sprintf("%s:%d", base, decision.NewBid), base
}

func (s *BidAutomationService) recordShadowBidDecision(
	ctx context.Context,
	workspaceID uuid.UUID,
	strategy domain.Strategy,
	binding domain.StrategyBinding,
	campaign sqlcgen.Campaign,
	targetNMID int64,
	decision *BidDecision,
	bidCtx BidContext,
	observedAt time.Time,
	dateFrom time.Time,
	dateTo time.Time,
	oldBid int32,
	proposedBid int32,
) error {
	transitionKey, _ := automationBidActionKeys(campaign, targetNMID, decision, observedAt)
	metricsJSON, err := json.Marshal(map[string]any{
		"data_mode":       "exact",
		"date_from":       normalizeStatDate(dateFrom).Format("2006-01-02"),
		"date_to":         normalizeStatDate(dateTo).Format("2006-01-02"),
		"impressions":     bidCtx.Impressions,
		"clicks":          bidCtx.Clicks,
		"spend":           bidCtx.Spend,
		"revenue":         bidCtx.Revenue,
		"orders":          bidCtx.Orders,
		"acos":            decision.ACoS,
		"roas":            decision.ROAS,
		"decision_time":   bidCtx.DecisionTime.UTC().Format(time.RFC3339),
		"bid_observed_at": observedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("encode shadow metrics: %w", err)
	}
	return s.queries.UpsertBidDecisionObservation(ctx, sqlcgen.UpsertBidDecisionObservationParams{
		ObservationKey:    transitionKey,
		WorkspaceID:       uuidToPgtype(workspaceID),
		SellerCabinetID:   campaign.SellerCabinetID,
		StrategyID:        uuidToPgtype(strategy.ID),
		StrategyBindingID: uuidToPgtype(binding.ID),
		CampaignID:        campaign.ID,
		ProductID:         uuidToPgtypePtr(binding.ProductID),
		WBCampaignID:      campaign.WbCampaignID,
		WBProductID:       targetNMID,
		Placement:         decision.Placement,
		OldBid:            oldBid,
		ProposedBid:       proposedBid,
		Reason:            decision.Reason,
		Metrics:           metricsJSON,
		AutomationLevel:   boundedInt32(strategy.Params.Merged().AutomationLevel),
		BidObservedAt:     observedAt.UTC(),
	})
}

func (s *BidAutomationService) financialIncreaseGuardrail(ctx context.Context, campaign sqlcgen.Campaign, stats []sqlcgen.CampaignStat, decision *BidDecision, now time.Time) (string, error) {
	if decision == nil || decision.NewBid <= decision.OldBid {
		return "", nil
	}
	limit, err := s.queries.GetCampaignDailyLimit(ctx, campaign.ID)
	limitFound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if reason := campaignSpendCapIncreaseGuardrailReason(campaign, limit, limitFound, stats, decision, now); reason != "" {
		return reason, nil
	}

	balance, err := s.queries.GetLatestSellerAdBalance(ctx, campaign.SellerCabinetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "fresh seller advertising balance is unavailable", nil
	}
	if err != nil {
		return "", err
	}
	return sellerBalanceIncreaseGuardrailReason(balance, decision, now), nil
}

func campaignSpendCapIncreaseGuardrailReason(campaign sqlcgen.Campaign, configured sqlcgen.CampaignDailyLimit, configuredFound bool, stats []sqlcgen.CampaignStat, decision *BidDecision, now time.Time) string {
	if decision == nil || decision.NewBid <= decision.OldBid {
		return ""
	}
	limit := int64(0)
	if configuredFound && configured.Enabled {
		limit = configured.DailyLimit
	} else if campaign.DailyBudget.Valid {
		limit = campaign.DailyBudget.Int64
	}
	if limit <= 0 {
		return "positive real campaign daily limit is unavailable"
	}
	todaySpend, ok := campaignSpendForDate(stats, now)
	if !ok {
		return "today campaign spend is unavailable for configured daily limit"
	}
	if todaySpend >= limit {
		return fmt.Sprintf("today spend %d reached configured campaign daily limit %d", todaySpend, limit)
	}
	return ""
}

func campaignDailyLimitIncreaseGuardrailReason(limit sqlcgen.CampaignDailyLimit, stats []sqlcgen.CampaignStat, decision *BidDecision, now time.Time) string {
	if decision == nil || decision.NewBid <= decision.OldBid || !limit.Enabled {
		return ""
	}
	if limit.DailyLimit <= 0 {
		return "enabled campaign daily limit has no positive real cap"
	}
	todaySpend, ok := campaignSpendForDate(stats, now)
	if !ok {
		return "today campaign spend is unavailable for configured daily limit"
	}
	if todaySpend >= limit.DailyLimit {
		return fmt.Sprintf("today spend %d reached configured campaign daily limit %d", todaySpend, limit.DailyLimit)
	}
	return ""
}

func sellerBalanceIncreaseGuardrailReason(balance sqlcgen.SellerAdBalance, decision *BidDecision, now time.Time) string {
	if decision == nil || decision.NewBid <= decision.OldBid {
		return ""
	}
	if !balance.CapturedAt.Valid {
		return "fresh seller advertising balance is unknown"
	}
	age := now.Sub(balance.CapturedAt.Time)
	if age < 0 {
		return "seller advertising balance capture time is invalid"
	}
	if age > 24*time.Hour {
		return "seller advertising balance is stale"
	}
	if balance.Balance <= 0 {
		return "fresh seller advertising balance is zero"
	}
	return ""
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

func campaignStatsFromProductStats(stats []sqlcgen.ProductStat) []sqlcgen.CampaignStat {
	result := make([]sqlcgen.CampaignStat, 0, len(stats))
	for _, stat := range stats {
		result = append(result, sqlcgen.CampaignStat{
			ID:          stat.ID,
			CampaignID:  stat.CampaignID,
			Date:        stat.Date,
			Impressions: stat.Impressions,
			Clicks:      stat.Clicks,
			Spend:       stat.Spend,
			Orders:      stat.Orders,
			Revenue:     stat.Revenue,
			CreatedAt:   stat.CreatedAt,
			UpdatedAt:   stat.UpdatedAt,
			Atbs:        stat.Atbs,
			Canceled:    stat.Canceled,
			Shks:        stat.Shks,
		})
	}
	return result
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

// closedCampaignStats returns only completed WB calendar days. Advertising spend
// arrives during the current Moscow day before orders and revenue are fully
// attributed, so current-day rows are unsuitable for efficiency decisions.
func closedCampaignStats(stats []sqlcgen.CampaignStat, now time.Time) []sqlcgen.CampaignStat {
	cutoff := normalizeStatDate(moscowTime(now))
	result := make([]sqlcgen.CampaignStat, 0, len(stats))
	for _, stat := range stats {
		if !stat.Date.Valid || !normalizeStatDate(stat.Date.Time).Before(cutoff) {
			continue
		}
		result = append(result, stat)
	}
	return result
}

func aggregateBidPerformance(stats []sqlcgen.CampaignStat) (impressions, clicks, orders int64, spend, revenue float64) {
	for _, stat := range stats {
		impressions += stat.Impressions
		clicks += stat.Clicks
		spend += float64(stat.Spend)
		if stat.Orders.Valid {
			orders += stat.Orders.Int64
		}
		if stat.Revenue.Valid {
			revenue += float64(stat.Revenue.Int64)
		}
	}
	return impressions, clicks, orders, spend, revenue
}

func lastClosedCampaignStatDate(now time.Time) time.Time {
	return normalizeStatDate(moscowTime(now)).AddDate(0, 0, -1)
}

func moscowTime(value time.Time) time.Time {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("MSK", 3*60*60)
	}
	return value.In(location)
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
		if !params.AllowIncreaseWithoutStock {
			ev := latestProductStockEvidence(ctx, s.queries, product.ID)
			if !ev.OK {
				return &BidIncreaseGuardrail{Reason: "real product stock evidence is unavailable"}
			}
			// When the quantity is unknown (delivery_data confirms presence but not count)
			// we cannot evaluate MinStockForIncrease; presence alone clears this gate.
			if ev.QuantityKnown && int(ev.Stock) < params.MinStockForIncrease {
				return &BidIncreaseGuardrail{Reason: "real product stock is below min_stock_for_increase"}
			}
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

	return &BidIncreaseGuardrail{Allowed: true, MaxAllowedDRRPercent: readiness.MaxAllowedDRRPercent}
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

// augmentSearchPlaybookContext fills the position/price/prev-impression fields the
// search_playbook engine needs. All sources are real synced data; when a source is
// missing the field stays zero and the engine's own guards handle the absence.
func (s *BidAutomationService) augmentSearchPlaybookContext(ctx context.Context, campaignID uuid.UUID, binding domain.StrategyBinding, stats []sqlcgen.CampaignStat, bidCtx *BidContext) {
	if avgPos, ok := s.campaignAvgPosition(ctx, campaignID); ok {
		bidCtx.AvgPosition = avgPos
		bidCtx.HasPosition = true
	}

	if products, err := s.productsForBidGuardrail(ctx, binding, campaignID); err == nil {
		for _, p := range products {
			if p.Price.Valid && p.Price.Int64 > 0 {
				bidCtx.BuyerPrice = float64(p.Price.Int64)
				break
			}
		}
	}

	// Split the lookback window in half so the flat-impression pullback rule compares
	// the recent half against the prior half.
	cur, prev := splitImpressionsByDateMidpoint(stats)
	if cur > 0 || prev > 0 {
		bidCtx.Impressions = cur
		bidCtx.PrevImpressions = prev
	}
}

// campaignAvgPosition returns the impression-weighted average position across the
// campaign's keywords, from the latest synced phrase stats.
// ponytail: per-phrase loop capped at 500 phrases/campaign; batch by campaign if that ceiling is ever hit.
func (s *BidAutomationService) campaignAvgPosition(ctx context.Context, campaignID uuid.UUID) (float64, bool) {
	phrases, err := s.queries.ListPhrasesByCampaign(ctx, sqlcgen.ListPhrasesByCampaignParams{
		CampaignID: uuidToPgtype(campaignID),
		Limit:      500,
		Offset:     0,
	})
	if err != nil || len(phrases) == 0 {
		return 0, false
	}

	var weightedSum, weight float64
	for _, phrase := range phrases {
		stat, err := s.queries.GetLatestPhraseStat(ctx, phrase.ID)
		if err != nil || !stat.AvgPos.Valid || stat.AvgPos.Float64 <= 0 {
			continue
		}
		w := float64(stat.Impressions)
		if w <= 0 {
			w = 1
		}
		weightedSum += stat.AvgPos.Float64 * w
		weight += w
	}
	if weight == 0 {
		return 0, false
	}
	return weightedSum / weight, true
}

// splitImpressionsByDateMidpoint sums impressions for the recent half of the stat
// window (cur) and the older half (prev), split at the date midpoint.
func splitImpressionsByDateMidpoint(stats []sqlcgen.CampaignStat) (cur, prev int64) {
	var minD, maxD time.Time
	for _, st := range stats {
		if !st.Date.Valid {
			continue
		}
		d := st.Date.Time
		if minD.IsZero() || d.Before(minD) {
			minD = d
		}
		if maxD.IsZero() || d.After(maxD) {
			maxD = d
		}
	}
	if minD.IsZero() {
		return 0, 0
	}
	mid := minD.Add(maxD.Sub(minD) / 2)
	for _, st := range stats {
		if !st.Date.Valid {
			continue
		}
		if st.Date.Time.Before(mid) {
			prev += st.Impressions
		} else {
			cur += st.Impressions
		}
	}
	return cur, prev
}

func (s *BidAutomationService) decryptCabinetToken(ctx context.Context, cabinetID uuid.UUID) (string, error) {
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return "", err
	}
	return crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
}
