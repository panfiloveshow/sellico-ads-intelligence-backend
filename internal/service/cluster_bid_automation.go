package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const maxClusterAutomationTargets = 1000

// executeManualCPMClusters is a separate target loop because a manual CPM bid
// belongs to (campaign, nmID, normquery), not to a campaign or product alone.
// It intentionally refuses strategies that need normquery revenue: WB does not
// provide that attribution. Search-playbook increases instead use exact
// normquery spend/orders and a real per-order profitability ceiling from Sellico
// unit economics; campaign/product revenue is never borrowed.
func (s *BidAutomationService) executeManualCPMClusters(
	ctx context.Context,
	workspaceID uuid.UUID,
	strategy domain.Strategy,
	binding domain.StrategyBinding,
	campaign sqlcgen.Campaign,
	token string,
	applyToWB bool,
	evaluation *bindingEvaluationRecorder,
	now, dateFrom, dateTo time.Time,
) (int, error) {
	if !isManualCPMCampaign(campaign) {
		return 0, fmt.Errorf("cluster automation requires a manual CPM campaign")
	}
	if uuidFromPgtype(campaign.WorkspaceID) != workspaceID || uuidFromPgtype(campaign.SellerCabinetID) != strategy.SellerCabinetID {
		return 0, fmt.Errorf("campaign does not belong to strategy scope")
	}
	if reason := manualCPMStrategySkipReason(strategy.Type); reason != "" {
		s.logger.Info().Str("strategy_id", strategy.ID.String()).Str("campaign_id", uuidFromPgtype(campaign.ID).String()).
			Str("reason", reason).Msg("skipping manual CPM strategy because exact cluster evidence is unavailable")
		evaluation.block(ctx, "cluster_strategy_unsupported", reason, true)
		return 0, nil
	}

	recentFrom := normalizeStatDate(dateFrom).AddDate(0, 0, strategy.Params.Merged().LookbackDays/2)
	snapshots, err := s.queries.ListAutomationClusterSnapshots(ctx, sqlcgen.ListAutomationClusterSnapshotsParams{
		CampaignID: campaign.ID,
		ProductID:  uuidToPgtypePtr(binding.ProductID),
		DateFrom:   pgtype.Date{Time: normalizeStatDate(dateFrom), Valid: true},
		DateTo:     pgtype.Date{Time: normalizeStatDate(dateTo), Valid: true},
		RecentFrom: pgtype.Date{Time: recentFrom, Valid: true},
		Limit:      maxClusterAutomationTargets + 1,
	})
	if err != nil {
		return 0, fmt.Errorf("load real normquery targets: %w", err)
	}
	if len(snapshots) > maxClusterAutomationTargets {
		evaluation.block(ctx, "cluster_target_limit_exceeded", fmt.Sprintf("more than %d normquery targets", maxClusterAutomationTargets), true)
		return 0, fmt.Errorf("campaign has more than %d normquery targets; refusing partial automation", maxClusterAutomationTargets)
	}
	if len(snapshots) == 0 {
		evaluation.block(ctx, "cluster_targets_missing", "no exact normquery targets are available", true)
		return 0, nil
	}

	campaignStats, err := s.queries.GetCampaignStatsByDateRange(ctx, sqlcgen.GetCampaignStatsByDateRangeParams{
		CampaignID: campaign.ID,
		Date:       pgtype.Date{Time: normalizeStatDate(dateFrom), Valid: true},
		Date_2:     pgtype.Date{Time: normalizeStatDate(now), Valid: true},
		Limit:      1000,
		Offset:     0,
	})
	if err != nil {
		return 0, fmt.Errorf("load campaign pacing stats: %w", err)
	}

	changes := 0
	var actionErrors []error
	params := strategy.Params.Merged()
	for _, snapshot := range snapshots {
		evaluation.fact(ctx, "cluster_target_observed", "observed", domain.FactReadyNoChange, "phrases+phrase_stats", map[string]any{
			"phrase_id": uuidFromPgtype(snapshot.PhraseID), "wb_product_id": snapshot.WBProductID, "norm_query": snapshot.NormQuery,
		})
		if reason := clusterSnapshotGuardrailReason(snapshot, params.MaxDataAgeHours, now, strategy.Type); reason != "" {
			s.logger.Info().Str("strategy_id", strategy.ID.String()).Str("norm_query", snapshot.NormQuery).
				Str("reason", reason).Msg("skipping normquery target")
			evaluation.fact(ctx, "cluster_evidence_blocked", "blocked", domain.FactBlocked, "phrases+phrase_stats", map[string]any{"reason": reason, "norm_query": snapshot.NormQuery})
			continue
		}

		product, productErr := s.queries.GetProductByID(ctx, snapshot.ProductID)
		if productErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s product: %w", uuidFromPgtype(snapshot.PhraseID), productErr))
			continue
		}
		if product.WorkspaceID != campaign.WorkspaceID || product.SellerCabinetID != campaign.SellerCabinetID || product.WbProductID != snapshot.WBProductID {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s product scope does not match campaign", uuidFromPgtype(snapshot.PhraseID)))
			continue
		}

		productID := uuidFromPgtype(snapshot.ProductID)
		phraseID := uuidFromPgtype(snapshot.PhraseID)
		clusterBinding := binding
		clusterBinding.ProductID = &productID
		increaseGuardrail := s.bidIncreaseGuardrail(ctx, strategy, clusterBinding, uuidFromPgtype(campaign.ID), int(snapshot.CurrentBid))
		bidCtx := BidContext{
			CurrentBid:        int(snapshot.CurrentBid),
			Impressions:       snapshot.RecentImpressions,
			PrevImpressions:   snapshot.PreviousImpressions,
			Clicks:            snapshot.Clicks,
			Spend:             float64(snapshot.Spend),
			Orders:            snapshot.Orders,
			Revenue:           0, // deliberately unavailable at normquery granularity
			Placement:         "search",
			IncreaseGuardrail: increaseGuardrail,
			DecisionTime:      now,
			HasPosition:       snapshot.AveragePosition.Valid && snapshot.AveragePosition.Float64 > 0,
			AvgPosition:       snapshot.AveragePosition.Float64,
		}
		if increaseGuardrail != nil {
			bidCtx.BuyerPrice = increaseGuardrail.BuyerPrice
		}
		decision := s.engine.CalculateBid(strategy, bidCtx)
		if decision == nil {
			if increaseGuardrail != nil && !increaseGuardrail.Allowed {
				evaluation.fact(ctx, "bid_increase_readiness_blocked", "blocked", domain.FactBlocked, "stock+unit_economics", map[string]any{"reason": increaseGuardrail.Reason, "norm_query": snapshot.NormQuery})
				continue
			}
			evaluation.evaluatedTarget()
			evaluation.fact(ctx, "cluster_ready_no_change", "passed", domain.FactReadyNoChange, "bid_engine", map[string]any{"norm_query": snapshot.NormQuery})
			continue
		}
		decision.CampaignID = uuidFromPgtype(campaign.ID)
		decision.ProductID = &productID
		decision.PhraseID = &phraseID

		if reason := recentClicksIncreaseGuardrailReason(campaignStats, decision, now); reason != "" {
			evaluation.fact(ctx, "recent_clicks_guardrail", "blocked", domain.FactBlocked, "campaign_stats", map[string]any{"reason": reason, "norm_query": snapshot.NormQuery})
			continue
		}
		if reason := dailyBudgetIncreaseGuardrailReason(campaign, campaignStats, decision, now); reason != "" {
			evaluation.fact(ctx, "daily_budget_guardrail", "blocked", domain.FactBlocked, "campaign_stats", map[string]any{"reason": reason, "norm_query": snapshot.NormQuery})
			continue
		}
		if reason, guardErr := s.financialIncreaseGuardrail(ctx, campaign, campaignStats, decision, now); guardErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s financial guard: %w", phraseID, guardErr))
			continue
		} else if reason != "" {
			evaluation.fact(ctx, "financial_guardrail", "blocked", domain.FactBlocked, "campaign_finance", map[string]any{"reason": reason, "norm_query": snapshot.NormQuery})
			continue
		}
		if reason, guardErr := s.clusterBidActionGuardrail(ctx, strategy, phraseID, uuidFromPgtype(campaign.ID), decision); guardErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s action guard: %w", phraseID, guardErr))
			continue
		} else if reason != "" {
			evaluation.fact(ctx, "action_guardrail", "blocked", domain.FactBlocked, "bid_changes", map[string]any{"reason": reason, "norm_query": snapshot.NormQuery})
			continue
		}
		if reason, minimumErr := s.minimumBidDecreaseGuardrail(ctx, token, campaign, snapshot.WBProductID, decision); minimumErr != nil {
			s.recordWBActionRateLimitFromError(ctx, strategy.SellerCabinetID, minimumErr)
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s minimum bid guard: %w", phraseID, minimumErr))
			continue
		} else if reason != "" {
			evaluation.fact(ctx, "wb_minimum_bid_guardrail", "blocked", domain.FactBlocked, "wb_minimum_bids", map[string]any{"reason": reason, "norm_query": snapshot.NormQuery})
			continue
		}

		oldBid32, oldBidErr := checkedInt32(decision.OldBid)
		newBid32, newBidErr := checkedInt32(decision.NewBid)
		if oldBidErr != nil || newBidErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s bid outside supported range", phraseID))
			continue
		}
		evaluation.evaluatedTarget()
		if !applyToWB {
			if shadowErr := s.recordShadowClusterBidDecision(ctx, workspaceID, strategy, binding, campaign, snapshot, decision, bidCtx, dateFrom, dateTo, oldBid32, newBid32); shadowErr != nil {
				actionErrors = append(actionErrors, fmt.Errorf("phrase %s shadow decision: %w", phraseID, shadowErr))
			}
			evaluation.fact(ctx, "cluster_would_apply", "passed", domain.FactWouldApply, "bid_engine", map[string]any{"reason": decision.Reason, "norm_query": snapshot.NormQuery, "old_bid": decision.OldBid, "new_bid": decision.NewBid})
			evaluation.proposedTarget()
			continue
		}

		if reason, rateErr := s.wbActionRateLimitGuardrail(ctx, strategy.SellerCabinetID); rateErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s rate-limit guard: %w", phraseID, rateErr))
			break
		} else if reason != "" {
			break
		}

		normQuery := strings.TrimSpace(snapshot.NormQuery)
		automationKey, observationKey := automationClusterBidActionKeys(campaign, snapshot, decision)
		unresolved, unresolvedErr := s.queries.HasUnresolvedAutomationBidAction(ctx, sqlcgen.HasUnresolvedAutomationBidActionParams{
			CampaignID: campaign.ID, ProductID: snapshot.ProductID, Placement: "search",
			NormQuery: pgtype.Text{String: normQuery, Valid: true},
		})
		if unresolvedErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s unresolved guard: %w", phraseID, unresolvedErr))
			continue
		}
		if unresolved {
			evaluation.fact(ctx, "unresolved_prior_action", "blocked", domain.FactBlocked, "wb_bid_actions", map[string]any{"norm_query": snapshot.NormQuery})
			continue
		}
		maxWorkspaceChangesPerDay, capBlockReason, capErr := s.currentWorkspaceAutomationCap(ctx, workspaceID)
		if capErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s reload workspace daily cap: %w", phraseID, capErr))
			continue
		}
		if capBlockReason != "" {
			s.logger.Warn().Str("strategy_id", strategy.ID.String()).Str("campaign_id", uuidFromPgtype(campaign.ID).String()).
				Str("reason", capBlockReason).Msg("blocked live cluster bid claim because current workspace automation settings are not ready")
			continue
		}
		dayStart, dayEnd := automationBidDayWindow(time.Now())
		claim, claimed, claimErr := s.queries.ClaimAutomationBidAction(ctx, sqlcgen.ClaimAutomationBidActionParams{
			AutomationKey: automationKey, AutomationObservationKey: observationKey,
			WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: campaign.SellerCabinetID,
			CampaignID: campaign.ID, ProductID: snapshot.ProductID, PhraseID: snapshot.PhraseID,
			WBCampaignID: campaign.WbCampaignID, WBProductID: snapshot.WBProductID,
			OldBid: int64(decision.OldBid), NewBid: int64(decision.NewBid), Reason: decision.Reason,
			Placement: "search", NormQuery: pgtype.Text{String: normQuery, Valid: true},
			BidObservedAt: pgtype.Timestamptz{Time: snapshot.BidObservedAt.Time, Valid: snapshot.BidObservedAt.Valid},
			StrategyID:    uuidToPgtype(strategy.ID),
			DayStart:      pgtype.Timestamptz{Time: dayStart, Valid: true}, DayEnd: pgtype.Timestamptz{Time: dayEnd, Valid: true},
			MaxWorkspaceChangesPerDay: boundedInt32(maxWorkspaceChangesPerDay),
		})
		if claimErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s action claim: %w", phraseID, claimErr))
			continue
		}
		if !claimed {
			evaluation.fact(ctx, "duplicate_action", "blocked", domain.FactBlocked, "wb_bid_actions", map[string]any{"norm_query": snapshot.NormQuery})
			continue
		}
		if claim.Status == "blocked" {
			evaluation.fact(ctx, "workspace_daily_cap_reached", "blocked", domain.FactBlocked, "wb_bid_actions", map[string]any{"norm_query": snapshot.NormQuery})
			break
		}
		evaluation.fact(ctx, "action_claimed", "passed", domain.FactClaimed, "wb_bid_actions", map[string]any{"automation_action_id": uuidFromPgtype(claim.ID), "norm_query": snapshot.NormQuery})

		writeLease, blockReason, preWriteErr := s.liveBidPreWriteGuardrail(
			ctx, workspaceID, strategy.ID, uuidFromPgtype(claim.ID), dayStart, dayEnd,
		)
		if preWriteErr != nil {
			blockReason = "live automation guardrail could not be reloaded"
		}
		if blockReason != "" {
			response, _ := json.Marshal(map[string]string{"block_reason": blockReason})
			if completeErr := s.queries.CompleteAutomationBidAction(ctx, sqlcgen.CompleteAutomationBidActionParams{
				AutomationKey: automationKey, Status: "blocked", WBResponse: response,
			}); completeErr != nil {
				actionErrors = append(actionErrors, fmt.Errorf("phrase %s finalize blocked claim: %w", phraseID, completeErr))
			}
			if preWriteErr != nil {
				actionErrors = append(actionErrors, fmt.Errorf("phrase %s pre-write guard: %w", phraseID, preWriteErr))
			}
			continue
		}

		writeCtx, cancelWrite := automationBidWriteContext(ctx)
		wbErr := s.wbClient.SetClusterBids(writeCtx, token, campaign.WbCampaignID, []wb.ClusterBidItem{{
			NMID: snapshot.WBProductID, NormQuery: normQuery, Bid: decision.NewBid,
		}})
		cancelWrite()
		if leaseErr := releaseAutomationBidWriteLease(writeLease); leaseErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s release pre-write cap lease: %w", phraseID, leaseErr))
		}
		wbStatus := "applied"
		var wbResponse []byte
		if wbErr != nil {
			wbStatus = "failed"
			if wb.CampaignBidUpdateOutcomeUnknown(wbErr) {
				wbStatus = "unknown"
			}
			wbResponse, _ = json.Marshal(map[string]string{"error": wbErr.Error()})
			s.recordWBActionRateLimitFromError(ctx, strategy.SellerCabinetID, wbErr)
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s WB cluster bid update: %w", phraseID, wbErr))
		}

		_, recordErr := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
			WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: campaign.SellerCabinetID,
			CampaignID: campaign.ID, ProductID: snapshot.ProductID, PhraseID: snapshot.PhraseID,
			StrategyID: uuidToPgtype(strategy.ID), Placement: "search",
			OldBid: oldBid32, NewBid: newBid32, Reason: decision.Reason,
			Source: domain.BidSourceStrategy, WbStatus: wbStatus, AutomationActionID: claim.ID,
		})
		if recordErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s record bid change: %w", phraseID, recordErr))
		} else if completeErr := s.queries.CompleteAutomationBidAction(ctx, sqlcgen.CompleteAutomationBidActionParams{
			AutomationKey: automationKey, Status: wbStatus, WBResponse: wbResponse,
		}); completeErr != nil {
			actionErrors = append(actionErrors, fmt.Errorf("phrase %s complete claim: %w", phraseID, completeErr))
		}
		if wbStatus == "applied" {
			changes++
			evaluation.fact(ctx, "cluster_bid_applied", "passed", domain.FactApplied, "wb_campaign_actions", map[string]any{"norm_query": snapshot.NormQuery, "new_bid": decision.NewBid})
		} else if wbStatus == "unknown" {
			evaluation.fact(ctx, "cluster_bid_unknown", "error", domain.FactUnknown, "wb_campaign_actions", map[string]any{"norm_query": snapshot.NormQuery})
		} else {
			evaluation.fact(ctx, "cluster_bid_failed", "error", domain.FactFailed, "wb_campaign_actions", map[string]any{"norm_query": snapshot.NormQuery})
		}
		if isRateLimitIssueFromError(wbErr) {
			break
		}
	}
	return changes, errors.Join(actionErrors...)
}

