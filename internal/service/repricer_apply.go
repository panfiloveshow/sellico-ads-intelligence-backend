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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	priceUploadChunkSize    = 1000
	pricePollMaxAttempts    = 48 // ~4h at the 5-minute sweep cadence
	repricerStateStaleAfter = 30 * time.Minute
)

// priceChangeIntent is one product's intended change, shared by the auto,
// manual-bulk and schedule apply paths.
type priceChangeIntent struct {
	CabinetID       uuid.UUID
	NmID            int64
	OldPriceRub     int64
	NewPriceRub     int64
	OldDiscount     int
	NewDiscount     int
	MinPriceRub     int64
	Reason          string
	Source          string
	StrategyID      *uuid.UUID
	ScheduleEntryID *uuid.UUID
	RollbackOf      *uuid.UUID
	CreatedBy       *uuid.UUID
	DecisionContext *domain.PriceChangeDecisionContext
}

// applyIntents persists pending price_changes and uploads them to WB in ≤1000
// item chunks per cabinet. Returns the created upload-task IDs. On a per-cabinet
// upload failure the cabinet's changes are marked failed and the others proceed.
func (s *RepricerService) applyIntents(ctx context.Context, workspaceID uuid.UUID, intents []priceChangeIntent) ([]uuid.UUID, error) {
	byCabinet := map[uuid.UUID][]priceChangeIntent{}
	for _, it := range intents {
		byCabinet[it.CabinetID] = append(byCabinet[it.CabinetID], it)
	}

	var taskIDs []uuid.UUID
	var applyErrs []error
	for cabinetID, cabinetIntents := range byCabinet {
		if s.pricesEndpointCoolingDown(ctx, cabinetID, wbEndpointPricesUpload) {
			s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("prices upload cooling down, skipping cabinet")
			applyErrs = append(applyErrs, fmt.Errorf("cabinet %s: prices upload cooling down", cabinetID))
			metrics.RepricerUploadsTotal.WithLabelValues("skipped_cooldown").Inc()
			continue
		}
		token, err := crypto.Decrypt(s.mustCabinetToken(ctx, cabinetID), s.encryptionKey)
		if err != nil {
			s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("failed to decrypt cabinet token for price upload")
			applyErrs = append(applyErrs, fmt.Errorf("cabinet %s: decrypt price token: %w", cabinetID, err))
			metrics.RepricerUploadsTotal.WithLabelValues("token_error").Inc()
			continue
		}
		for start := 0; start < len(cabinetIntents); start += priceUploadChunkSize {
			end := start + priceUploadChunkSize
			if end > len(cabinetIntents) {
				end = len(cabinetIntents)
			}
			chunk := cabinetIntents[start:end]
			taskID, err := s.uploadChunk(ctx, workspaceID, cabinetID, token, chunk)
			if taskID != uuid.Nil {
				taskIDs = append(taskIDs, taskID)
			}
			if err != nil {
				s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesUpload, err)
				s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("price upload chunk failed")
				applyErrs = append(applyErrs, fmt.Errorf("cabinet %s chunk %d-%d: %w", cabinetID, start, end, err))
				continue
			}
		}
	}
	return taskIDs, errors.Join(applyErrs...)
}

