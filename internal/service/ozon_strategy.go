package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// ozonCampaignStateRunning is the Performance API state in which bid writes
// are meaningful; every other state blocks the strategy deterministically.
const ozonCampaignStateRunning = "CAMPAIGN_STATE_RUNNING"

// OzonStrategyService runs the deterministic ozon_cpc_target_drr strategy:
// per bound campaign it computes ДРР over the lookback window, asks the
// shared BidEngine for a campaign-level decision (ДРР == ACoS math), applies
// deterministic guardrails, and scales every per-SKU bid by the decision's
// multiplier. AutomationLevel < 3 records shadow rows only.
// ozonStrategyPerfClient is the narrow Performance-API surface the deterministic
// bid sweep needs. *ozon.PerfClient satisfies it in production; tests supply a
// fake so the live (level-3) write path can be exercised without a live call.
type ozonStrategyPerfClient interface {
	SetCampaignProductBids(ctx context.Context, creds ozon.Credentials, campaignID int64, bids []ozon.ProductBid) error
	GetMinSKUBids(ctx context.Context, creds ozon.Credentials, skus []int64, paymentType string) ([]ozon.MinSKUBid, error)
}

type OzonStrategyService struct {
	queries       *sqlcgen.Queries
	perfClient    ozonStrategyPerfClient
	engine        *BidEngine
	encryptionKey []byte
	logger        zerolog.Logger
}

// NewOzonStrategyService wires the deterministic Ozon strategy runner.
func NewOzonStrategyService(queries *sqlcgen.Queries, perfClient *ozon.PerfClient, engine *BidEngine, encryptionKey []byte, logger zerolog.Logger) *OzonStrategyService {
	return &OzonStrategyService{
		queries:       queries,
		perfClient:    perfClient,
		engine:        engine,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ozon_strategy").Logger(),
	}
}

// ozonStrategyDecisionContext is persisted into ozon_bid_changes.decision_context.
type ozonStrategyDecisionContext struct {
	StrategyID   string  `json:"strategy_id"`
	StrategyType string  `json:"strategy_type"`
	DRR          float64 `json:"drr"`
	TargetDRR    float64 `json:"target_drr"`
	Multiplier   float64 `json:"multiplier"`
	Clicks       int64   `json:"clicks"`
	SpendRub     float64 `json:"spend_rub"`
	RevenueRub   float64 `json:"revenue_rub"`
	Orders       int64   `json:"orders"`
	LookbackDays int     `json:"lookback_days"`
	Reason       string  `json:"reason"`
}

// RunForWorkspace executes every active ozon_* strategy of the workspace.
// Returns the number of live (level 3) campaign bid applications.
func (s *OzonStrategyService) RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	rows, err := s.queries.ListActiveStrategiesByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, fmt.Errorf("list active strategies: %w", err)
	}

	applied := 0
	var runErrors []error
	for _, row := range rows {
		strategy := strategyFromSqlc(row)
		if !domain.IsOzonStrategy(strategy.Type) {
			continue
		}
		// AI autopilot strategies are executed exclusively by the AI manager
		// (ozon:ai_run); the deterministic sweep must never touch them.
		if strategy.Type == domain.StrategyTypeOzonAIAutopilot {
			continue
		}
		// Repricer strategies are executed exclusively by OzonRepricerService
		// (ozon:repricer_run); the bid sweep must never touch them.
		if domain.IsOzonPriceStrategy(strategy.Type) {
			continue
		}
		count, strategyErr := s.runStrategy(ctx, workspaceID, strategy)
		applied += count
		if strategyErr != nil {
			s.logger.Error().Err(strategyErr).
				Str("strategy_id", strategy.ID.String()).
				Str("strategy_type", strategy.Type).
				Msg("ozon strategy execution failed")
			runErrors = append(runErrors, fmt.Errorf("strategy %s: %w", strategy.ID, strategyErr))
		}
	}
	return applied, errors.Join(runErrors...)
}

