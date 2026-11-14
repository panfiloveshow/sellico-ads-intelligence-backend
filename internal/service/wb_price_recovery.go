package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func (s *RepricerService) beginWBPriceDispatch(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, claimed []sqlcgen.PriceChange) error {
	if len(claimed) == 0 || len(claimed) > priceUploadChunkSize {
		return errors.New("unbounded WB price claim")
	}
	batch := uuidFromPgtype(claimed[0].SubmissionBatchID)
	if batch == uuid.Nil {
		return errors.New("WB price claim has no batch")
	}
	ids := make([]int64, 0, len(claimed))
	localIDs := make([]uuid.UUID, 0, len(claimed))
	for _, row := range claimed {
		if uuidFromPgtype(row.WorkspaceID) != workspaceID || uuidFromPgtype(row.SellerCabinetID) != cabinetID || uuidFromPgtype(row.SubmissionBatchID) != batch {
			return errors.New("WB price claim scope mismatch")
		}
		ids = append(ids, row.WbProductID)
		localIDs = append(localIDs, uuidFromPgtype(row.ID))
	}
	for _, row := range claimed {
		if row.NewPriceRub <= 0 || row.NewPriceRub > 9_999_999_999 || row.NewDiscountPercent < 0 || row.NewDiscountPercent > 99 {
			return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, errors.New("invalid WB price command"))
		}
	}
	snapshots, err := s.wbClient.ReadPriceSnapshots(ctx, token, ids)
	if err != nil {
		return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, fmt.Errorf("WB price source could not be verified: %w", err))
	}
	evidence := make([]sqlcgen.WBPriceDispatchEvidence, 0, len(claimed))
	for _, row := range claimed {
		snapshot, ok := snapshots[row.WbProductID]
		baseline := row
		baseline.NewPriceRub, baseline.NewDiscountPercent = row.OldPriceRub, row.OldDiscountPercent
		if !ok || row.OldPriceRub <= 0 || row.OldPriceRub > 9_999_999_999 || row.OldDiscountPercent < 0 || row.OldDiscountPercent > 100 || !wbPriceCommandObserved(snapshot, baseline) {
			return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, fmt.Errorf("WB source price changed or is incomplete for article %d; prepare a new command", row.WbProductID))
		}
		if err := s.validateManagerWBDispatch(ctx, row, snapshot, token); err != nil {
			return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, err)
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		evidence = append(evidence, sqlcgen.WBPriceDispatchEvidence{ChangeID: uuidFromPgtype(row.ID).String(), NmID: row.WbProductID, OldPriceRub: row.OldPriceRub, OldDiscount: row.OldDiscountPercent, NewPriceRub: row.NewPriceRub, NewDiscount: row.NewDiscountPercent, Snapshot: raw})
	}
	if err := ctx.Err(); err != nil {
		return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, err)
	}
	account, err := adguard.WBAccountKey(token)
	if err != nil {
		return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, errors.New("WB seller identity could not be verified before price dispatch"))
	}
	changed, err := s.queries.BeginWBPriceDispatch(ctx, uuidToPgtype(workspaceID), uuidToPgtype(cabinetID), uuidToPgtype(batch), evidence, account)
	if err != nil {
		return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, fmt.Errorf("persist WB price dispatch marker: %w", err))
	}
	if !changed {
		return s.failUnsentWBPriceClaim(ctx, workspaceID, batch, localIDs, errors.New("WB price claim expired or already entered transport; resend blocked"))
	}
	return nil
}

func (s *RepricerService) failUnsentWBPriceClaim(ctx context.Context, workspaceID, batchID uuid.UUID, ids []uuid.UUID, cause error) error {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	reason := cause.Error()
	if len(reason) > 500 {
		reason = reason[:500]
	}
	err := s.queries.FailUnsentWBPriceChanges(persist, uuidToPgtype(workspaceID), uuidToPgtype(batchID), uuidsToPgtype(ids), reason)
	return errors.Join(cause, err)
}

