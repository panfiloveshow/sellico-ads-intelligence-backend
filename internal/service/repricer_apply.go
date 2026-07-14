package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	pricePollAlertAttempt   = 48 // ~4h at the 5-minute sweep cadence
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

type applyIntentsResult struct {
	Accepted  int
	Queued    int
	Failed    int
	ChangeIDs []uuid.UUID
	TaskIDs   []uuid.UUID
}

func (s *RepricerService) applyIntents(ctx context.Context, workspaceID uuid.UUID, intents []priceChangeIntent) ([]uuid.UUID, error) {
	result, err := s.enqueueAndApplyIntents(ctx, workspaceID, intents)
	return result.TaskIDs, err
}

// enqueueAndApplyIntents persists every latest intent before attempting WB.
// A concurrent worker can only submit rows returned by the atomic DB claim.
func (s *RepricerService) enqueueAndApplyIntents(ctx context.Context, workspaceID uuid.UUID, intents []priceChangeIntent) (applyIntentsResult, error) {
	result := applyIntentsResult{}
	changeIDs := make([]uuid.UUID, 0, len(intents))
	seenIDs := make(map[uuid.UUID]struct{}, len(intents))
	var applyErrs []error
	for _, it := range intents {
		changeID, err := s.enqueuePendingChange(ctx, workspaceID, it)
		if err != nil {
			applyErrs = append(applyErrs, fmt.Errorf("enqueue nmID %d: %w", it.NmID, err))
			continue
		}
		result.Accepted++
		result.ChangeIDs = append(result.ChangeIDs, changeID)
		if _, exists := seenIDs[changeID]; !exists {
			seenIDs[changeID] = struct{}{}
			changeIDs = append(changeIDs, changeID)
		}
	}
	if result.Accepted == 0 {
		return result, errors.Join(applyErrs...)
	}
	if _, err := s.queries.AggregatePriceSchedulesByWorkspace(ctx, uuidToPgtype(workspaceID)); err != nil {
		applyErrs = append(applyErrs, fmt.Errorf("aggregate superseded price schedules: %w", err))
	}

	_, drainErr := s.drainPendingPriceChanges(ctx, workspaceID)
	if drainErr != nil {
		applyErrs = append(applyErrs, drainErr)
	}
	linkedTaskIDs, err := s.queries.ListPriceUploadTaskIDsForChanges(ctx, uuidToPgtype(workspaceID), uuidsToPgtype(changeIDs))
	if err != nil {
		applyErrs = append(applyErrs, fmt.Errorf("list accepted price tasks: %w", err))
	} else {
		for _, taskID := range linkedTaskIDs {
			result.TaskIDs = append(result.TaskIDs, uuidFromPgtype(taskID))
		}
	}
	if len(changeIDs) > 0 {
		queued, err := s.queries.CountPendingPriceChanges(ctx, uuidToPgtype(workspaceID), uuidsToPgtype(changeIDs))
		if err != nil {
			applyErrs = append(applyErrs, fmt.Errorf("count queued price changes: %w", err))
		} else {
			result.Queued = int(queued)
		}
		failed, err := s.queries.CountFailedPriceChanges(ctx, uuidToPgtype(workspaceID), uuidsToPgtype(changeIDs))
		if err != nil {
			applyErrs = append(applyErrs, fmt.Errorf("count failed price changes: %w", err))
		} else {
			result.Failed = int(failed)
		}
	}
	return result, errors.Join(applyErrs...)
}

