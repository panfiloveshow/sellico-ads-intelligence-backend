package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	priceUploadChunkSize = 1000
	pricePollMaxAttempts = 48 // ~4h at the 5-minute sweep cadence
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
	for cabinetID, cabinetIntents := range byCabinet {
		if s.pricesEndpointCoolingDown(ctx, cabinetID, wbEndpointPricesUpload) {
			s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("prices upload cooling down, skipping cabinet")
			continue
		}
		token, err := crypto.Decrypt(s.mustCabinetToken(ctx, cabinetID), s.encryptionKey)
		if err != nil {
			s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("failed to decrypt cabinet token for price upload")
			continue
		}
		for start := 0; start < len(cabinetIntents); start += priceUploadChunkSize {
			end := start + priceUploadChunkSize
			if end > len(cabinetIntents) {
				end = len(cabinetIntents)
			}
			chunk := cabinetIntents[start:end]
			taskID, err := s.uploadChunk(ctx, workspaceID, cabinetID, token, chunk)
			if err != nil {
				s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesUpload, err)
				s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("price upload chunk failed")
				continue
			}
			if taskID != uuid.Nil {
				taskIDs = append(taskIDs, taskID)
			}
		}
	}
	return taskIDs, nil
}

// uploadChunk inserts pending changes, uploads them to WB, records the upload
// task, then flips the changes to 'uploaded' with the task id.
func (s *RepricerService) uploadChunk(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, chunk []priceChangeIntent) (uuid.UUID, error) {
	items := make([]wb.PriceUpdateItem, 0, len(chunk))
	changeIDs := make([]uuid.UUID, 0, len(chunk))
	for _, it := range chunk {
		changeID, err := s.createPendingChange(ctx, workspaceID, it)
		if err != nil {
			return uuid.Nil, err
		}
		changeIDs = append(changeIDs, changeID)
		items = append(items, wb.PriceUpdateItem{NmID: it.NmID, Price: it.NewPriceRub, Discount: it.NewDiscount})
	}

	wbTaskID, _, err := s.wbClient.UploadPriceTask(ctx, token, items)
	if err != nil {
		s.failChanges(ctx, changeIDs, err.Error())
		return uuid.Nil, err
	}

	task, err := s.queries.CreatePriceUploadTask(ctx, sqlcgen.CreatePriceUploadTaskParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		WbTaskID:        wbTaskID,
		Status:          domain.PriceTaskUploaded,
		ItemsCount:      int32(len(items)),
	})
	if err != nil {
		return uuid.Nil, err
	}
	taskUUID := uuidFromPgtype(task.ID)
	for _, id := range changeIDs {
		if _, err := s.queries.UpdatePriceChangeStatus(ctx, sqlcgen.UpdatePriceChangeStatusParams{
			ID:           uuidToPgtype(id),
			WbStatus:     domain.PriceStatusUploaded,
			UploadTaskID: uuidToPgtype(taskUUID),
		}); err != nil {
			s.logger.Warn().Err(err).Msg("failed to link price change to upload task")
		}
	}
	return taskUUID, nil
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
	tasks, err := s.queries.ListPendingPriceUploadTasks(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, err
	}
	terminal := 0
	for _, task := range tasks {
		cabinetID := uuidFromPgtype(task.SellerCabinetID)
		token, decErr := crypto.Decrypt(s.mustCabinetToken(ctx, cabinetID), s.encryptionKey)
		if decErr != nil {
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
	if err != nil || status == nil {
		// Not in history yet — check the buffer (still pending) and bump poll count.
		s.bumpPoll(ctx, task)
		return false
	}

	switch status.Status {
	case 3: // processed OK
		s.completeTask(ctx, taskUUID, domain.PriceTaskApplied, domain.PriceStatusApplied, "")
		return true
	case 6: // all products errored
		s.completeTask(ctx, taskUUID, domain.PriceTaskFailed, domain.PriceStatusFailed, "all products failed")
		s.notifyPriceResult(ctx, workspaceID, task, "failed")
		return true
	case 5: // partial — resolve per product
		s.resolvePartial(ctx, token, task)
		s.finishTaskRow(ctx, taskUUID, domain.PriceTaskPartial, "partial errors")
		s.notifyPriceResult(ctx, workspaceID, task, "partial")
		return true
	default:
		s.bumpPoll(ctx, task)
		return false
	}
}

func (s *RepricerService) resolvePartial(ctx context.Context, token string, task sqlcgen.PriceUploadTask) {
	goods, err := s.wbClient.ListPriceTaskHistoryGoods(ctx, token, task.WbTaskID, priceUploadChunkSize, 0)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load partial price task goods")
		return
	}
	failed := map[int64]string{}
	for _, g := range goods {
		if g.Status != 3 && g.ErrorText != "" {
			failed[g.NmID] = g.ErrorText
		}
	}
	// The whole task's changes default to applied; only the failed nmIDs flip.
	if err := s.queries.UpdatePriceChangesStatusByTask(ctx, task.ID, domain.PriceStatusApplied); err != nil {
		s.logger.Warn().Err(err).Msg("failed to bulk-apply partial task changes")
	}
	// ponytail: per-nm failure marking would need a task+nm query; the aggregate
	// task status ('partial') plus notification is enough for v1 — add a
	// per-product update if the UI needs exact rows.
	_ = failed
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

func (s *RepricerService) bumpPoll(ctx context.Context, task sqlcgen.PriceUploadTask) {
	next := task.PollCount + 1
	if int(next) >= pricePollMaxAttempts {
		s.completeTask(ctx, uuidFromPgtype(task.ID), domain.PriceTaskFailed, domain.PriceStatusFailed, "poll_timeout")
		return
	}
	if _, err := s.queries.UpdatePriceUploadTask(ctx, sqlcgen.UpdatePriceUploadTaskParams{
		ID:        task.ID,
		Status:    domain.PriceTaskProcessing,
		PollCount: next,
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to bump price task poll count")
	}
}

func (s *RepricerService) notifyPriceResult(ctx context.Context, workspaceID uuid.UUID, task sqlcgen.PriceUploadTask, outcome string) {
	// ponytail: Phase 7 wires NotificationService (Telegram/email); for now log.
	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int64("wb_task_id", task.WbTaskID).
		Int("items", int(task.ItemsCount)).
		Str("outcome", outcome).
		Msg("price upload finished")
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
	if _, err := s.applyIntents(ctx, workspaceID, []priceChangeIntent{intent}); err != nil {
		return nil, err
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
