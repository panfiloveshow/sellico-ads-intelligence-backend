package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const scheduleClaimBatch = 200

// CreateSchedule validates and inserts a scheduled price change. When RevertAt is
// set (delta_percent only in v1) it auto-creates a paired inverse revert entry.
func (s *RepricerService) CreateSchedule(ctx context.Context, actorID, workspaceID uuid.UUID, in domain.PriceScheduleInput) (*domain.PriceScheduleEntry, error) {
	if err := validateScheduleInput(in, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := s.requireCabinetInWorkspace(ctx, workspaceID, in.SellerCabinetID); err != nil {
		return nil, err
	}
	row, err := s.queries.CreatePriceScheduleEntry(ctx, sqlcgen.CreatePriceScheduleEntryParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		SellerCabinetID:  uuidToPgtype(in.SellerCabinetID),
		ScopeType:        in.ScopeType,
		ProductIds:       in.ProductIDs,
		AdjustmentType:   in.AdjustmentType,
		AdjustmentValue:  in.AdjustmentValue,
		Direction:        textToPgtype(in.Direction),
		ScheduledAt:      pgtype.Timestamptz{Time: in.ScheduledAt, Valid: true},
		RevertAt:         optionalTimestamptz(in.RevertAt),
		RevertToPrevious: in.RevertToPrevious,
		Comment:          textToPgtype(in.Comment),
		CreatedBy:        uuidToPgtypePtr(&actorID),
	})
	if err != nil {
		return nil, err
	}

	// Auto-revert: exact inverse delta at revert_at. The repository is not exposed
	// through a transaction here, so a failed pair creation is compensated by
	// making the primary non-executable and returning the error to the caller.
	if in.RevertAt != nil {
		primaryID := uuidFromPgtype(row.ID)
		signed := signedScheduleDelta(in.AdjustmentValue, in.Direction)
		inverse := inverseDeltaPercent(signed)
		revertDirection := domain.PriceDirectionUp
		if inverse < 0 {
			revertDirection = domain.PriceDirectionDown
		}
		if _, err := s.queries.CreatePriceScheduleEntry(ctx, sqlcgen.CreatePriceScheduleEntryParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(in.SellerCabinetID),
			ScopeType:       in.ScopeType,
			ProductIds:      in.ProductIDs,
			AdjustmentType:  domain.PriceAdjustDeltaPercent,
			AdjustmentValue: math.Abs(inverse),
			Direction:       textToPgtype(revertDirection),
			ScheduledAt:     pgtype.Timestamptz{Time: *in.RevertAt, Valid: true},
			RevertOf:        uuidToPgtypePtr(&primaryID),
			Comment:         textToPgtype("auto-revert of " + primaryID.String()),
			CreatedBy:       uuidToPgtypePtr(&actorID),
		}); err != nil {
			compensationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, compensationErr := s.queries.UpdatePriceScheduleEntryStatus(compensationCtx, sqlcgen.UpdatePriceScheduleEntryStatusParams{
				ID:     row.ID,
				Status: domain.PriceScheduleFailed,
				Error:  pgtype.Text{String: "auto-revert pair creation failed", Valid: true},
			})
			if compensationErr != nil {
				return nil, fmt.Errorf("create auto-revert: %w; disable primary schedule: %v", err, compensationErr)
			}
			return nil, fmt.Errorf("create auto-revert: %w", err)
		}
	}

	entry := priceScheduleEntryFromSqlc(row)
	return &entry, nil
}

// ListSchedules returns schedule entries for a workspace (optionally by status).
func (s *RepricerService) ListSchedules(ctx context.Context, workspaceID uuid.UUID, status string, limit, offset int32) ([]domain.PriceScheduleEntry, error) {
	statusFilter := pgtype.Text{}
	if status != "" {
		statusFilter = pgtype.Text{String: status, Valid: true}
	}
	rows, err := s.queries.ListPriceScheduleEntriesByWorkspace(ctx, uuidToPgtype(workspaceID), statusFilter, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PriceScheduleEntry, len(rows))
	for i, row := range rows {
		out[i] = priceScheduleEntryFromSqlc(row)
	}
	return out, nil
}

