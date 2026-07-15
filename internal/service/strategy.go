package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// StrategyService manages bid automation strategies.
type StrategyService struct {
	queries *sqlcgen.Queries
}

const liveStrategyOwnershipConflictMessage = "live strategy ownership conflict: another active live strategy already owns this campaign/product scope"

type strategyBindingScope struct {
	CampaignID uuid.UUID
	ProductID  *uuid.UUID
}

func strategyBindingScopesOverlap(left, right strategyBindingScope) bool {
	if left.CampaignID == uuid.Nil || right.CampaignID == uuid.Nil || left.CampaignID != right.CampaignID {
		return false
	}
	if left.ProductID == nil || right.ProductID == nil {
		return true
	}
	return *left.ProductID == *right.ProductID
}

func strategyBindingScopeSetHasOverlap(scopes []strategyBindingScope) bool {
	for left := range scopes {
		for right := left + 1; right < len(scopes); right++ {
			if strategyBindingScopesOverlap(scopes[left], scopes[right]) {
				return true
			}
		}
	}
	return false
}

func strategyRequiresLiveOwnership(isActive bool, automationLevel int) bool {
	if automationLevel == 0 {
		automationLevel = domain.DefaultStrategyParams().AutomationLevel
	}
	return isActive && automationLevel >= 3
}

func NewStrategyService(queries *sqlcgen.Queries) *StrategyService {
	return &StrategyService{queries: queries}
}

func (s *StrategyService) Create(ctx context.Context, workspaceID uuid.UUID, input domain.Strategy) (*domain.Strategy, error) {
	if err := validateStrategyForSave(input); err != nil {
		return nil, err
	}
	if input.SellerCabinetID == uuid.Nil {
		return nil, apperror.New(apperror.ErrValidation, "seller_cabinet_id is required")
	}
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(input.SellerCabinetID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if uuidFromPgtype(cabinet.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}

	paramsJSON, err := json.Marshal(input.Params)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "invalid strategy params")
	}

	row, err := s.queries.CreateStrategy(ctx, sqlcgen.CreateStrategyParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(input.SellerCabinetID),
		Name:            input.Name,
		Type:            input.Type,
		Params:          paramsJSON,
		IsActive:        input.IsActive,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create strategy")
	}

	result := strategyFromSqlc(row)
	return &result, nil
}

func (s *StrategyService) Get(ctx context.Context, workspaceID, strategyID uuid.UUID) (*domain.Strategy, error) {
	row, err := s.queries.GetStrategyByIDAndWorkspace(ctx, sqlcgen.GetStrategyByIDAndWorkspaceParams{
		ID:          uuidToPgtype(strategyID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "strategy not found")
	}

	result := strategyFromSqlc(row)

	bindings, _ := s.queries.ListStrategyBindings(ctx, uuidToPgtype(strategyID))
	for _, b := range bindings {
		result.Bindings = append(result.Bindings, bindingFromSqlc(b))
	}

	return &result, nil
}

func (s *StrategyService) List(ctx context.Context, workspaceID uuid.UUID, sellerCabinetID *uuid.UUID, limit, offset int32) ([]domain.Strategy, error) {
	rows, err := s.queries.ListStrategiesByWorkspace(ctx, sqlcgen.ListStrategiesByWorkspaceParams{
		WorkspaceID:           uuidToPgtype(workspaceID),
		SellerCabinetIDFilter: nullableUUIDToPgtype(sellerCabinetID),
		Limit:                 limit,
		Offset:                offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list strategies")
	}

	result := make([]domain.Strategy, len(rows))
	for i, row := range rows {
		result[i] = strategyFromSqlc(row)
	}
	return result, nil
}

func (s *StrategyService) Update(ctx context.Context, workspaceID, strategyID uuid.UUID, input domain.Strategy) (*domain.Strategy, error) {
	if err := validateStrategyForSave(input); err != nil {
		return nil, err
	}
	paramsJSON, err := json.Marshal(input.Params)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "invalid strategy params")
	}

	qtx, tx, err := s.queries.BeginStrategyOwnershipTx(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to start strategy ownership check")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := qtx.GetStrategyByIDAndWorkspace(ctx, sqlcgen.GetStrategyByIDAndWorkspaceParams{
		ID: uuidToPgtype(strategyID), WorkspaceID: uuidToPgtype(workspaceID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrNotFound, "strategy not found")
		}
		return nil, apperror.New(apperror.ErrInternal, "failed to load strategy for ownership check")
	}
	if strategyRequiresLiveOwnership(input.IsActive, input.Params.AutomationLevel) {
		conflict, conflictErr := qtx.HasLiveStrategyOwnershipConflictForBindings(ctx, sqlcgen.HasLiveStrategyOwnershipConflictForBindingsParams{
			WorkspaceID: uuidToPgtype(workspaceID), StrategyID: uuidToPgtype(strategyID),
		})
		if conflictErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to check live strategy ownership")
		}
		if conflict {
			return nil, apperror.New(apperror.ErrValidation, liveStrategyOwnershipConflictMessage)
		}
	}

	row, err := qtx.UpdateStrategyInWorkspace(ctx, sqlcgen.UpdateStrategyInWorkspaceParams{
		ID:          uuidToPgtype(strategyID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        input.Name,
		Type:        input.Type,
		Params:      paramsJSON,
		IsActive:    input.IsActive,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrNotFound, "strategy not found")
		}
		return nil, apperror.New(apperror.ErrInternal, "failed to update strategy")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to commit strategy ownership update")
	}

	result := strategyFromSqlc(row)
	return &result, nil
}