// Native task processing is a receipt. Successful products also need a fresh
// exact observation of every size before releasing the physical article hold.
func (s *RepricerService) reconcileWBPriceTask(ctx context.Context, workspaceID uuid.UUID, token string, task sqlcgen.PriceUploadTask, nativeStatus int) (bool, error) {
	changes, err := s.queries.ListWBPriceTaskChanges(ctx, task.ID, uuidToPgtype(workspaceID))
	if err != nil {
		return false, err
	}
	known := make(map[int64]bool)
	var active []sqlcgen.PriceChange
	for _, row := range changes {
		known[row.WbProductID] = true
		if row.WbStatus == domain.PriceStatusUploaded || row.WbStatus == domain.PriceStatusSubmitting {
			active = append(active, row)
		}
	}
	if len(active) == 0 || len(active) > priceUploadChunkSize {
		return false, errors.New("WB task has no bounded active command set")
	}
	perGood := make(map[int64]wb.PriceTaskGood)
	if nativeStatus == 5 {
		goods, err := s.wbClient.ListPriceTaskHistoryGoods(ctx, token, task.WbTaskID, priceUploadChunkSize, 0)
		if err != nil {
			return false, err
		}
		for _, good := range goods {
			if !known[good.NmID] {
				return false, errors.New("WB task goods contain an unrelated article")
			}
			if _, exists := perGood[good.NmID]; exists {
				return false, errors.New("WB task goods repeat an article")
			}
			perGood[good.NmID] = good
		}
	}
	ids := make([]int64, 0, len(active))
	verdicts := make(map[int64]string)
	reasons := make(map[int64]string)
	terminal, allFailed := terminalPriceTaskResult(nativeStatus)
	for _, row := range active {
		if row.NewPriceRub <= 0 || row.NewPriceRub > 9_999_999_999 || row.NewDiscountPercent < 0 || row.NewDiscountPercent > 100 {
			return false, errors.New("unverifiable WB price command")
		}
		verdicts[row.WbProductID] = domain.PriceStatusApplied
		if allFailed {
			verdicts[row.WbProductID] = domain.PriceStatusFailed
			reasons[row.WbProductID] = terminal.reason
			continue
		}
		if nativeStatus == 5 {
			good, ok := perGood[row.WbProductID]
			if !ok || good.Price != row.NewPriceRub || good.Discount != int(row.NewDiscountPercent) {
				return false, errors.New("WB has not confirmed the exact command in partial task results")
			}
			switch good.Status {
			case 2:
				if good.ErrorText != "" {
					return false, errors.New("contradictory WB item success receipt")
				}
			case 3:
				verdicts[row.WbProductID] = domain.PriceStatusFailed
				reasons[row.WbProductID], _ = partialPriceGoodFailure(good)
				continue
			default:
				return false, errors.New("unknown WB item status")
			}
		} else if nativeStatus != 3 {
			return false, errors.New("WB task is not processed")
		}
		ids = append(ids, row.WbProductID)
	}
	observed := map[int64]wb.PriceSnapshot{}
	if len(ids) > 0 {
		observed, err = s.wbClient.ReadPriceSnapshots(ctx, token, ids)
		if err != nil {
			return false, err
		}
	}
	evidence := make([]sqlcgen.WBPriceVerification, 0, len(active))
	for _, row := range active {
		proof := map[string]any{"native_task_id": task.WbTaskID, "native_task_status": nativeStatus, "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}
		if nativeStatus == 5 {
			proof["item_receipt"] = perGood[row.WbProductID]
		}
		if verdicts[row.WbProductID] == domain.PriceStatusApplied {
			snapshot, exists := observed[row.WbProductID]
			if !exists || !wbPriceCommandObserved(snapshot, row) {
				return false, fmt.Errorf("WB price readback does not match article %d", row.WbProductID)
			}
			approval, err := managerApprovalFromContext(row.DecisionContext)
			if err != nil || (approval != nil && (!managerWBObserved(*approval, snapshot, true) || approval.ExecutorCredentialsDigest != ManagerPriceCredentialsDigest("wb", "", token) || !managerWBRowMatches(*approval, row))) {
				return false, errors.New("WB manager readback conditions or size identity changed")
			}
			proof["snapshot"] = snapshot
		}
		raw, err := json.Marshal(proof)
		if err != nil {
			return false, err
		}
		evidence = append(evidence, sqlcgen.WBPriceVerification{ChangeID: uuidFromPgtype(row.ID).String(), NmID: row.WbProductID, PriceRub: row.NewPriceRub, Discount: row.NewDiscountPercent, Status: verdicts[row.WbProductID], Reason: reasons[row.WbProductID], Proof: raw})
	}
	status := domain.PriceTaskApplied
	if allFailed {
		status = domain.PriceTaskFailed
	} else if nativeStatus == 5 {
		status = domain.PriceTaskPartial
	}
	return s.queries.FinalizeObservedWBPriceTask(ctx, task, uuidToPgtype(workspaceID), status, evidence)
}

func wbPriceCommandObserved(snapshot wb.PriceSnapshot, row sqlcgen.PriceChange) bool {
	if snapshot.NmID != row.WbProductID || snapshot.DiscountPercent != int(row.NewDiscountPercent) || len(snapshot.Sizes) == 0 {
		return false
	}
	for _, size := range snapshot.Sizes {
		if size.PriceCents != row.NewPriceRub*100 || size.DiscountedPriceCents != row.NewPriceRub*(100-int64(row.NewDiscountPercent)) {
			return false
		}
	}
	return true
}