func manualCPMStrategySkipReason(strategyType string) string {
	switch strategyType {
	case domain.StrategyTypeAntiSliv, domain.StrategyTypeSearchPlaybook:
		return ""
	case domain.StrategyTypeACoS, domain.StrategyTypeROAS:
		return "WB does not provide exact normquery revenue required by this strategy"
	case domain.StrategyTypeDayparting:
		return "cluster-specific dayparting baseline state is not configured"
	default:
		return "strategy does not support normquery targets"
	}
}

func clusterSnapshotGuardrailReason(snapshot sqlcgen.AutomationClusterSnapshot, maxAgeHours int, now time.Time, strategyType string) string {
	if !snapshot.PhraseID.Valid || !snapshot.ProductID.Valid || snapshot.WBProductID <= 0 || strings.TrimSpace(snapshot.NormQuery) == "" || snapshot.CurrentBid <= 0 {
		return "exact cluster identity or current bid is unavailable"
	}
	if snapshot.StatRows <= 0 || !snapshot.StatsObservedAt.Valid {
		return "completed-day cluster stats are unavailable"
	}
	if !snapshot.BidObservedAt.Valid {
		return "cluster bid observation time is unavailable"
	}
	if maxAgeHours <= 0 {
		maxAgeHours = domain.DefaultStrategyParams().MaxDataAgeHours
	}
	maxAge := time.Duration(maxAgeHours) * time.Hour
	if now.Sub(snapshot.StatsObservedAt.Time) < 0 || now.Sub(snapshot.StatsObservedAt.Time) > maxAge {
		return "cluster stats are stale"
	}
	if now.Sub(snapshot.BidObservedAt.Time) < 0 || now.Sub(snapshot.BidObservedAt.Time) > maxAge {
		return "cluster bid is stale"
	}
	if (strategyType == domain.StrategyTypeAntiSliv || strategyType == domain.StrategyTypeSearchPlaybook) && !snapshot.OrdersKnown {
		return "cluster order evidence is incomplete"
	}
	if strategyType == domain.StrategyTypeAntiSliv && snapshot.Orders > 0 {
		return "anti-waste cluster reduction requires verified zero orders"
	}
	return ""
}