func (s *StrategyService) ListShadowDecisions(ctx context.Context, workspaceID, strategyID uuid.UUID, limit, offset int32) ([]domain.BidDecisionObservation, error) {
	if _, err := s.Get(ctx, workspaceID, strategyID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListBidDecisionObservationsByStrategy(ctx, sqlcgen.ListBidDecisionObservationsByStrategyParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		StrategyID:  uuidToPgtype(strategyID),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list shadow bid decisions")
	}
	result := make([]domain.BidDecisionObservation, len(rows))
	for index, row := range rows {
		result[index] = domain.BidDecisionObservation{
			ID:                uuidFromPgtype(row.ID),
			WorkspaceID:       uuidFromPgtype(row.WorkspaceID),
			SellerCabinetID:   uuidFromPgtype(row.SellerCabinetID),
			StrategyID:        uuidFromPgtype(row.StrategyID),
			StrategyBindingID: uuidFromPgtype(row.StrategyBindingID),
			CampaignID:        uuidFromPgtype(row.CampaignID),
			ProductID:         uuidToPtr(row.ProductID),
			PhraseID:          uuidToPtr(row.PhraseID),
			WBCampaignID:      row.WBCampaignID,
			WBProductID:       row.WBProductID,
			NormQuery:         row.NormQuery.String,
			Placement:         row.Placement,
			OldBid:            int(row.OldBid),
			ProposedBid:       int(row.ProposedBid),
			Reason:            row.Reason,
			Metrics:           json.RawMessage(row.Metrics),
			AutomationLevel:   int(row.AutomationLevel),
			BidObservedAt:     row.BidObservedAt.Time,
			FirstSeenAt:       row.FirstSeenAt.Time,
			LastSeenAt:        row.LastSeenAt.Time,
		}
	}
	return result, nil
}

func validateStrategyForSave(input domain.Strategy) error {
	if strings.TrimSpace(input.Name) == "" {
		return apperror.New(apperror.ErrValidation, "strategy name is required")
	}
	switch input.Type {
	case domain.StrategyTypeACoS,
		domain.StrategyTypeROAS,
		domain.StrategyTypeAntiSliv,
		domain.StrategyTypeDayparting,
		domain.StrategyTypeSearchPlaybook:
	case domain.StrategyTypeRecommendation:
		return apperror.New(apperror.ErrValidation, "recommendation strategies are not executable; use explicit recommendation approval")
	case domain.StrategyTypePriceMarginFloor,
		domain.StrategyTypePriceInventoryDemand,
		domain.StrategyTypePriceAdLinked,
		domain.StrategyTypePricePeakHours,
		domain.StrategyTypePriceCompetitorFollow:
		return validatePriceStrategy(input)
	default:
		return apperror.New(apperror.ErrValidation, "invalid strategy type")
	}
	params := input.Params
	switch {
	case params.MinBid < 0:
		return apperror.New(apperror.ErrValidation, "min_bid must be non-negative")
	case params.MaxBid < 0:
		return apperror.New(apperror.ErrValidation, "max_bid must be non-negative")
	case params.MinBid > 0 && params.MaxBid > 0 && params.MinBid > params.MaxBid:
		return apperror.New(apperror.ErrValidation, "min_bid must be less than or equal to max_bid")
	case params.MaxCPC < 0:
		return apperror.New(apperror.ErrValidation, "max_cpc must be non-negative")
	case params.MaxCPO < 0:
		return apperror.New(apperror.ErrValidation, "max_cpo must be non-negative")
	case params.MaxACoS < 0 || params.MaxACoS > 1000:
		return apperror.New(apperror.ErrValidation, "max_acos must be between 0 and 1000")
	case params.AutomationLevel < 0 || params.AutomationLevel > 4:
		return apperror.New(apperror.ErrValidation, fmt.Sprintf("automation_level %d is invalid; use 1..4", params.AutomationLevel))
	case params.MaxChangePercent < 0 || params.MaxChangePercent > 100:
		return apperror.New(apperror.ErrValidation, "max_change_percent must be between 0 and 100")
	case params.LookbackDays < 0:
		return apperror.New(apperror.ErrValidation, "lookback_days must be non-negative")
	case params.MinClicks < 0:
		return apperror.New(apperror.ErrValidation, "min_clicks must be non-negative")
	case params.MinStockForIncrease < 0:
		return apperror.New(apperror.ErrValidation, "min_stock_for_increase must be non-negative")
	case params.CooldownMinutes < 0:
		return apperror.New(apperror.ErrValidation, "cooldown_minutes must be non-negative")
	case params.MaxChangesPerDay < 0:
		return apperror.New(apperror.ErrValidation, "max_changes_per_day must be non-negative")
	case params.MaxDataAgeHours < 0:
		return apperror.New(apperror.ErrValidation, "max_data_age_hours must be non-negative")
	}
	switch input.Type {
	case domain.StrategyTypeACoS:
		if params.TargetACoS <= 0 || params.TargetACoS > 1000 {
			return apperror.New(apperror.ErrValidation, "target_acos must be greater than 0 and at most 1000")
		}
	case domain.StrategyTypeROAS:
		if params.TargetROAS <= 0 || params.TargetROAS > 1000 {
			return apperror.New(apperror.ErrValidation, "target_roas must be greater than 0 and at most 1000")
		}
	case domain.StrategyTypeAntiSliv:
		if params.MaxACoS <= 0 {
			return apperror.New(apperror.ErrValidation, "max_acos must be greater than 0 for anti_sliv")
		}
	case domain.StrategyTypeDayparting:
		return validateDaypartingStrategy(params)
	case domain.StrategyTypeSearchPlaybook:
		return validateSearchPlaybookStrategy(params)
	}
	return nil
}

func validateDaypartingStrategy(p domain.StrategyParams) error {
	if p.BaseMultiplier < 0 || p.BaseMultiplier > 5 {
		return apperror.New(apperror.ErrValidation, "base_multiplier must be greater than 0 and at most 5")
	}
	for rawHour, multiplier := range p.HourlyMultipliers {
		hour, err := strconv.Atoi(rawHour)
		if err != nil || hour < 0 || hour > 23 {
			return apperror.New(apperror.ErrValidation, "hourly_multipliers keys must be hours 0..23")
		}
		if multiplier <= 0 || multiplier > 5 {
			return apperror.New(apperror.ErrValidation, "hourly_multipliers values must be greater than 0 and at most 5")
		}
	}
	for rawWeekday, multiplier := range p.WeekdayMultipliers {
		weekday, err := strconv.Atoi(rawWeekday)
		if err != nil || weekday < 0 || weekday > 6 {
			return apperror.New(apperror.ErrValidation, "weekday_multipliers keys must be weekdays 0..6")
		}
		if multiplier <= 0 || multiplier > 5 {
			return apperror.New(apperror.ErrValidation, "weekday_multipliers values must be greater than 0 and at most 5")
		}
	}
	timezone := strings.TrimSpace(p.Timezone)
	if timezone == "" {
		timezone = "Europe/Moscow"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return apperror.New(apperror.ErrValidation, "timezone must be a valid IANA timezone")
	}
	return nil
}

// validateSearchPlaybookStrategy validates search_playbook-specific parameters.
func validateSearchPlaybookStrategy(p domain.StrategyParams) error {
	switch p.FrequencyTier {
	case "", "high", "mid", "low":
	default:
		return apperror.New(apperror.ErrValidation, "frequency_tier must be high, mid or low")
	}
	if p.TargetPosition < 0 || p.TargetPosition > 100 || (p.TargetPosition > 0 && p.TargetPosition < 1) {
		return apperror.New(apperror.ErrValidation, "target_position must be between 1 and 100")
	}
	if p.TargetPosition == 0 && p.FrequencyTier == "" {
		return apperror.New(apperror.ErrValidation, "search_playbook requires frequency_tier or target_position")
	}
	if p.SacrificialSpendPricePct < 0 || p.SacrificialSpendPricePct > 1000 {
		return apperror.New(apperror.ErrValidation, "sacrificial_spend_price_pct must be between 0 and 1000")
	}
	if p.FlatImpressionsPct < 0 || p.FlatImpressionsPct > 100 || p.RollbackStepPercent < 0 || p.RollbackStepPercent > 100 {
		return apperror.New(apperror.ErrValidation, "flat_impressions_pct and rollback_step_percent must be between 0 and 100")
	}
	return nil
}

// validatePriceStrategy validates repricer (price_*) strategy parameters.
func validatePriceStrategy(input domain.Strategy) error {
	p := input.Params
	if p.StepPercent < 0 || p.StepPercent > 10 {
		return apperror.New(apperror.ErrValidation, "step_percent must be between 0 and 10")
	}
	if p.MinPriceRub != nil && *p.MinPriceRub < 0 {
		return apperror.New(apperror.ErrValidation, "min_price_rub must be non-negative")
	}
	if p.MaxPriceRub != nil && *p.MaxPriceRub < 0 {
		return apperror.New(apperror.ErrValidation, "max_price_rub must be non-negative")
	}
	if p.MinPriceRub != nil && p.MaxPriceRub != nil && *p.MinPriceRub > *p.MaxPriceRub {
		return apperror.New(apperror.ErrValidation, "min_price_rub must be less than or equal to max_price_rub")
	}
	if p.PriceApplyMode != "" && p.PriceApplyMode != domain.PriceApplyModeDryRun && p.PriceApplyMode != domain.PriceApplyModeAuto {
		return apperror.New(apperror.ErrValidation, "price_apply_mode must be dry_run or auto")
	}
	if p.OverstockDays < 0 || p.LowStockDays < 0 {
		return apperror.New(apperror.ErrValidation, "stock day thresholds must be non-negative")
	}
	if p.MaxPriceChangesPerDay < 0 {
		return apperror.New(apperror.ErrValidation, "max_price_changes_per_day must be non-negative")
	}
	if p.MaxAllowedDRRPercent != nil && (*p.MaxAllowedDRRPercent < 0 || *p.MaxAllowedDRRPercent > 100) {
		return apperror.New(apperror.ErrValidation, "max_allowed_drr_percent must be between 0 and 100")
	}
	if p.PeakUpliftPercent < 0 || p.PeakUpliftPercent > 100 || p.DeadDiscountPercent < 0 || p.DeadDiscountPercent > 100 {
		return apperror.New(apperror.ErrValidation, "peak/dead percentages must be between 0 and 100")
	}
	if p.MaxDiscountPercent < 0 || p.MaxDiscountPercent > 95 {
		return apperror.New(apperror.ErrValidation, "max_discount_percent must be between 0 and 95")
	}
	if p.UndercutPercent < 0 || p.UndercutPercent > 95 {
		return apperror.New(apperror.ErrValidation, "undercut_percent must be between 0 and 95")
	}
	// Upward moves need a ceiling, but the engine already skips them without one
	// ("max_price_required_for_increase") — a down-only inventory strategy is
	// valid, so max_price_rub stays optional.
	return nil
}

func (s *StrategyService) Delete(ctx context.Context, workspaceID, strategyID uuid.UUID) error {
	return s.queries.DeleteStrategyInWorkspace(ctx, sqlcgen.DeleteStrategyInWorkspaceParams{
		ID:          uuidToPgtype(strategyID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
}

func (s *StrategyService) AttachBinding(ctx context.Context, workspaceID, strategyID uuid.UUID, campaignID, productID *uuid.UUID) (*domain.StrategyBinding, error) {
	qtx, tx, err := s.queries.BeginStrategyOwnershipTx(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to start strategy ownership check")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	strategyRow, err := qtx.GetStrategyByIDAndWorkspace(ctx, sqlcgen.GetStrategyByIDAndWorkspaceParams{
		ID: uuidToPgtype(strategyID), WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "strategy or binding target not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load strategy for ownership check")
	}
	strategy := strategyFromSqlc(strategyRow)
	if strategyRequiresLiveOwnership(strategy.IsActive, strategy.Params.AutomationLevel) {
		conflict, conflictErr := qtx.HasLiveStrategyOwnershipConflictForScope(ctx, sqlcgen.HasLiveStrategyOwnershipConflictForScopeParams{
			WorkspaceID: uuidToPgtype(workspaceID), StrategyID: uuidToPgtype(strategyID),
			CampaignID: nullableUUIDToPgtype(campaignID), ProductID: nullableUUIDToPgtype(productID),
		})
		if conflictErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to check live strategy ownership")
		}
		if conflict {
			return nil, apperror.New(apperror.ErrValidation, liveStrategyOwnershipConflictMessage)
		}
	}

	row, err := qtx.CreateStrategyBindingInWorkspace(ctx, sqlcgen.CreateStrategyBindingInWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		StrategyID:  uuidToPgtype(strategyID),
		CampaignID:  nullableUUIDToPgtype(campaignID),
		ProductID:   nullableUUIDToPgtype(productID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrNotFound, "strategy or binding target not found")
		}
		return nil, apperror.New(apperror.ErrInternal, "failed to attach strategy")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to commit strategy binding ownership")
	}

	result := bindingFromSqlc(row)
	return &result, nil
}

func (s *StrategyService) DetachBinding(ctx context.Context, workspaceID, bindingID uuid.UUID) error {
	return s.queries.DeleteStrategyBindingInWorkspace(ctx, sqlcgen.DeleteStrategyBindingInWorkspaceParams{
		ID:          uuidToPgtype(bindingID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
}

// ListActive returns all active strategies for a workspace (used by bid automation worker).
func (s *StrategyService) ListActive(ctx context.Context, workspaceID uuid.UUID) ([]domain.Strategy, error) {
	rows, err := s.queries.ListActiveStrategiesByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}

	result := make([]domain.Strategy, len(rows))
	for i, row := range rows {
		result[i] = strategyFromSqlc(row)
		bindings, _ := s.queries.ListStrategyBindings(ctx, uuidToPgtype(result[i].ID))
		for _, b := range bindings {
			result[i].Bindings = append(result[i].Bindings, bindingFromSqlc(b))
		}
	}
	return result, nil
}

func strategyFromSqlc(row sqlcgen.Strategy) domain.Strategy {
	s := domain.Strategy{
		ID:              uuidFromPgtype(row.ID),
		WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		Name:            row.Name,
		Type:            row.Type,
		IsActive:        row.IsActive,
		CreatedAt:       row.CreatedAt.Time,
		UpdatedAt:       row.UpdatedAt.Time,
	}
	if len(row.Params) > 0 {
		_ = json.Unmarshal(row.Params, &s.Params)
	}
	return s
}

func bindingFromSqlc(row sqlcgen.StrategyBinding) domain.StrategyBinding {
	b := domain.StrategyBinding{
		ID:         uuidFromPgtype(row.ID),
		StrategyID: uuidFromPgtype(row.StrategyID),
		CreatedAt:  row.CreatedAt.Time,
	}
	if row.CampaignID.Valid {
		id := uuidFromPgtype(row.CampaignID)
		b.CampaignID = &id
	}
	if row.ProductID.Valid {
		id := uuidFromPgtype(row.ProductID)
		b.ProductID = &id
	}
	return b
}

func nullableUUIDToPgtype(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return uuidToPgtype(*id)
}