// uploadChunk inserts pending changes, uploads them to WB, records the upload
// task, then flips the changes to 'uploaded' with the task id.
func (s *RepricerService) uploadChunk(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, chunk []priceChangeIntent) (uuid.UUID, error) {
	items := make([]wb.PriceUpdateItem, 0, len(chunk))
	changeIDs := make([]uuid.UUID, 0, len(chunk))
	var reserveErrs []error
	for _, it := range chunk {
		changeID, err := s.createPendingChange(ctx, workspaceID, it)
		if err != nil {
			reserveErrs = append(reserveErrs, fmt.Errorf("reserve nmID %d: %w", it.NmID, err))
			continue
		}
		changeIDs = append(changeIDs, changeID)
		items = append(items, wb.PriceUpdateItem{NmID: it.NmID, Price: it.NewPriceRub, Discount: it.NewDiscount})
	}
	if len(items) == 0 {
		return uuid.Nil, errors.Join(reserveErrs...)
	}

	wbTaskID, duplicate, err := s.wbClient.UploadPriceTask(ctx, token, items)
	if err != nil {
		metrics.RepricerUploadsTotal.WithLabelValues("submit_failed").Inc()
		s.failChanges(ctx, changeIDs, err.Error())
		return uuid.Nil, errors.Join(append(reserveErrs, err)...)
	}

	task, err := s.queries.CreatePriceUploadTaskAndLinkChanges(ctx, sqlcgen.CreatePriceUploadTaskParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		WbTaskID:        wbTaskID,
		Status:          domain.PriceTaskUploaded,
		ItemsCount:      int32(len(items)),
	}, uuidsToPgtype(changeIDs))
	if err != nil {
		metrics.RepricerUploadsTotal.WithLabelValues("link_failed").Inc()
		// WB may already have accepted the write. Release the local reservations so
		// a retry can recover WB's duplicate task id (208) and reconcile it.
		s.failChanges(ctx, changeIDs, "wb task accepted but local task linking failed: "+err.Error())
		return uuid.Nil, errors.Join(append(reserveErrs, fmt.Errorf("link accepted WB task %d: %w", wbTaskID, err))...)
	}
	taskUUID := uuidFromPgtype(task.ID)
	if duplicate {
		metrics.RepricerUploadsTotal.WithLabelValues("duplicate").Inc()
	} else {
		metrics.RepricerUploadsTotal.WithLabelValues("accepted").Inc()
	}
	if duplicate {
		switch task.Status {
		case domain.PriceTaskFailed:
			return taskUUID, errors.Join(append(reserveErrs, errors.New("duplicate WB price task already failed"))...)
		case domain.PriceTaskPartial:
			if err := s.resolvePartial(ctx, token, task); err != nil {
				return taskUUID, errors.Join(append(reserveErrs, fmt.Errorf("reconcile duplicate partial task: %w", err))...)
			}
			return taskUUID, errors.Join(append(reserveErrs, errors.New("duplicate WB price task completed partially"))...)
		}
	}
	return taskUUID, errors.Join(reserveErrs...)
}

func (s *RepricerService) createPendingChange(ctx context.Context, workspaceID uuid.UUID, it priceChangeIntent) (uuid.UUID, error) {
	var ctxJSON []byte
	if it.DecisionContext != nil {
		ctxJSON, _ = json.Marshal(it.DecisionContext)
	}
	row, err := s.queries.CreatePriceChange(ctx, sqlcgen.CreatePriceChangeParams{
		WorkspaceID:        uuidToPgtype(workspaceID),
		SellerCabinetID:    uuidToPgtype(it.CabinetID),
		StrategyID:         uuidToPgtypePtr(it.StrategyID),
		ScheduleEntryID:    uuidToPgtypePtr(it.ScheduleEntryID),
		WbProductID:        it.NmID,
		OldPriceRub:        it.OldPriceRub,
		NewPriceRub:        it.NewPriceRub,
		OldDiscountPercent: int32(it.OldDiscount),
		NewDiscountPercent: int32(it.NewDiscount),
		MinPriceRub:        pgtype.Int8{Int64: it.MinPriceRub, Valid: it.MinPriceRub > 0},
		Reason:             it.Reason,
		Source:             it.Source,
		WbStatus:           domain.PriceStatusPending,
		CanRollback:        it.Source != domain.PriceSourceRollback,
		RollbackOf:         uuidToPgtypePtr(it.RollbackOf),
		CreatedBy:          uuidToPgtypePtr(it.CreatedBy),
		DecisionContext:    ctxJSON,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return uuidFromPgtype(row.ID), nil
}

func (s *RepricerService) failChanges(ctx context.Context, ids []uuid.UUID, reason string) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	for _, id := range ids {
		if _, err := s.queries.UpdatePriceChangeStatus(ctx, sqlcgen.UpdatePriceChangeStatusParams{
			ID:       uuidToPgtype(id),
			WbStatus: domain.PriceStatusFailed,
			Error:    pgtype.Text{String: reason, Valid: reason != ""},
		}); err != nil {
			s.logger.Warn().Err(err).Msg("failed to mark price change failed")
		}
	}
}

func (s *RepricerService) mustCabinetToken(ctx context.Context, cabinetID uuid.UUID) string {
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return ""
	}
	return cabinet.EncryptedToken
}