func automationClusterBidActionKeys(campaign sqlcgen.Campaign, snapshot sqlcgen.AutomationClusterSnapshot, decision *BidDecision) (string, string) {
	base := fmt.Sprintf("%s:%d:%d:search:%q:%d:%s", uuidFromPgtype(campaign.SellerCabinetID), campaign.WbCampaignID,
		snapshot.WBProductID, strings.TrimSpace(snapshot.NormQuery), decision.OldBid,
		snapshot.BidObservedAt.Time.UTC().Format(time.RFC3339Nano))
	return fmt.Sprintf("%s:%d", base, decision.NewBid), base
}

func (s *BidAutomationService) recordShadowClusterBidDecision(
	ctx context.Context, workspaceID uuid.UUID, strategy domain.Strategy, binding domain.StrategyBinding,
	campaign sqlcgen.Campaign, snapshot sqlcgen.AutomationClusterSnapshot, decision *BidDecision,
	bidCtx BidContext, dateFrom, dateTo time.Time, oldBid, proposedBid int32,
) error {
	transitionKey, _ := automationClusterBidActionKeys(campaign, snapshot, decision)
	metrics, err := json.Marshal(map[string]any{
		"data_mode": "exact", "scope": "normquery", "revenue_available": false,
		"date_from":   normalizeStatDate(dateFrom).Format("2006-01-02"),
		"date_to":     normalizeStatDate(dateTo).Format("2006-01-02"),
		"impressions": bidCtx.Impressions, "previous_impressions": bidCtx.PrevImpressions,
		"clicks": bidCtx.Clicks, "spend": bidCtx.Spend, "orders": bidCtx.Orders,
		"buyer_price": bidCtx.BuyerPrice, "max_allowed_cpo": bidCtx.IncreaseGuardrail.MaxAllowedCPO,
		"average_position": bidCtx.AvgPosition, "decision_time": bidCtx.DecisionTime.UTC().Format(time.RFC3339),
		"bid_observed_at": snapshot.BidObservedAt.Time.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("encode cluster shadow metrics: %w", err)
	}
	return s.queries.UpsertBidDecisionObservation(ctx, sqlcgen.UpsertBidDecisionObservationParams{
		ObservationKey: transitionKey, WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: campaign.SellerCabinetID,
		StrategyID: uuidToPgtype(strategy.ID), StrategyBindingID: uuidToPgtype(binding.ID), CampaignID: campaign.ID,
		ProductID: snapshot.ProductID, PhraseID: snapshot.PhraseID, WBCampaignID: campaign.WbCampaignID,
		WBProductID: snapshot.WBProductID, NormQuery: pgtype.Text{String: strings.TrimSpace(snapshot.NormQuery), Valid: true},
		Placement: "search", OldBid: oldBid, ProposedBid: proposedBid, Reason: decision.Reason,
		Metrics: metrics, AutomationLevel: boundedInt32(strategy.Params.Merged().AutomationLevel), BidObservedAt: snapshot.BidObservedAt.Time.UTC(),
	})
}

func (s *BidAutomationService) clusterBidActionGuardrail(ctx context.Context, strategy domain.Strategy, phraseID, campaignID uuid.UUID, decision *BidDecision) (string, error) {
	params := strategy.Params.Merged()
	if params.CooldownMinutes <= 0 && params.MaxChangesPerDay <= 0 {
		return "", nil
	}
	now := time.Now()
	earliest := now
	if params.CooldownMinutes > 0 {
		earliest = minTime(earliest, now.Add(-time.Duration(params.CooldownMinutes)*time.Minute))
	}
	if params.MaxChangesPerDay > 0 {
		earliest = minTime(earliest, now.Add(-24*time.Hour))
	}
	changes, err := s.queries.ListBidChangesByWorkspaceSince(ctx, sqlcgen.ListBidChangesByWorkspaceSinceParams{
		WorkspaceID: uuidToPgtype(strategy.WorkspaceID), Since: pgtype.Timestamptz{Time: earliest, Valid: true}, Limit: 2000,
	})
	if err != nil {
		return "", err
	}
	countSince := func(since time.Time) int {
		count := 0
		for _, change := range changes {
			if change.WbStatus != "applied" || change.Placement != decision.Placement || !change.CreatedAt.Valid || change.CreatedAt.Time.Before(since) {
				continue
			}
			if change.CampaignID.Valid && uuidFromPgtype(change.CampaignID) == campaignID && change.PhraseID.Valid && uuidFromPgtype(change.PhraseID) == phraseID {
				count++
			}
		}
		return count
	}
	if params.CooldownMinutes > 0 && countSince(now.Add(-time.Duration(params.CooldownMinutes)*time.Minute)) > 0 {
		return "cluster bid cooldown is active", nil
	}
	if params.MaxChangesPerDay > 0 && countSince(now.Add(-24*time.Hour)) >= params.MaxChangesPerDay {
		return "cluster max_changes_per_day reached", nil
	}
	return "", nil
}
