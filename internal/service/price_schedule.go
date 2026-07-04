package service

import (
	"context"
	"errors"
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

	// Auto-revert: exact inverse delta at revert_at.
	if in.RevertAt != nil {
		primaryID := uuidFromPgtype(row.ID)
		inverse := inverseDeltaPercent(in.AdjustmentValue)
		if _, err := s.queries.CreatePriceScheduleEntry(ctx, sqlcgen.CreatePriceScheduleEntryParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(in.SellerCabinetID),
			ScopeType:       in.ScopeType,
			ProductIds:      in.ProductIDs,
			AdjustmentType:  domain.PriceAdjustDeltaPercent,
			AdjustmentValue: inverse,
			ScheduledAt:     pgtype.Timestamptz{Time: *in.RevertAt, Valid: true},
			RevertOf:        uuidToPgtypePtr(&primaryID),
			Comment:         textToPgtype("auto-revert of " + primaryID.String()),
			CreatedBy:       uuidToPgtypePtr(&actorID),
		}); err != nil {
			s.logger.Warn().Err(err).Msg("failed to create auto-revert schedule entry")
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
	for i := 0; i < scheduleClaimBatch; i++ {
		row, err := s.queries.ClaimDuePriceScheduleEntry(ctx, pgtype.Timestamptz{Time: now, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return executed, err
		}
		s.executeScheduleEntry(ctx, row)
		executed++
	}
	return executed, nil
}

func (s *RepricerService) executeScheduleEntry(ctx context.Context, row sqlcgen.PriceScheduleEntry) {
	entryID := uuidFromPgtype(row.ID)
	workspaceID := uuidFromPgtype(row.WorkspaceID)
	cabinetID := uuidFromPgtype(row.SellerCabinetID)

	prices, err := s.queries.ListProductPricesByCabinet(ctx, row.SellerCabinetID)
	if err != nil {
		s.failSchedule(ctx, entryID, err.Error())
		return
	}
	floors := s.marginFloors(ctx, workspaceID)

	targetNm := map[int64]bool{}
	if row.ScopeType == domain.PriceScopeList {
		for _, nm := range row.ProductIds {
			targetNm[nm] = true
		}
	}

	adj := domain.ManualPriceAdjustment{Type: row.AdjustmentType, Value: row.AdjustmentValue}
	var intents []priceChangeIntent
	for _, p := range prices {
		if row.ScopeType == domain.PriceScopeList && !targetNm[p.WbProductID] {
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

	taskIDs, err := s.applyIntents(ctx, workspaceID, intents)
	if err != nil {
		s.failSchedule(ctx, entryID, err.Error())
		return
	}
	if _, err := s.queries.UpdatePriceScheduleEntryStatus(ctx, sqlcgen.UpdatePriceScheduleEntryStatusParams{
		ID:              uuidToPgtype(entryID),
		Status:          domain.PriceScheduleDone,
		ExecutedTaskIds: uuidsToPgtype(taskIDs),
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to mark schedule entry done")
	}
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
	if in.ScopeType == domain.PriceScopeList && len(in.ProductIDs) == 0 {
		return apperror.New(apperror.ErrValidation, "scope_type=list requires product_ids")
	}
	switch in.AdjustmentType {
	case domain.PriceAdjustDeltaPercent, domain.PriceAdjustTargetRub:
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