// PollUploadTasks advances every pending upload task for a workspace by querying
// WB task status. Returns the number of tasks that reached a terminal state.
func (s *RepricerService) PollUploadTasks(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	staleBefore := pgtype.Timestamptz{Time: time.Now().UTC().Add(-repricerStateStaleAfter), Valid: true}
	if pending, schedules, recoverErr := s.queries.RecoverStaleRepricerState(ctx, staleBefore); recoverErr != nil {
		s.logger.Warn().Err(recoverErr).Msg("failed to recover stale repricer state")
	} else if pending > 0 || schedules > 0 {
		s.logger.Warn().Int64("pending_failed", pending).Int64("schedules_released", schedules).Msg("recovered stale repricer state")
		if pending > 0 {
			metrics.RepricerStateRecoveriesTotal.WithLabelValues("pending_change").Add(float64(pending))
		}
		if schedules > 0 {
			metrics.RepricerStateRecoveriesTotal.WithLabelValues("executing_schedule").Add(float64(schedules))
		}
	}
	tasks, err := s.queries.ListPendingPriceUploadTasks(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, err
	}
	terminal := 0
	for _, task := range tasks {
		cabinetID := uuidFromPgtype(task.SellerCabinetID)
		token, decErr := crypto.Decrypt(s.mustCabinetToken(ctx, cabinetID), s.encryptionKey)
		if decErr != nil {
			s.logger.Warn().Err(decErr).Str("cabinet_id", cabinetID.String()).Int64("wb_task_id", task.WbTaskID).Msg("cannot decrypt token while polling price task")
			if s.bumpPoll(ctx, workspaceID, task, "decrypt cabinet token: "+decErr.Error()) {
				terminal++
			}
			continue
		}
		if s.pollOneTask(ctx, workspaceID, token, task) {
			terminal++
		}
	}
	return terminal, nil
}

// pollOneTask returns true when the task reached a terminal state.
func (s *RepricerService) pollOneTask(ctx context.Context, workspaceID uuid.UUID, token string, task sqlcgen.PriceUploadTask) bool {
	taskUUID := uuidFromPgtype(task.ID)
	status, err := s.wbClient.GetPriceTaskHistory(ctx, token, task.WbTaskID)
	if err != nil {
		s.recordPricesRateLimit(ctx, uuidFromPgtype(task.SellerCabinetID), wbEndpointPricesPoll, err)
		s.logger.Warn().Err(err).Int64("wb_task_id", task.WbTaskID).Msg("price task history poll failed")
		return s.bumpPoll(ctx, workspaceID, task, "history poll: "+err.Error())
	}
	if status == nil {
		buffered, bufferErr := s.wbClient.GetPriceTaskBuffer(ctx, token, task.WbTaskID)
		if bufferErr != nil {
			s.recordPricesRateLimit(ctx, uuidFromPgtype(task.SellerCabinetID), wbEndpointPricesPoll, bufferErr)
			return s.bumpPoll(ctx, workspaceID, task, "buffer poll: "+bufferErr.Error())
		}
		if buffered == nil {
			return s.bumpPoll(ctx, workspaceID, task, "task absent from history and buffer")
		}
		return s.bumpPoll(ctx, workspaceID, task, "")
	}

	switch status.Status {
	case 3: // processed OK
		s.completeTask(ctx, taskUUID, domain.PriceTaskApplied, domain.PriceStatusApplied, "")
		metrics.RepricerUploadsTotal.WithLabelValues("applied").Inc()
		return true
	case 6: // all products errored
		s.completeTask(ctx, taskUUID, domain.PriceTaskFailed, domain.PriceStatusFailed, "all products failed")
		metrics.RepricerUploadsTotal.WithLabelValues("failed").Inc()
		s.notifyPriceResult(ctx, workspaceID, task, "failed")
		return true
	case 5: // partial — resolve per product
		if err := s.resolvePartial(ctx, token, task); err != nil {
			s.logger.Warn().Err(err).Int64("wb_task_id", task.WbTaskID).Msg("failed to resolve partial price task")
			return s.bumpPoll(ctx, workspaceID, task, "resolve partial: "+err.Error())
		}
		s.finishTaskRow(ctx, taskUUID, domain.PriceTaskPartial, "partial errors")
		metrics.RepricerUploadsTotal.WithLabelValues("partial").Inc()
		s.notifyPriceResult(ctx, workspaceID, task, "partial")
		return true
	default:
		return s.bumpPoll(ctx, workspaceID, task, fmt.Sprintf("non-terminal WB status %d", status.Status))
	}
}

