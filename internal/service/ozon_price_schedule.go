package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ozonScheduleBatch caps one executor pass (mirrors the WB scheduleClaimBatch).
const ozonScheduleBatch = 200

// --- CRUD (mirrors the WB price calendar, per-SKU absolute prices) ---

// CreateSchedule plans one future Ozon price change, optionally with an
// auto-revert at ends_at. A price below the computed margin floor is a
// non-blocking warning in the response — the calendar is an explicit human
// decision, so it is recorded, not rejected.
func (s *OzonRepricerService) CreateSchedule(ctx context.Context, workspaceID, cabinetID uuid.UUID, in domain.OzonPriceScheduleInput) (*domain.OzonPriceScheduleEntry, error) {
	now := time.Now().UTC()
	if in.SKU <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "sku must be positive")
	}
	if in.ScheduledPriceRub <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "scheduled_price_rub must be positive")
	}
	if in.RevertPriceRub != nil && *in.RevertPriceRub <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "revert_price_rub must be positive")
	}
	if !in.StartsAt.After(now) {
		return nil, apperror.New(apperror.ErrValidation, "starts_at must be in the future")
	}
	if in.EndsAt != nil && !in.EndsAt.After(in.StartsAt) {
		return nil, apperror.New(apperror.ErrValidation, "ends_at must be after starts_at")
	}
	cabinet, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, err
	}

	// Mirror row: offer_id, current price (the default revert target) and the
	// economics for the floor warning.
	var offerID pgtype.Text
	warning := ""
	revertPrice := in.RevertPriceRub
	if mirror, mirrorErr := s.queries.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID), Sku: in.SKU,
	}); mirrorErr == nil {
		offerID = mirror.OfferID
		if in.EndsAt != nil && revertPrice == nil {
			// Auto-revert defaults to the price the product has right now.
			if current := pgNumericToFloat(mirror.PriceRub); current > 0 {
				revertPrice = &current
			}
		}
		if floor := s.scheduleFloorFor(ctx, cabinet.ID, mirror); floor > 0 && in.ScheduledPriceRub < floor {
			warning = fmt.Sprintf("scheduled price %.2f₽ is below the margin floor %.2f₽", in.ScheduledPriceRub, floor)
		}
	} else if !errors.Is(mirrorErr, pgx.ErrNoRows) {
		return nil, fmt.Errorf("load product price mirror: %w", mirrorErr)
	}
	if in.EndsAt != nil && revertPrice == nil {
		return nil, apperror.New(apperror.ErrValidation, "revert_price_rub is required with ends_at when the product has no synced price to restore")
	}

	row, err := s.queries.CreateOzonPriceScheduleEntry(ctx, sqlcgen.CreateOzonPriceScheduleEntryParams{
		SellerCabinetID:   uuidToPgtype(cabinet.ID),
		Sku:               in.SKU,
		OfferID:           offerID,
		ScheduledPriceRub: floatToPgNumeric(in.ScheduledPriceRub),
		RevertPriceRub:    floatPtrToPgNumeric(revertPrice),
		StartsAt:          pgtype.Timestamptz{Time: in.StartsAt.UTC(), Valid: true},
		EndsAt:            optionalTimestamptz(in.EndsAt),
	})
	if err != nil {
		return nil, fmt.Errorf("create ozon price schedule: %w", err)
	}
	entry := ozonPriceScheduleEntryFromSqlc(row)
	entry.Warning = warning
	return &entry, nil
}

// scheduleFloorFor computes the informational margin floor for one mirror row
// (zero target margin, Sellico economics fallback). 0 = not computable.
func (s *OzonRepricerService) scheduleFloorFor(ctx context.Context, cabinetID uuid.UUID, row sqlcgen.OzonProductPrice) float64 {
	var econ *sqlcgen.OzonProductEconomic
	if row.OfferID.Valid {
		if economics, err := s.loadCabinetEconomics(ctx, cabinetID); err == nil {
			if e, ok := economics[row.OfferID.String]; ok {
				econ = &e
			}
		}
	}
	floor, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:      ozonEffectiveNetPrice(pgNumericToFloat(row.NetPriceRub), econ),
		CommissionFBOPct: pgNumericToFloat(row.CommissionFboPct),
		CommissionFBSPct: pgNumericToFloat(row.CommissionFbsPct),
		AcquiringRub:     pgNumericToFloat(row.AcquiringPct),
	})
	if reason != "" {
		return 0
	}
	return floor
}