// CancelSchedule marks a planned entry canceled.
func (s *RepricerService) CancelSchedule(ctx context.Context, workspaceID, entryID uuid.UUID) error {
	row, err := s.queries.GetPriceScheduleEntry(ctx, uuidToPgtype(entryID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperror.New(apperror.ErrNotFound, "schedule entry not found")
		}
		return err
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return apperror.New(apperror.ErrNotFound, "schedule entry not found")
	}
	if row.Status != domain.PriceSchedulePlanned {
		return apperror.New(apperror.ErrValidation, "only planned entries can be canceled")
	}
	_, err = s.queries.UpdatePriceScheduleEntryStatus(ctx, sqlcgen.UpdatePriceScheduleEntryStatusParams{
		ID:     uuidToPgtype(entryID),
		Status: domain.PriceScheduleCanceled,
	})
	return err
}

// ExecuteDueSchedules claims and runs every due schedule entry across all
// workspaces. Returns the number executed.
func (s *RepricerService) ExecuteDueSchedules(ctx context.Context, now time.Time) (int, error) {
	executed := 0
	deferred := make([]uuid.UUID, 0)
	defer func() {
		for _, entryID := range deferred {
			if _, err := s.queries.UpdatePriceScheduleEntryStatus(context.WithoutCancel(ctx), sqlcgen.UpdatePriceScheduleEntryStatusParams{
				ID:     uuidToPgtype(entryID),
				Status: domain.PriceSchedulePlanned,
			}); err != nil {
				s.logger.Error().Err(err).Str("schedule_entry_id", entryID.String()).Msg("failed to requeue paused price schedule")
			}
		}
	}()
	for i := 0; i < scheduleClaimBatch; i++ {
		row, err := s.queries.ClaimDuePriceScheduleEntry(ctx, pgtype.Timestamptz{Time: now, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return executed, err
		}
		if s.executeScheduleEntry(ctx, row) {
			deferred = append(deferred, uuidFromPgtype(row.ID))
			continue
		}
		executed++
	}
	return executed, nil
}

// executeScheduleEntry returns true when a paused entry must be requeued.
func (s *RepricerService) executeScheduleEntry(ctx context.Context, row sqlcgen.PriceScheduleEntry) bool {
	entryID := uuidFromPgtype(row.ID)
	workspaceID := uuidFromPgtype(row.WorkspaceID)
	cabinetID := uuidFromPgtype(row.SellerCabinetID)
	if row.RevertOf.Valid {
		primary, err := s.queries.GetPriceScheduleEntry(ctx, row.RevertOf)
		if err != nil || uuidFromPgtype(primary.WorkspaceID) != workspaceID {
			s.failSchedule(ctx, entryID, "auto-revert primary schedule not found")
			return false
		}
		switch autoRevertPrimaryDisposition(primary.Status) {
		case "defer":
			return true
		case "reject":
			s.failSchedule(ctx, entryID, "auto-revert primary schedule did not complete")
			return false
		}
	}
	if err := s.requireCabinetInWorkspace(ctx, workspaceID, cabinetID); err != nil {
		s.failSchedule(ctx, entryID, "seller cabinet does not belong to schedule workspace")
		return false
	}

	// Freeze switch: a paused cabinet defers its scheduled entries (they stay
	// 'planned' and fire once unfrozen).
	if paused, err := s.queries.PausedCabinets(ctx, row.WorkspaceID); err == nil {
		if _, frozen := paused[cabinetID.String()]; frozen {
			return true
		}
	}

	prices, err := s.queries.ListProductPricesByCabinet(ctx, row.SellerCabinetID)
	if err != nil {
		s.failSchedule(ctx, entryID, err.Error())
		return false
	}
	floors := s.marginFloors(ctx, workspaceID)

	targetNm := map[int64]bool{}
	if row.ScopeType == domain.PriceScopeList || row.ScopeType == domain.PriceScopeProduct {
		for _, nm := range row.ProductIds {
			targetNm[nm] = true
		}
	}

	adjustmentValue := row.AdjustmentValue
	if row.AdjustmentType == domain.PriceAdjustDeltaPercent {
		adjustmentValue = signedScheduleDelta(row.AdjustmentValue, row.Direction.String)
	}
	adj := domain.ManualPriceAdjustment{Type: row.AdjustmentType, Value: adjustmentValue}
	var intents []priceChangeIntent
	for _, p := range prices {
		if !scheduleIncludesProduct(row.ScopeType, targetNm, p.WbProductID) {
			continue
		}
		cur := productPriceFromSqlc(p)
		newBase := applyAdjustment(cur.PriceRub, adj)
		if newBase <= 0 {
			continue
		}
		floor := floors[cur.WBProductID]
		if floor > 0 && effectiveOf(newBase, cur.DiscountPercent) < floor {
			newBase = basePriceForTarget(floor, cur.DiscountPercent)
		}
		if newBase == cur.PriceRub {
			continue
		}
		eid := entryID
		intents = append(intents, priceChangeIntent{
			CabinetID:       cabinetID,
			NmID:            cur.WBProductID,
			OldPriceRub:     cur.PriceRub,
			NewPriceRub:     newBase,
			OldDiscount:     cur.DiscountPercent,
			NewDiscount:     cur.DiscountPercent,
			MinPriceRub:     floor,
			Reason:          "scheduled price change",
			Source:          domain.PriceSourceSchedule,
			ScheduleEntryID: &eid,
		})
	}
	if !hasApplicablePriceChanges(intents) {
		s.failSchedule(ctx, entryID, "no applicable price changes")
		return false
	}

	taskIDs, err := s.applyIntents(ctx, workspaceID, intents)
	if err != nil {
		s.failSchedule(ctx, entryID, err.Error())
		return false
	}
	if _, err := s.queries.UpdatePriceScheduleEntryStatus(ctx, sqlcgen.UpdatePriceScheduleEntryStatusParams{
		ID:              uuidToPgtype(entryID),
		Status:          domain.PriceScheduleDone,
		ExecutedTaskIds: uuidsToPgtype(taskIDs),
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to mark schedule entry done")
	}
	return false
}

func (s *RepricerService) requireCabinetInWorkspace(ctx context.Context, workspaceID, cabinetID uuid.UUID) error {
	if cabinetID == uuid.Nil {
		return apperror.New(apperror.ErrValidation, "seller_cabinet_id is required")
	}
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil || uuidFromPgtype(cabinet.WorkspaceID) != workspaceID {
		return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	return nil
}

func (s *RepricerService) failSchedule(ctx context.Context, entryID uuid.UUID, reason string) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if _, err := s.queries.UpdatePriceScheduleEntryStatus(ctx, sqlcgen.UpdatePriceScheduleEntryStatusParams{
		ID:     uuidToPgtype(entryID),
		Status: domain.PriceScheduleFailed,
		Error:  pgtype.Text{String: reason, Valid: reason != ""},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to mark schedule entry failed")
	}
}

func validateScheduleInput(in domain.PriceScheduleInput, now time.Time) error {
	switch in.ScopeType {
	case domain.PriceScopeAll, domain.PriceScopeList, domain.PriceScopeProduct:
	default:
		return apperror.New(apperror.ErrValidation, "invalid scope_type")
	}
	switch in.ScopeType {
	case domain.PriceScopeAll:
		if len(in.ProductIDs) != 0 {
			return apperror.New(apperror.ErrValidation, "scope_type=all does not accept product_ids")
		}
	case domain.PriceScopeProduct:
		if len(in.ProductIDs) != 1 || in.ProductIDs[0] <= 0 {
			return apperror.New(apperror.ErrValidation, "scope_type=product requires exactly one positive product_id")
		}
	case domain.PriceScopeList:
		if err := validateScheduleProductIDs(in.ProductIDs); err != nil {
			return err
		}
	}
	switch in.AdjustmentType {
	case domain.PriceAdjustDeltaPercent:
		if in.Direction != domain.PriceDirectionUp && in.Direction != domain.PriceDirectionDown {
			return apperror.New(apperror.ErrValidation, "delta_percent requires direction up or down")
		}
		if in.AdjustmentValue <= 0 {
			return apperror.New(apperror.ErrValidation, "delta_percent adjustment_value must be positive")
		}
		if err := validatePriceAdjustment(domain.ManualPriceAdjustment{Type: in.AdjustmentType, Value: in.AdjustmentValue}, true); err != nil {
			return err
		}
	case domain.PriceAdjustTargetRub:
		if in.Direction != "" {
			return apperror.New(apperror.ErrValidation, "target_rub does not accept direction")
		}
		if err := validatePriceAdjustment(domain.ManualPriceAdjustment{Type: in.AdjustmentType, Value: in.AdjustmentValue}, true); err != nil {
			return err
		}
	default:
		return apperror.New(apperror.ErrValidation, "invalid adjustment_type")
	}
	if !in.ScheduledAt.After(now) {
		return apperror.New(apperror.ErrValidation, "scheduled_at must be in the future")
	}
	if in.RevertAt != nil {
		if !in.RevertAt.After(in.ScheduledAt) {
			return apperror.New(apperror.ErrValidation, "revert_at must be after scheduled_at")
		}
		// Auto-revert restores the exact base price only for delta_percent.
		if in.AdjustmentType != domain.PriceAdjustDeltaPercent {
			return apperror.New(apperror.ErrValidation, "revert_at is supported for delta_percent adjustments only in v1")
		}
	}
	return nil
}

func validateScheduleProductIDs(ids []int64) error {
	if len(ids) == 0 {
		return apperror.New(apperror.ErrValidation, "scope_type=list requires product_ids")
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return apperror.New(apperror.ErrValidation, "product_ids must be positive")
		}
		if _, duplicate := seen[id]; duplicate {
			return apperror.New(apperror.ErrValidation, "product_ids must be unique")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func signedScheduleDelta(value float64, direction string) float64 {
	value = math.Abs(value)
	if direction == domain.PriceDirectionDown {
		return -value
	}
	return value
}

func scheduleIncludesProduct(scopeType string, targetNm map[int64]bool, nm int64) bool {
	return scopeType == domain.PriceScopeAll || targetNm[nm]
}

func autoRevertPrimaryDisposition(status string) string {
	switch status {
	case domain.PriceScheduleDone:
		return "execute"
	case domain.PriceSchedulePlanned, domain.PriceScheduleExecuting:
		return "defer"
	default:
		return "reject"
	}
}

func hasApplicablePriceChanges(intents []priceChangeIntent) bool {
	return len(intents) > 0
}

// inverseDeltaPercent returns the delta that exactly undoes a delta_percent move:
// applying (1+v/100) then (1+inv/100) yields 1.
func inverseDeltaPercent(v float64) float64 {
	return (1/(1+v/100) - 1) * 100
}

func optionalTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func uuidsToPgtype(ids []uuid.UUID) []pgtype.UUID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = uuidToPgtype(id)
	}
	return out
}

func priceScheduleEntryFromSqlc(row sqlcgen.PriceScheduleEntry) domain.PriceScheduleEntry {
	e := domain.PriceScheduleEntry{
		ID:               uuidFromPgtype(row.ID),
		WorkspaceID:      uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID:  uuidFromPgtype(row.SellerCabinetID),
		ScopeType:        row.ScopeType,
		ProductIDs:       row.ProductIds,
		AdjustmentType:   row.AdjustmentType,
		AdjustmentValue:  row.AdjustmentValue,
		RevertToPrevious: row.RevertToPrevious,
		Status:           row.Status,
	}
	if row.Direction.Valid {
		e.Direction = row.Direction.String
	}
	if row.ScheduledAt.Valid {
		e.ScheduledAt = row.ScheduledAt.Time
	}
	if row.RevertAt.Valid {
		v := row.RevertAt.Time
		e.RevertAt = &v
	}
	if row.RevertOf.Valid {
		v := uuidFromPgtype(row.RevertOf)
		e.RevertOf = &v
	}
	if row.Error.Valid {
		e.Error = &row.Error.String
	}
	if row.Comment.Valid {
		e.Comment = &row.Comment.String
	}
	for _, id := range row.ExecutedTaskIds {
		e.ExecutedTaskIDs = append(e.ExecutedTaskIDs, uuidFromPgtype(id))
	}
	if row.CreatedAt.Valid {
		e.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		e.UpdatedAt = row.UpdatedAt.Time
	}
	return e
}