func (s *RepricerService) resolvePartial(ctx context.Context, token string, task sqlcgen.PriceUploadTask) error {
	goods, err := s.wbClient.ListPriceTaskHistoryGoods(ctx, token, task.WbTaskID, priceUploadChunkSize, 0)
	if err != nil {
		return err
	}
	if len(goods) == 0 {
		return errors.New("partial task returned no per-good results")
	}
	if err := s.queries.UpdatePriceChangesStatusByTask(ctx, task.ID, domain.PriceStatusApplied); err != nil {
		return err
	}
	var updateErrs []error
	for _, g := range goods {
		reason, failed := partialPriceGoodFailure(g)
		if !failed {
			continue
		}
		if err := s.queries.UpdatePriceChangeStatusByTaskAndNmID(ctx, task.ID, g.NmID, domain.PriceStatusFailed,
			pgtype.Text{String: reason, Valid: true}); err != nil {
			updateErrs = append(updateErrs, fmt.Errorf("nmID %d: %w", g.NmID, err))
		}
	}
	return errors.Join(updateErrs...)
}

func partialPriceGoodFailure(g wb.PriceTaskGood) (string, bool) {
	if g.Status == 3 {
		return "", false
	}
	if g.ErrorText != "" {
		return g.ErrorText, true
	}
	return fmt.Sprintf("WB item status %d", g.Status), true
}

func (s *RepricerService) completeTask(ctx context.Context, taskUUID uuid.UUID, taskStatus, changeStatus, errText string) {
	if err := s.queries.UpdatePriceChangesStatusByTask(ctx, uuidToPgtype(taskUUID), changeStatus); err != nil {
		s.logger.Warn().Err(err).Msg("failed to update task changes status")
	}
	s.finishTaskRow(ctx, taskUUID, taskStatus, errText)
}