func (s *OzonStrategyService) runStrategy(ctx context.Context, workspaceID uuid.UUID, strategy domain.Strategy) (int, error) {
	params := strategy.Params.Merged()

	creds, err := s.strategyCredentials(ctx, workspaceID, strategy)
	if err != nil {
		return 0, err
	}

	bindings, err := s.queries.ListOzonCampaignBindingsByStrategy(ctx, uuidToPgtype(strategy.ID))
	if err != nil {
		return 0, fmt.Errorf("list ozon bindings: %w", err)
	}
	if len(bindings) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	// Min-bid lookups are cached across the whole strategy run: one campaign's
	// SKUs often repeat in sibling campaigns.
	minBidCache := map[int64]float64{}

	applied := 0
	var bindingErrors []error
	for _, binding := range bindings {
		count, bindingErr := s.runCampaign(ctx, strategy, params, creds, binding, now, minBidCache)
		applied += count
		if bindingErr != nil {
			bindingErrors = append(bindingErrors, fmt.Errorf("campaign %d: %w", binding.OzonCampaignID, bindingErr))
		}
	}
	return applied, errors.Join(bindingErrors...)
}

func (s *OzonStrategyService) strategyCredentials(ctx context.Context, workspaceID uuid.UUID, strategy domain.Strategy) (ozon.Credentials, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(strategy.SellerCabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return ozon.Credentials{}, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.WorkspaceID != workspaceID || cabinet.Marketplace != domain.MarketplaceOzon {
		return ozon.Credentials{}, apperror.New(apperror.ErrValidation, "strategy cabinet is not an ozon cabinet of this workspace")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return ozon.Credentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	if !creds.HasPerformanceAPI() {
		return ozon.Credentials{}, apperror.New(apperror.ErrValidation, "performance api credentials are missing for this cabinet")
	}
	return ozonClientCreds(creds), nil
}

func (s *OzonStrategyService) runCampaign(
	ctx context.Context,
	strategy domain.Strategy,
	params domain.StrategyParams,
	creds ozon.Credentials,
	binding sqlcgen.ListOzonCampaignBindingsByStrategyRow,
	now time.Time,
	minBidCache map[int64]float64,
) (int, error) {
	logger := s.logger.With().
		Str("strategy_id", strategy.ID.String()).
		Int64("ozon_campaign_id", binding.OzonCampaignID).
		Logger()

	// Guardrail: only running campaigns are written to.
	if pgTextValue(binding.State) != ozonCampaignStateRunning {
		logger.Debug().Str("state", pgTextValue(binding.State)).Msg("skipping non-running ozon campaign")
		return 0, nil
	}

	campaignID := uuidFromPgtype(binding.ID)
	since := now.AddDate(0, 0, -params.LookbackDays)
	agg, err := s.queries.AggregateOzonCampaignStatsSince(ctx, sqlcgen.AggregateOzonCampaignStatsSinceParams{
		CampaignID: binding.ID,
		Date:       pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("aggregate stats: %w", err)
	}

	// Guardrail: click evidence. The ACoS engine re-checks this, but the
	// explicit gate keeps the skip reason observable.
	if agg.Clicks < int64(params.MinClicks) {
		logger.Debug().Int64("clicks", agg.Clicks).Int("min_clicks", params.MinClicks).Msg("skipping: not enough clicks in lookback window")
		return 0, nil
	}

	productRows, err := s.queries.ListOzonCampaignProducts(ctx, binding.ID)
	if err != nil {
		return 0, fmt.Errorf("list campaign products: %w", err)
	}
	type skuBid struct {
		SKU    int64
		BidRub float64
	}
	currentBids := make([]skuBid, 0, len(productRows))
	bidSum := 0.0
	for _, row := range productRows {
		bidRub := pgNumericToFloat(row.BidRub)
		if !row.IsActive || bidRub <= 0 {
			continue
		}
		currentBids = append(currentBids, skuBid{SKU: row.Sku, BidRub: bidRub})
		bidSum += bidRub
	}
	if len(currentBids) == 0 {
		logger.Debug().Msg("skipping: campaign has no active products with bids")
		return 0, nil
	}

	// The campaign-level decision runs on the average per-SKU bid (integer
	// rubles, as the engine expects); the resulting multiplier is applied to
	// every SKU uniformly.
	avgBid := int(math.Round(bidSum / float64(len(currentBids))))
	if avgBid <= 0 {
		return 0, nil
	}

	bidCtx := BidContext{
		CurrentBid:   avgBid,
		Impressions:  agg.Views,
		Clicks:       agg.Clicks,
		Spend:        pgNumericToFloat(agg.SpendRub),
		Revenue:      pgNumericToFloat(agg.RevenueRub),
		Orders:       agg.Orders,
		Placement:    "ozon_cpc",
		DecisionTime: now,
	}
	decision := s.engine.CalculateBid(strategy, bidCtx)
	if decision == nil || decision.OldBid <= 0 || decision.NewBid == decision.OldBid {
		return 0, nil
	}
	multiplier := float64(decision.NewBid) / float64(decision.OldBid)

	// Guardrails: cooldown + daily cap per (campaign, sku). Uniform scaling
	// writes all SKUs together, so the worst per-SKU counter guards the run.
	guard, err := s.queries.GetOzonStrategyGuardState(ctx, sqlcgen.GetOzonStrategyGuardStateParams{
		CampaignID: binding.ID,
		DayStart:   pgtype.Timestamptz{Time: ozonStrategyDayStart(now), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("load guard state: %w", err)
	}
	var lastChangeAt *time.Time
	if guard.LastChangeAt.Valid {
		lastChange := guard.LastChangeAt.Time
		lastChangeAt = &lastChange
	}
	if reason := ozonStrategyGuardReason(guard.MaxChangesPerSkuToday, lastChangeAt, params, now); reason != "" {
		logger.Info().Str("reason", reason).Msg("ozon strategy blocked by guardrail")
		return 0, nil
	}

	shadow := params.AutomationLevel < 3

	// Guardrail: Ozon's minimum per-SKU bids (live writes only — shadow rows
	// record the raw intent). A failed lookup blocks the live write: without
	// real minimums the write is not provably valid.
	if !shadow {
		skus := make([]int64, 0, len(currentBids))
		for _, item := range currentBids {
			skus = append(skus, item.SKU)
		}
		if err := s.fillMinBids(ctx, creds, skus, minBidCache); err != nil {
			return 0, fmt.Errorf("min sku bids unavailable, live write blocked: %w", err)
		}
	}

	drr := 0.0
	if bidCtx.Revenue > 0 {
		drr = bidCtx.Spend / bidCtx.Revenue * 100
	}
	contextJSON, _ := json.Marshal(ozonStrategyDecisionContext{
		StrategyID:   strategy.ID.String(),
		StrategyType: strategy.Type,
		DRR:          drr,
		TargetDRR:    params.TargetACoS,
		Multiplier:   multiplier,
		Clicks:       agg.Clicks,
		SpendRub:     bidCtx.Spend,
		RevenueRub:   bidCtx.Revenue,
		Orders:       agg.Orders,
		LookbackDays: params.LookbackDays,
		Reason:       decision.Reason,
	})

	status := domain.OzonBidStatusShadow
	if !shadow {
		status = domain.OzonBidStatusPending
	}

	newBids := make([]ozon.ProductBid, 0, len(currentBids))
	changeIDs := make([]pgtype.UUID, 0, len(currentBids))
	for _, item := range currentBids {
		newBid := roundRub(item.BidRub * multiplier)
		if minBid, ok := minBidCache[item.SKU]; ok && minBid > 0 && newBid < minBid {
			newBid = roundRub(minBid)
		}
		if newBid <= 0 || newBid == item.BidRub {
			continue
		}
		row, insertErr := s.queries.InsertOzonBidChange(ctx, sqlcgen.InsertOzonBidChangeParams{
			CampaignID:      binding.ID,
			Kind:            domain.OzonBidKindCPC,
			Sku:             pgtype.Int8{Int64: item.SKU, Valid: true},
			OldBidRub:       floatToPgNumeric(item.BidRub),
			NewBidRub:       floatToPgNumeric(newBid),
			Reason:          textToPgtype(decision.Reason),
			Source:          domain.BidSourceStrategy,
			Status:          status,
			DecisionContext: contextJSON,
		})
		if insertErr != nil {
			return 0, fmt.Errorf("record bid change: %w", insertErr)
		}
		changeIDs = append(changeIDs, row.ID)
		newBids = append(newBids, ozon.ProductBid{SKU: item.SKU, BidRub: newBid})
	}
	if len(newBids) == 0 {
		return 0, nil
	}

	if shadow {
		logger.Info().
			Float64("multiplier", multiplier).
			Int("skus", len(newBids)).
			Str("reason", decision.Reason).
			Msg("recorded shadow ozon bid decisions without calling the API")
		return 0, nil
	}

	writeErr := s.perfClient.SetCampaignProductBids(ctx, creds, binding.OzonCampaignID, newBids)
	finalStatus := domain.OzonBidStatusApplied
	var errText pgtype.Text
	if writeErr != nil {
		finalStatus = domain.OzonBidStatusFailed
		errText = textToPgtype(truncateError(writeErr.Error()))
	}
	for _, id := range changeIDs {
		if markErr := s.queries.MarkOzonBidChangeResult(ctx, sqlcgen.MarkOzonBidChangeResultParams{
			ID: id, Status: finalStatus, Error: errText,
		}); markErr != nil {
			logger.Warn().Err(markErr).Msg("failed to finalize ozon strategy bid change")
		}
	}
	if writeErr != nil {
		return 0, fmt.Errorf("set campaign product bids: %w", writeErr)
	}

	for _, bid := range newBids {
		if mirrorErr := s.queries.UpdateOzonCampaignProductBid(ctx, sqlcgen.UpdateOzonCampaignProductBidParams{
			CampaignID: binding.ID,
			Sku:        bid.SKU,
			BidRub:     floatToPgNumeric(bid.BidRub),
		}); mirrorErr != nil {
			logger.Warn().Err(mirrorErr).Int64("sku", bid.SKU).Msg("failed to mirror strategy bid")
		}
	}

	logger.Info().
		Str("campaign_id", campaignID.String()).
		Float64("multiplier", multiplier).
		Int("skus", len(newBids)).
		Str("reason", decision.Reason).
		Msg("ozon strategy bids applied")
	return 1, nil
}

// fillMinBids resolves Ozon minimum CPC bids for any SKU missing from the
// cache; results (including zero minimums) are cached for the whole run.
func (s *OzonStrategyService) fillMinBids(ctx context.Context, creds ozon.Credentials, skus []int64, cache map[int64]float64) error {
	missing := make([]int64, 0, len(skus))
	for _, sku := range skus {
		if _, ok := cache[sku]; !ok {
			missing = append(missing, sku)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	minBids, err := s.perfClient.GetMinSKUBids(ctx, creds, missing, "CPC")
	if err != nil {
		return err
	}
	found := map[int64]float64{}
	for _, row := range minBids {
		found[row.SKU] = row.BidRub
	}
	for _, sku := range missing {
		cache[sku] = found[sku] // zero when the API returned no row
	}
	return nil
}

// ozonStrategyGuardReason is the pure cooldown/daily-cap guardrail: it blocks
// when the campaign already hit MaxChangesPerDay per SKU today, or when the
// last strategy change is younger than CooldownMinutes.
func ozonStrategyGuardReason(changesToday int64, lastChangeAt *time.Time, params domain.StrategyParams, now time.Time) string {
	if params.MaxChangesPerDay > 0 && changesToday >= int64(params.MaxChangesPerDay) {
		return fmt.Sprintf("daily change limit reached (%d/%d)", changesToday, params.MaxChangesPerDay)
	}
	if lastChangeAt != nil && params.CooldownMinutes > 0 {
		cooldown := time.Duration(params.CooldownMinutes) * time.Minute
		if age := now.Sub(*lastChangeAt); age < cooldown {
			return fmt.Sprintf("cooldown active: last change %s ago, cooldown %s", age.Round(time.Second), cooldown)
		}
	}
	return ""
}

// ozonStrategyDayStart returns the UTC midnight preceding now — the daily-cap
// counting window boundary.
func ozonStrategyDayStart(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// roundRub rounds a ruble amount to kopecks (2dp).
func roundRub(value float64) float64 {
	return math.Round(value*100) / 100
}