// ListSchedules returns the cabinet's calendar entries (optionally by status).
func (s *OzonRepricerService) ListSchedules(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, limit, offset int32) ([]domain.OzonPriceScheduleEntry, int64, error) {
	if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	statusFilter := pgtype.Text{}
	if status != "" {
		statusFilter = pgtype.Text{String: status, Valid: true}
	}
	total, err := s.queries.CountOzonPriceScheduleEntriesByCabinet(ctx, sqlcgen.CountOzonPriceScheduleEntriesByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Status:          statusFilter,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ozon price schedules")
	}
	rows, err := s.queries.ListOzonPriceScheduleEntriesByCabinet(ctx, sqlcgen.ListOzonPriceScheduleEntriesByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Status:          statusFilter,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ozon price schedules")
	}
	out := make([]domain.OzonPriceScheduleEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, ozonPriceScheduleEntryFromSqlc(row))
	}
	return out, total, nil
}

// CancelSchedule cancels a pending entry (applied/reverted history is
// immutable).
func (s *OzonRepricerService) CancelSchedule(ctx context.Context, workspaceID, entryID uuid.UUID) error {
	row, err := s.queries.GetOzonPriceScheduleEntry(ctx, uuidToPgtype(entryID))
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "schedule entry not found")
	}
	if err != nil {
		return fmt.Errorf("load schedule entry: %w", err)
	}
	if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, uuidFromPgtype(row.SellerCabinetID)); err != nil {
		return apperror.New(apperror.ErrNotFound, "schedule entry not found")
	}
	if row.Status != domain.OzonScheduleStatusPending {
		return apperror.New(apperror.ErrValidation, "only pending entries can be cancelled")
	}
	if _, err := s.queries.CancelOzonPriceScheduleEntry(ctx, row.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperror.New(apperror.ErrValidation, "only pending entries can be cancelled")
		}
		return fmt.Errorf("cancel schedule entry: %w", err)
	}
	return nil
}

// --- execution (ozon:execute_schedules, every 15m) ---

// ExecuteDueSchedules applies due pending entries and reverts applied entries
// whose ends_at has passed, across all cabinets. Paused cabinets are skipped
// (entries stay pending/applied and fire once unfrozen). Returns the number
// of price writes performed.
func (s *OzonRepricerService) ExecuteDueSchedules(ctx context.Context, now time.Time) (int, error) {
	nowTz := pgtype.Timestamptz{Time: now, Valid: true}
	executed := 0
	var errs []error

	due, err := s.queries.ListDueOzonPriceSchedules(ctx, sqlcgen.ListDueOzonPriceSchedulesParams{Now: nowTz, Limit: ozonScheduleBatch})
	if err != nil {
		return 0, fmt.Errorf("list due ozon schedules: %w", err)
	}
	for _, entry := range due {
		applied, execErr := s.executeScheduleEntry(ctx, entry, false, now)
		if execErr != nil {
			errs = append(errs, execErr)
		}
		if applied {
			executed++
		}
	}

	reverts, err := s.queries.ListDueOzonPriceScheduleReverts(ctx, sqlcgen.ListDueOzonPriceScheduleRevertsParams{Now: nowTz, Limit: ozonScheduleBatch})
	if err != nil {
		return executed, fmt.Errorf("list due ozon schedule reverts: %w", err)
	}
	for _, entry := range reverts {
		applied, execErr := s.executeScheduleEntry(ctx, entry, true, now)
		if execErr != nil {
			errs = append(errs, execErr)
		}
		if applied {
			executed++
		}
	}
	return executed, errors.Join(errs...)
}