func (s *RepricerService) finishTaskRow(ctx context.Context, taskUUID uuid.UUID, status, errText string) {
	if _, err := s.queries.UpdatePriceUploadTask(ctx, sqlcgen.UpdatePriceUploadTaskParams{
		ID:          uuidToPgtype(taskUUID),
		Status:      status,
		CompletedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Error:       pgtype.Text{String: errText, Valid: errText != ""},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to finish price upload task")
	}
}

func (s *RepricerService) bumpPoll(ctx context.Context, workspaceID uuid.UUID, task sqlcgen.PriceUploadTask, reason string) bool {
	next := task.PollCount + 1
	if int(next) >= pricePollMaxAttempts {
		finalReason := "poll_timeout"
		if reason != "" {
			finalReason += ": " + reason
		}
		s.completeTask(ctx, uuidFromPgtype(task.ID), domain.PriceTaskFailed, domain.PriceStatusFailed, finalReason)
		metrics.RepricerUploadsTotal.WithLabelValues("poll_timeout").Inc()
		s.notifyPriceResult(ctx, workspaceID, task, "failed")
		return true
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if _, err := s.queries.UpdatePriceUploadTask(ctx, sqlcgen.UpdatePriceUploadTaskParams{
		ID:        task.ID,
		Status:    domain.PriceTaskProcessing,
		PollCount: next,
		Error:     pgtype.Text{String: reason, Valid: reason != ""},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to bump price task poll count")
	}
	return false
}

func (s *RepricerService) notifyPriceResult(ctx context.Context, workspaceID uuid.UUID, task sqlcgen.PriceUploadTask, outcome string) {
	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int64("wb_task_id", task.WbTaskID).
		Int("items", int(task.ItemsCount)).
		Str("outcome", outcome).
		Msg("price upload finished")
	if s.notifications != nil {
		s.notifications.NotifyPriceUploadResult(ctx, workspaceID, int(task.ItemsCount), outcome)
	}
}

// Rollback reverts an applied price change by uploading the original price/discount.
func (s *RepricerService) Rollback(ctx context.Context, actorID, workspaceID, changeID uuid.UUID) (*domain.PriceChange, error) {
	row, err := s.queries.GetPriceChange(ctx, uuidToPgtype(changeID))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperror.New(apperror.ErrNotFound, "price change not found")
		}
		return nil, err
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "price change not found")
	}
	if row.WbStatus != domain.PriceStatusApplied || !row.CanRollback {
		return nil, apperror.New(apperror.ErrValidation, "price change is not rollback-able")
	}
	current, err := s.queries.GetProductPriceForRollback(ctx, row.WorkspaceID, row.SellerCabinetID, row.WbProductID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrValidation, "current product price is unavailable; sync prices before rollback")
		}
		return nil, err
	}
	if !rollbackBaselineMatches(current, row) {
		return nil, apperror.New(apperror.ErrValidation, "current product price differs from the applied change; rollback would overwrite a newer external price")
	}
	if newer, err := s.queries.HasNewerActivePriceChange(ctx, row.WorkspaceID, row.WbProductID, row.ID); err != nil {
		return nil, err
	} else if newer {
		return nil, apperror.New(apperror.ErrValidation, "a newer price change exists for this product")
	}
	if child, err := s.queries.HasRollbackChild(ctx, uuidToPgtype(changeID)); err == nil && child {
		return nil, apperror.New(apperror.ErrValidation, "price change already rolled back")
	}
	if inflight, err := s.queries.HasInFlightPriceChange(ctx, row.WorkspaceID, row.WbProductID); err == nil && inflight {
		return nil, apperror.New(apperror.ErrValidation, "product has an in-flight price change")
	}

	origChangeID := changeID
	intent := priceChangeIntent{
		CabinetID:   uuidFromPgtype(row.SellerCabinetID),
		NmID:        row.WbProductID,
		OldPriceRub: row.NewPriceRub,
		NewPriceRub: row.OldPriceRub,
		OldDiscount: int(row.NewDiscountPercent),
		NewDiscount: int(row.OldDiscountPercent),
		Reason:      "rollback of price change " + changeID.String(),
		Source:      domain.PriceSourceRollback,
		RollbackOf:  &origChangeID,
		CreatedBy:   &actorID,
	}
	// Upload the reverting change and mark the original rolled back.
	taskIDs, err := s.applyIntents(ctx, workspaceID, []priceChangeIntent{intent})
	if err != nil {
		return nil, err
	}
	if len(taskIDs) == 0 {
		return nil, apperror.New(apperror.ErrConflict, "rollback was not accepted by WB")
	}
	if _, err := s.queries.UpdatePriceChangeStatus(ctx, sqlcgen.UpdatePriceChangeStatusParams{
		ID:       uuidToPgtype(changeID),
		WbStatus: domain.PriceStatusRolledBack,
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to mark original change rolled back")
	}
	out := priceChangeFromSqlc(row)
	return &out, nil
}

func rollbackBaselineMatches(current sqlcgen.ProductPrice, change sqlcgen.PriceChange) bool {
	return current.PriceRub == change.NewPriceRub && current.DiscountPercent == change.NewDiscountPercent
}

func priceChangeFromSqlc(row sqlcgen.PriceChange) domain.PriceChange {
	pc := domain.PriceChange{
		ID:                 uuidFromPgtype(row.ID),
		WorkspaceID:        uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID:    uuidFromPgtype(row.SellerCabinetID),
		WBProductID:        row.WbProductID,
		OldPriceRub:        row.OldPriceRub,
		NewPriceRub:        row.NewPriceRub,
		OldDiscountPercent: int(row.OldDiscountPercent),
		NewDiscountPercent: int(row.NewDiscountPercent),
		Reason:             row.Reason,
		Source:             row.Source,
		WBStatus:           row.WbStatus,
		CanRollback:        row.CanRollback,
	}
	if row.MinPriceRub.Valid {
		v := row.MinPriceRub.Int64
		pc.MinPriceRub = &v
	}
	if row.Error.Valid {
		pc.Error = &row.Error.String
	}
	if row.CreatedAt.Valid {
		pc.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		pc.UpdatedAt = row.UpdatedAt.Time
	}
	return pc
}