func (s *RepricerService) drainPendingPriceChanges(ctx context.Context, workspaceID uuid.UUID) ([]uuid.UUID, error) {
	cabinetIDs, err := s.queries.ListClaimablePriceChangeCabinets(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	var taskIDs []uuid.UUID
	var drainErrs []error
	for _, rawCabinetID := range cabinetIDs {
		cabinetID := uuidFromPgtype(rawCabinetID)
		ids, err := s.drainCabinetPriceChanges(ctx, workspaceID, cabinetID)
		taskIDs = append(taskIDs, ids...)
		if err != nil {
			drainErrs = append(drainErrs, fmt.Errorf("cabinet %s: %w", cabinetID, err))
		}
	}
	return taskIDs, errors.Join(drainErrs...)
}

func (s *RepricerService) drainCabinetPriceChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID) ([]uuid.UUID, error) {
	if s.pricesEndpointCoolingDown(ctx, cabinetID, wbEndpointPricesUpload) {
		metrics.RepricerUploadsTotal.WithLabelValues("queued_cooldown").Inc()
		return nil, nil
	}
	token, err := crypto.Decrypt(s.mustCabinetToken(ctx, cabinetID), s.encryptionKey)
	if err != nil {
		metrics.RepricerUploadsTotal.WithLabelValues("token_error").Inc()
		s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("price changes remain queued because cabinet token is unavailable")
		return nil, nil
	}
	var taskIDs []uuid.UUID
	for {
		uncertain, err := s.queries.ClaimUnknownPriceChanges(ctx, uuidToPgtype(workspaceID), uuidToPgtype(cabinetID))
		if err != nil {
			return taskIDs, err
		}
		if len(uncertain) > 0 {
			taskID, err := s.uploadClaimedChunk(ctx, workspaceID, cabinetID, token, uncertain)
			if taskID != uuid.Nil {
				taskIDs = append(taskIDs, taskID)
			}
			if err != nil {
				s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesUpload, err)
				return taskIDs, err
			}
			continue
		}
		claimed, err := s.queries.ClaimPendingPriceChanges(ctx, uuidToPgtype(workspaceID), uuidToPgtype(cabinetID), uuidToPgtype(uuid.New()), priceUploadChunkSize)
		if err != nil {
			return taskIDs, err
		}
		if len(claimed) == 0 {
			return taskIDs, nil
		}
		taskID, err := s.uploadClaimedChunk(ctx, workspaceID, cabinetID, token, claimed)
		if taskID != uuid.Nil {
			taskIDs = append(taskIDs, taskID)
		}
		if err != nil {
			s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesUpload, err)
			return taskIDs, err
		}
	}
}

func (s *RepricerService) uploadClaimedChunk(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, claimed []sqlcgen.PriceChange) (uuid.UUID, error) {
	// WB duplicate detection is payload-sensitive. Keep both the first submit
	// and an unknown-outcome retry byte-equivalent for the persisted batch.
	sort.Slice(claimed, func(i, j int) bool { return claimed[i].WbProductID < claimed[j].WbProductID })
	items := make([]wb.PriceUpdateItem, 0, len(claimed))
	changeIDs := make([]uuid.UUID, 0, len(claimed))
	for _, change := range claimed {
		changeIDs = append(changeIDs, uuidFromPgtype(change.ID))
		items = append(items, wb.PriceUpdateItem{NmID: change.WbProductID, Price: change.NewPriceRub, Discount: int(change.NewDiscountPercent)})
	}
	wbTaskID, duplicate, err := s.wbClient.UploadPriceTask(ctx, token, items)
	if err != nil {
		metrics.RepricerUploadsTotal.WithLabelValues("submit_failed").Inc()
		if wb.IsPriceUploadOutcomeUnknown(err) {
			_, stateErr := s.queries.MarkSubmittingPriceChangesUnknown(ctx, uuidToPgtype(workspaceID), uuidsToPgtype(changeIDs), truncatedText(err.Error()))
			return uuid.Nil, errors.Join(err, stateErr)
		}
		_, stateErr := s.queries.FailSubmittingPriceChanges(ctx, uuidToPgtype(workspaceID), uuidsToPgtype(changeIDs), truncatedText(err.Error()))
		return uuid.Nil, errors.Join(err, stateErr)
	}

	task, linked, err := s.queries.CreatePriceUploadTaskAndLinkChanges(ctx, sqlcgen.CreatePriceUploadTaskParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		WbTaskID:        wbTaskID,
		Status:          domain.PriceTaskUploaded,
		ItemsCount:      int32(len(items)),
	}, uuidsToPgtype(changeIDs))
	if err != nil {
		metrics.RepricerUploadsTotal.WithLabelValues("link_failed").Inc()
		return uuid.Nil, fmt.Errorf("link accepted WB task %d: %w", wbTaskID, err)
	}
	if linked != int64(len(changeIDs)) {
		return uuid.Nil, fmt.Errorf("link accepted WB task %d: claimed %d changes, linked %d", wbTaskID, len(changeIDs), linked)
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
			s.aggregateSchedulesForTask(ctx, workspaceID, taskUUID)
			return taskUUID, errors.New("duplicate WB price task already failed")
		case domain.PriceTaskPartial:
			if _, err := s.resolvePartial(ctx, workspaceID, token, task); err != nil {
				return taskUUID, fmt.Errorf("reconcile duplicate partial task: %w", err)
			}
			s.aggregateSchedulesForTask(ctx, workspaceID, taskUUID)
			return taskUUID, errors.New("duplicate WB price task completed partially")
		}
	}
	s.aggregateSchedulesForTask(ctx, workspaceID, taskUUID)
	return taskUUID, nil
}