// executeScheduleEntry performs one calendar write: the scheduled price
// (revert=false) or the revert price (revert=true), through the audited
// manual path. Returns true when a write reached Ozon successfully.
func (s *OzonRepricerService) executeScheduleEntry(ctx context.Context, entry sqlcgen.OzonPriceScheduleEntry, revert bool, now time.Time) (bool, error) {
	entryID := uuidFromPgtype(entry.ID)
	cabinetID := uuidFromPgtype(entry.SellerCabinetID)

	cabinet, creds, err := s.resolveScheduleCabinet(ctx, cabinetID)
	if err != nil {
		s.failScheduleEntry(ctx, entry.ID, revert, err.Error())
		return false, nil // permanent per-entry failure, not an executor error
	}
	// Freeze switch: a paused cabinet defers its calendar (entries keep their
	// current status and fire on a later pass once unfrozen).
	if cabinet.RepricerPausedUntil != nil && cabinet.RepricerPausedUntil.After(now) {
		return false, nil
	}

	price := pgNumericToFloat(entry.ScheduledPriceRub)
	reason := "schedule"
	if revert {
		price = pgNumericToFloat(entry.RevertPriceRub)
		reason = "schedule revert"
	}
	if price <= 0 {
		s.failScheduleEntry(ctx, entry.ID, revert, "schedule has no positive price to apply")
		return false, nil
	}

	// Hard hourly budget: when the SKU has no write headroom the entry is
	// left as is and retried on the next 15m pass.
	if blocked := s.hourlyWriteGuard(ctx, cabinet.ID, entry.Sku, ozonHardHourlyWriteCap, now); blocked != "" {
		s.logger.Warn().Str("schedule_entry_id", entryID.String()).Int64("sku", entry.Sku).Str("blocked", blocked).Msg("ozon schedule deferred by hourly budget")
		return false, nil
	}

	var oldPrice pgtype.Numeric
	if mirror, mirrorErr := s.queries.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID), Sku: entry.Sku,
	}); mirrorErr == nil {
		oldPrice = mirror.PriceRub
	}

	contextJSON, _ := json.Marshal(ozonPriceDecisionContext{
		ActorType:  "schedule",
		Reason:     reason,
		ScheduleID: entryID.String(),
	})
	change, err := s.queries.InsertOzonPriceChange(ctx, sqlcgen.InsertOzonPriceChangeParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
		Sku:             entry.Sku,
		OfferID:         entry.OfferID,
		OldPriceRub:     oldPrice,
		NewPriceRub:     floatToPgNumeric(price),
		Reason:          textToPgtype(reason),
		Source:          domain.PriceSourceManual,
		Status:          domain.OzonPriceStatusPending,
		DecisionContext: contextJSON,
	})
	if err != nil {
		return false, fmt.Errorf("record scheduled price change for sku %d: %w", entry.Sku, err)
	}

	applyErr := s.applyIntents(ctx, cabinet.ID, creds, []ozonPriceIntent{{
		ChangeID:    change.ID,
		SKU:         entry.Sku,
		NewPriceRub: price,
	}})

	fresh, freshErr := s.queries.GetOzonPriceChangeByID(ctx, change.ID)
	if freshErr != nil || fresh.Status != domain.OzonPriceStatusApplied {
		message := "ozon rejected the scheduled price write"
		if freshErr == nil && fresh.Error.Valid && fresh.Error.String != "" {
			message = fresh.Error.String
		} else if applyErr != nil {
			message = truncateError(applyErr.Error())
		}
		s.failScheduleEntry(ctx, entry.ID, revert, message)
		return false, nil
	}

	status := domain.OzonScheduleStatusApplied
	if revert {
		status = domain.OzonScheduleStatusReverted
	}
	if _, markErr := s.queries.MarkOzonPriceScheduleResult(ctx, sqlcgen.MarkOzonPriceScheduleResultParams{
		ID: entry.ID, Status: status,
	}); markErr != nil {
		s.logger.Warn().Err(markErr).Str("schedule_entry_id", entryID.String()).Msg("failed to finalize ozon schedule entry")
	}
	return true, nil
}

// resolveScheduleCabinet loads one Ozon cabinet by id (no workspace gate —
// the executor runs in the worker) and returns Seller API credentials.
func (s *OzonRepricerService) resolveScheduleCabinet(ctx context.Context, cabinetID uuid.UUID) (*domain.SellerCabinet, ozon.Credentials, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, ozon.Credentials{}, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrValidation, "seller cabinet is not an Ozon cabinet")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return nil, ozon.Credentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	if !creds.HasSellerAPI() {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrValidation, "seller api credentials are missing for this cabinet")
	}
	return &cabinet, ozonClientCreds(creds), nil
}

func (s *OzonRepricerService) failScheduleEntry(ctx context.Context, entryID pgtype.UUID, revert bool, reason string) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	// A failed revert keeps the fact that the primary WAS applied visible via
	// applied_at; the status still moves to failed so it surfaces in the UI.
	_ = revert
	if _, err := s.queries.MarkOzonPriceScheduleResult(ctx, sqlcgen.MarkOzonPriceScheduleResultParams{
		ID:     entryID,
		Status: domain.OzonScheduleStatusFailed,
		Error:  pgtype.Text{String: reason, Valid: reason != ""},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to mark ozon schedule entry failed")
	}
}

// --- pause + health ---

// SetPause freezes the cabinet's Ozon repricer automation for the given
// number of hours (0 unfreezes immediately). Manual writes are unaffected.
func (s *OzonRepricerService) SetPause(ctx context.Context, workspaceID, cabinetID uuid.UUID, hours int) (*time.Time, error) {
	if hours < 0 || hours > 24*30 {
		return nil, apperror.New(apperror.ErrValidation, "hours must be between 0 and 720")
	}
	if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	until := pgtype.Timestamptz{}
	var untilPtr *time.Time
	if hours > 0 {
		t := time.Now().UTC().Add(time.Duration(hours) * time.Hour)
		until = pgtype.Timestamptz{Time: t, Valid: true}
		untilPtr = &t
	}
	if err := s.queries.SetCabinetRepricerPause(ctx, uuidToPgtype(cabinetID), until, uuidToPgtype(workspaceID)); err != nil {
		return nil, fmt.Errorf("set repricer pause: %w", err)
	}
	return untilPtr, nil
}

// Health returns the one-glance Ozon repricer summary for a cabinet.
func (s *OzonRepricerService) Health(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonRepricerHealth, error) {
	cabinet, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, err
	}
	health := &domain.OzonRepricerHealth{}
	if cabinet.RepricerPausedUntil != nil && cabinet.RepricerPausedUntil.After(time.Now()) {
		health.PausedUntil = cabinet.RepricerPausedUntil
	}
	if last, err := s.queries.LastOzonStrategySweepAt(ctx, uuidToPgtype(cabinet.ID)); err == nil && last.Valid {
		t := last.Time
		health.LastSweepAt = &t
	}
	if pending, err := s.queries.CountPendingOzonPriceSchedulesByCabinet(ctx, uuidToPgtype(cabinet.ID)); err == nil {
		health.PendingSchedules = int(pending)
	}
	summary, err := s.queries.OzonPriceChanges24hSummary(ctx, uuidToPgtype(cabinet.ID))
	if err != nil {
		return nil, fmt.Errorf("summarize ozon price changes: %w", err)
	}
	health.Changes24h = domain.OzonRepricerChanges24h{
		Applied: int(summary.Applied),
		Shadow:  int(summary.Shadow),
		Failed:  int(summary.Failed),
	}

	// products_below_floor: recompute the informational floor (zero target
	// margin, Sellico cost fallback) over the whole mirror — same math as the
	// /ozon/prices read helper.
	prices, err := s.loadCabinetPrices(ctx, cabinet.ID)
	if err != nil {
		return nil, err
	}
	economics, err := s.loadCabinetEconomics(ctx, cabinet.ID)
	if err != nil {
		return nil, err
	}
	below := 0
	for _, row := range prices {
		current := pgNumericToFloat(row.PriceRub)
		if current <= 0 {
			continue
		}
		var econ *sqlcgen.OzonProductEconomic
		if row.OfferID.Valid {
			if e, ok := economics[row.OfferID.String]; ok {
				econ = &e
			}
		}
		floor, reason := computeOzonFloor(ozonFloorInputs{
			NetPriceRub:      ozonEffectiveNetPrice(pgNumericToFloat(row.NetPriceRub), econ),
			CommissionFBOPct: pgNumericToFloat(row.CommissionFboPct),
			CommissionFBSPct: pgNumericToFloat(row.CommissionFbsPct),
			AcquiringRub:     pgNumericToFloat(row.AcquiringPct),
		})
		if reason == "" && floor > 0 && current < floor-0.01 {
			below++
		}
	}
	health.ProductsBelowFloor = below
	return health, nil
}

// --- mapping ---

func ozonPriceScheduleEntryFromSqlc(row sqlcgen.OzonPriceScheduleEntry) domain.OzonPriceScheduleEntry {
	entry := domain.OzonPriceScheduleEntry{
		ID:                uuidFromPgtype(row.ID),
		SellerCabinetID:   uuidFromPgtype(row.SellerCabinetID),
		SKU:               row.Sku,
		OfferID:           pgTextValue(row.OfferID),
		ScheduledPriceRub: roundRub(pgNumericToFloat(row.ScheduledPriceRub)),
		Status:            row.Status,
		Error:             pgTextValue(row.Error),
	}
	if row.RevertPriceRub.Valid {
		v := roundRub(pgNumericToFloat(row.RevertPriceRub))
		entry.RevertPriceRub = &v
	}
	if row.StartsAt.Valid {
		entry.StartsAt = row.StartsAt.Time
	}
	if row.EndsAt.Valid {
		t := row.EndsAt.Time
		entry.EndsAt = &t
	}
	if row.CreatedAt.Valid {
		entry.CreatedAt = row.CreatedAt.Time
	}
	if row.AppliedAt.Valid {
		t := row.AppliedAt.Time
		entry.AppliedAt = &t
	}
	if row.RevertedAt.Valid {
		t := row.RevertedAt.Time
		entry.RevertedAt = &t
	}
	return entry
}

// ozonScheduleIsDue reports whether a pending entry should fire at `now`
// (pure helper, unit-tested).
func ozonScheduleIsDue(status string, startsAt, now time.Time) bool {
	return status == domain.OzonScheduleStatusPending && !startsAt.After(now)
}

// ozonScheduleNeedsRevert reports whether an applied entry should revert at
// `now` (pure helper, unit-tested).
func ozonScheduleNeedsRevert(status string, endsAt *time.Time, revertPriceRub *float64, now time.Time) bool {
	return status == domain.OzonScheduleStatusApplied &&
		endsAt != nil && !endsAt.After(now) &&
		revertPriceRub != nil && *revertPriceRub > 0
}