func (s *RepricerService) enqueuePendingChange(ctx context.Context, workspaceID uuid.UUID, it priceChangeIntent) (uuid.UUID, error) {
	var ctxJSON []byte
	if it.DecisionContext != nil {
		ctxJSON, _ = json.Marshal(it.DecisionContext)
	}
	row, err := s.queries.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{
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

func truncatedText(reason string) pgtype.Text {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return pgtype.Text{String: reason, Valid: reason != ""}
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
	if submitting, schedules, recoverErr := s.queries.RecoverStaleRepricerState(ctx, uuidToPgtype(workspaceID), staleBefore); recoverErr != nil {
		s.logger.Warn().Err(recoverErr).Msg("failed to recover stale repricer state")
	} else if submitting > 0 || schedules > 0 {
		s.logger.Warn().Int64("submitting_recovered", submitting).Int64("schedules_released", schedules).Msg("recovered stale repricer state")
		if submitting > 0 {
			metrics.RepricerStateRecoveriesTotal.WithLabelValues("submitting_change").Add(float64(submitting))
		}
		if schedules > 0 {
			metrics.RepricerStateRecoveriesTotal.WithLabelValues("executing_schedule").Add(float64(schedules))
		}
	}
	if _, err := s.queries.AggregatePriceSchedulesByWorkspace(ctx, uuidToPgtype(workspaceID)); err != nil {
		s.logger.Warn().Err(err).Msg("failed to aggregate recovered price schedules")
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
	_, drainErr := s.drainPendingPriceChanges(ctx, workspaceID)
	return terminal, drainErr
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

	if terminal, ok := terminalPriceTaskResult(status.Status); ok {
		transitioned := s.completeTask(ctx, workspaceID, taskUUID, terminal.taskStatus, terminal.changeStatus, terminal.reason)
		if transitioned {
			metrics.RepricerUploadsTotal.WithLabelValues(terminal.metric).Inc()
			if terminal.outcome != "" {
				s.notifyPriceResult(ctx, workspaceID, task, terminal.outcome)
			}
		}
		return transitioned
	}

	switch status.Status {
	case 5: // partial — resolve per product
		transitioned, err := s.resolvePartial(ctx, workspaceID, token, task)
		if err != nil {
			s.logger.Warn().Err(err).Int64("wb_task_id", task.WbTaskID).Msg("failed to resolve partial price task")
			return s.bumpPoll(ctx, workspaceID, task, "resolve partial: "+err.Error())
		}
		s.aggregateSchedulesForTask(ctx, workspaceID, taskUUID)
		if transitioned {
			metrics.RepricerUploadsTotal.WithLabelValues("partial").Inc()
			s.notifyPriceResult(ctx, workspaceID, task, "partial")
		}
		return transitioned
	default:
		return s.bumpPoll(ctx, workspaceID, task, fmt.Sprintf("non-terminal WB status %d", status.Status))
	}
}

type priceTaskTerminalResult struct {
	taskStatus   string
	changeStatus string
	reason       string
	metric       string
	outcome      string
}

func terminalPriceTaskResult(status int) (priceTaskTerminalResult, bool) {
	switch status {
	case 3:
		return priceTaskTerminalResult{taskStatus: domain.PriceTaskApplied, changeStatus: domain.PriceStatusApplied, metric: "applied"}, true
	case 4:
		// WB documents status 4 as terminal cancellation, so it must not age into poll_timeout.
		return priceTaskTerminalResult{taskStatus: domain.PriceTaskFailed, changeStatus: domain.PriceStatusFailed, reason: "canceled_by_wb", metric: "canceled", outcome: "failed"}, true
	case 6:
		return priceTaskTerminalResult{taskStatus: domain.PriceTaskFailed, changeStatus: domain.PriceStatusFailed, reason: "all products failed", metric: "failed", outcome: "failed"}, true
	default:
		return priceTaskTerminalResult{}, false
	}
}

func (s *RepricerService) resolvePartial(ctx context.Context, workspaceID uuid.UUID, token string, task sqlcgen.PriceUploadTask) (bool, error) {
	goods, err := s.wbClient.ListPriceTaskHistoryGoods(ctx, token, task.WbTaskID, priceUploadChunkSize, 0)
	if err != nil {
		return false, err
	}
	if len(goods) == 0 {
		return false, errors.New("partial task returned no per-good results")
	}
	failedNmIDs := make([]int64, 0)
	failureReasons := make([]string, 0)
	for _, g := range goods {
		reason, failed := partialPriceGoodFailure(g)
		if !failed {
			continue
		}
		failedNmIDs = append(failedNmIDs, g.NmID)
		failureReasons = append(failureReasons, reason)
	}
	return s.queries.FinalizePartialPriceUploadTask(ctx, task.ID, uuidToPgtype(workspaceID), failedNmIDs, failureReasons)
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

func (s *RepricerService) completeTask(ctx context.Context, workspaceID, taskUUID uuid.UUID, taskStatus, changeStatus, errText string) bool {
	transitioned, err := s.queries.FinalizePriceUploadTask(ctx, uuidToPgtype(taskUUID), uuidToPgtype(workspaceID), taskStatus, changeStatus, truncatedText(errText))
	if err != nil {
		s.logger.Warn().Err(err).Str("task_id", taskUUID.String()).Msg("failed to finalize price upload task")
		return false
	}
	s.aggregateSchedulesForTask(ctx, workspaceID, taskUUID)
	return transitioned
}

func (s *RepricerService) aggregateSchedulesForTask(ctx context.Context, workspaceID, taskUUID uuid.UUID) {
	updated, err := s.queries.AggregatePriceSchedulesByTaskID(ctx, uuidToPgtype(workspaceID), uuidToPgtype(taskUUID))
	if err != nil {
		s.logger.Warn().Err(err).Str("task_id", taskUUID.String()).Msg("failed to aggregate price schedules for WB task")
		return
	}
	if updated > 0 {
		s.logger.Info().Str("task_id", taskUUID.String()).Int64("schedules", updated).Msg("price schedules aggregated from WB task result")
	}
}

func (s *RepricerService) bumpPoll(ctx context.Context, workspaceID uuid.UUID, task sqlcgen.PriceUploadTask, reason string) bool {
	next := task.PollCount + 1
	if int(next) == pricePollAlertAttempt {
		metrics.RepricerUploadsTotal.WithLabelValues("poll_delayed").Inc()
		s.logger.Error().Str("task_id", uuidFromPgtype(task.ID).String()).Int64("wb_task_id", task.WbTaskID).Msg("WB price task has no terminal result after the expected polling window")
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if _, err := s.queries.BumpPriceUploadTaskPoll(ctx, task.ID, uuidToPgtype(workspaceID), task.PollCount, truncatedText(reason)); err != nil {
		s.logger.Warn().Err(err).Msg("failed to bump price task poll count")
	}
	// An absent/failed poll is not evidence that WB rejected the write. Keep the
	// task and its product lock active so a late WB apply cannot overwrite a
	// newer queued request.
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
	result, err := s.enqueueAndApplyIntents(ctx, workspaceID, []priceChangeIntent{intent})
	if err != nil && result.Accepted == 0 {
		return nil, err
	}
	if result.Accepted != 1 || len(result.ChangeIDs) != 1 {
		return nil, apperror.New(apperror.ErrConflict, "rollback request was not saved")
	}
	if result.Failed > 0 {
		if err != nil {
			return nil, err
		}
		return nil, apperror.New(apperror.ErrConflict, "rollback was rejected before delivery to WB")
	}
	if err != nil {
		s.logger.Warn().Err(err).Str("rollback_change_id", result.ChangeIDs[0].String()).Msg("rollback saved and will be retried")
	}
	child, err := s.queries.GetPriceChange(ctx, uuidToPgtype(result.ChangeIDs[0]))
	if err != nil {
		return nil, err
	}
	out := priceChangeFromSqlc(child)
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
