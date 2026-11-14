package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// exactOzonPriceCents refuses silently rounded or non-finite API input. Legacy
// float fields remain compatible while the dispatch journal stores kopecks.
func exactOzonPriceCents(value float64, required bool) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("price must be finite")
	}
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	if !ok {
		return 0, errors.New("price must be decimal")
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, errors.New("price must have at most two decimal places")
	}
	cents := r.Num().Int64()
	if cents < 0 || cents > 999999999999 || (required && cents == 0) {
		return 0, errors.New("price outside supported range")
	}
	return cents, nil
}

func ozonPriceAccountKey(creds ozon.Credentials) (string, error) {
	clientID := strings.TrimSpace(creds.ClientID)
	if clientID == "" {
		return "", errors.New("missing Ozon seller identity")
	}
	sum := sha256.Sum256([]byte("ozon-seller-price:" + clientID))
	return hex.EncodeToString(sum[:]), nil
}

func ozonPriceResourceKey(accountKey string, productID int64) string {
	sum := sha256.Sum256([]byte(accountKey + ":" + strconv.FormatInt(productID, 10)))
	return hex.EncodeToString(sum[:])
}

func sameOzonPriceSnapshot(a, b ozon.PriceSnapshot) bool {
	sameOptional := func(x, y *int64) bool {
		return (x == nil && y == nil) || (x != nil && y != nil && *x == *y)
	}
	return a.ProductID == b.ProductID && a.OfferID == b.OfferID && a.PriceCents == b.PriceCents &&
		sameOptional(a.OldPriceCents, b.OldPriceCents) && sameOptional(a.MinPriceCents, b.MinPriceCents)
}

func (s *OzonRepricerService) abortOzonPriceClaims(ctx context.Context, ids []pgtype.UUID) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var failures []error
	for _, id := range ids {
		if err := s.queries.AbortUndispatchedOzonPrice(cleanupCtx, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

type ozonDispatchCandidate struct {
	row    sqlcgen.OzonPriceChange
	target int64
	update ozon.PriceUpdate
}

func (s *OzonRepricerService) dispatchPriceIntents(ctx context.Context, cabinetID uuid.UUID, creds ozon.Credentials, intents []ozonPriceIntent) error {
	accountKey, err := ozonPriceAccountKey(creds)
	if err != nil {
		return err
	}
	if err := s.queries.RecoverOzonPriceClaims(ctx, uuidToPgtype(cabinetID)); err != nil {
		return fmt.Errorf("recover Ozon price claims: %w", err)
	}
	var dispatchErrors []error
	for start := 0; start < len(intents); start += ozonManualPriceItemsLimit {
		end := min(start+ozonManualPriceItemsLimit, len(intents))
		var candidates []ozonDispatchCandidate
		var ids []int64
		seen := map[int64]bool{}
		for _, intent := range intents[start:end] {
			row, loadErr := s.queries.GetOzonPriceChangeByID(ctx, intent.ChangeID)
			if loadErr != nil {
				dispatchErrors = append(dispatchErrors, fmt.Errorf("load pending Ozon price change: %w", loadErr))
				continue
			}
			if row.Status != domain.OzonPriceStatusPending {
				continue // no state, including submit_unknown, is ever replayed
			}
			target, targetErr := exactOzonPriceCents(pgNumericToFloat(row.NewPriceRub), true)
			if row.SellerCabinetID != uuidToPgtype(cabinetID) || row.Sku != intent.SKU || targetErr != nil || seen[row.Sku] {
				dispatchErrors = append(dispatchErrors, errors.New("invalid or duplicate Ozon price dispatch scope"))
				continue
			}
			seen[row.Sku] = true
			// The persisted row is authoritative. Transient callers cannot change
			// an already recorded target or optional crossed-out/minimum price.
			update := ozon.PriceUpdate{ProductID: row.Sku, PriceRub: float64(target) / 100}
			if row.NewOldPriceRub.Valid {
				v := pgNumericToFloat(row.NewOldPriceRub)
				update.OldPriceRub = &v
			}
			if row.MinPriceRub.Valid {
				v := pgNumericToFloat(row.MinPriceRub)
				update.MinPriceRub = &v
			}
			candidates = append(candidates, ozonDispatchCandidate{row: row, target: target, update: update})
			ids = append(ids, row.Sku)
		}
		if len(candidates) == 0 {
			continue
		}
		readCtx, cancelRead := context.WithTimeout(ctx, 30*time.Second)
		current, readErr := s.sellerClient.ReadPriceSnapshots(readCtx, creds, ids)
		cancelRead()
		if readErr != nil {
			for _, item := range candidates {
				if err := s.queries.FailUnclaimedOzonPrice(ctx, item.row.ID, "current Ozon price unavailable; command not sent"); err != nil {
					dispatchErrors = append(dispatchErrors, err)
				}
			}
			dispatchErrors = append(dispatchErrors, errors.New("current Ozon prices could not be verified; no command sent"))
			continue
		}
		var updates []ozon.PriceUpdate
		var claimed []pgtype.UUID
		byProduct := map[int64]pgtype.UUID{}
		for _, item := range candidates {
			observed, ok := current[item.row.Sku]
			previous, previousErr := exactOzonPriceCents(pgNumericToFloat(item.row.OldPriceRub), true)
			message := "current Ozon price differs from the recorded baseline; command not sent"
			if !ok || observed.ProductID != item.row.Sku || previousErr != nil || !item.row.OldPriceRub.Valid || previous != observed.PriceCents ||
				(item.row.OfferID.Valid && item.row.OfferID.String != "" && item.row.OfferID.String != observed.OfferID) {
				if err := s.queries.FailUnclaimedOzonPrice(ctx, item.row.ID, message); err != nil {
					dispatchErrors = append(dispatchErrors, err)
				}
				continue
			}
			if item.row.NewOldPriceRub.Valid && item.row.OldOldPriceRub.Valid {
				old, oldErr := exactOzonPriceCents(pgNumericToFloat(item.row.OldOldPriceRub), false)
				if oldErr != nil || observed.OldPriceCents == nil || old != *observed.OldPriceCents {
					if err := s.queries.FailUnclaimedOzonPrice(ctx, item.row.ID, message); err != nil {
						dispatchErrors = append(dispatchErrors, err)
					}
					continue
				}
			}
			if err := s.validateManagerOzonDispatch(ctx, item.row, observed, creds); err != nil {
				if failErr := s.queries.FailUnclaimedOzonPrice(ctx, item.row.ID, "manager approval or exact source changed; command not sent"); failErr != nil {
					dispatchErrors = append(dispatchErrors, failErr)
				}
				dispatchErrors = append(dispatchErrors, err)
				continue
			}
			baseline, _ := json.Marshal(observed)
			budget := int64(ozonHardHourlyWriteCap)
			if item.row.Source == domain.PriceSourceStrategy {
				budget = ozonStrategyHourlyWriteCap
			}
			claimErr := s.queries.ClaimOzonPriceWrite(ctx, item.row.ID, uuidToPgtype(cabinetID), item.row.Sku,
				accountKey, ozonPriceResourceKey(accountKey, item.row.Sku), observed.PriceCents, item.target, baseline, budget)
			if claimErr != nil {
				message = "price command could not acquire its product or write budget; command not sent"
				if errors.Is(claimErr, sqlcgen.ErrOzonPriceHeld) {
					message = "another price command is unresolved for this Ozon product; command not sent"
				}
				if err := s.queries.FailUnclaimedOzonPrice(ctx, item.row.ID, message); err != nil {
					dispatchErrors = append(dispatchErrors, err)
				}
				if !errors.Is(claimErr, sqlcgen.ErrOzonPriceHeld) && !errors.Is(claimErr, sqlcgen.ErrOzonPriceChanged) && !errors.Is(claimErr, sqlcgen.ErrOzonPriceBudget) {
					dispatchErrors = append(dispatchErrors, claimErr)
				}
				continue
			}
			claimed = append(claimed, item.row.ID)
			updates = append(updates, item.update)
			byProduct[item.row.Sku] = item.row.ID
		}
		if len(claimed) == 0 {
			continue
		}
		// A writer can finish while we acquire the product hold. Read again
		// under the durable hold, immediately before committing dispatch.
		claimedProducts := make([]int64, 0, len(byProduct))
		for productID := range byProduct {
			claimedProducts = append(claimedProducts, productID)
		}
		guardCtx, cancelGuardRead := context.WithTimeout(ctx, 30*time.Second)
		guarded, guardErr := s.sellerClient.ReadPriceSnapshots(guardCtx, creds, claimedProducts)
		cancelGuardRead()
		unchanged := guardErr == nil
		for _, productID := range claimedProducts {
			fresh, exists := guarded[productID]
			unchanged = unchanged && exists && sameOzonPriceSnapshot(current[productID], fresh)
			if exists {
				row, loadErr := s.queries.GetOzonPriceChangeByID(ctx, byProduct[productID])
				if loadErr != nil || s.validateManagerOzonDispatch(ctx, row, fresh, creds) != nil {
					unchanged = false
				}
			}
		}
		if !unchanged {
			if err := s.abortOzonPriceClaims(ctx, claimed); err != nil {
				dispatchErrors = append(dispatchErrors, err)
			}
			dispatchErrors = append(dispatchErrors, errors.New("Ozon price source changed or became unavailable while acquiring its hold; no command sent"))
			continue
		}
		if err := s.queries.MarkOzonPricesDispatched(ctx, claimed); err != nil {
			if cleanupErr := s.abortOzonPriceClaims(ctx, claimed); cleanupErr != nil {
				dispatchErrors = append(dispatchErrors, cleanupErr)
			}
			dispatchErrors = append(dispatchErrors, err)
			continue
		}
		results, writeErr := s.sellerClient.UpdatePrices(ctx, creds, updates)
		outcomes := map[int64]ozon.PriceUpdateResult{}
		validScope := true
		for _, result := range results {
			if _, wanted := byProduct[result.ProductID]; !wanted {
				validScope = false
			}
			if _, duplicate := outcomes[result.ProductID]; duplicate {
				validScope = false
			}
			if result.OfferID != "" && result.OfferID != current[result.ProductID].OfferID {
				validScope = false
			}
			outcomes[result.ProductID] = result
		}
		// A disconnected browser must not prevent recording a received receipt.
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		uncertain := writeErr != nil || !validScope
		for productID, id := range byProduct {
			status, message := domain.OzonPriceStatusSubmitUnknown, "submission receipt unavailable; read-only reconciliation required"
			if result, ok := outcomes[productID]; ok && validScope {
				if result.Updated && len(result.Errors) == 0 {
					status, message = domain.OzonPriceStatusVerifying, "awaiting exact price observation"
				} else if !result.Updated && len(result.Errors) > 0 {
					status, message = domain.OzonPriceStatusFailed, "Ozon explicitly rejected the price command"
				}
			}
			uncertain = uncertain || status == domain.OzonPriceStatusSubmitUnknown
			if err := s.queries.RecordOzonPriceReceipt(persistCtx, id, status, message); err != nil {
				dispatchErrors = append(dispatchErrors, err)
			}
		}
		cancelPersist()
		// Readback never resubmits. An observation without a known receipt
		// remains quarantined even when another writer set the same price.
		if err := s.reconcilePriceIDs(ctx, cabinetID, creds, claimed); err != nil {
			s.logger.Warn().Str("cabinet_id", cabinetID.String()).Msg("Ozon price readback unavailable; journal remains unresolved")
		}
		if uncertain {
			dispatchErrors = append(dispatchErrors, errors.New("Ozon price submission is uncertain; do not resend; reconcile the recorded change"))
		}
	}
	return errors.Join(dispatchErrors...)
}

func (s *OzonRepricerService) reconcilePriceIDs(ctx context.Context, cabinetID uuid.UUID, creds ozon.Credentials, ids []pgtype.UUID) error {
	accountKey, err := ozonPriceAccountKey(creds)
	if err != nil {
		return err
	}
	var leases []sqlcgen.OzonPriceWriteLease
	var products []int64
	for _, id := range ids {
		a, err := s.queries.GetOzonPriceLease(ctx, id)
		if err != nil {
			return err
		}
		if a.SellerCabinetID != uuidToPgtype(cabinetID) || a.VendorAccountKey != accountKey {
			return apperror.New(apperror.ErrValidation, "Ozon seller identity changed; price reconciliation blocked")
		}
		if a.ResourceKey.Valid && a.DispatchedAt.Valid {
			leases = append(leases, a)
			products = append(products, a.ProductID)
		}
	}
	if len(leases) == 0 {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	observed, err := s.sellerClient.ReadPriceSnapshots(readCtx, creds, products)
	if err != nil {
		return errors.New("Ozon price readback unavailable")
	}
	for _, a := range leases {
		p, ok := observed[a.ProductID]
		if !ok || p.ProductID != a.ProductID {
			continue // missing is unknown, never a zero price or a rejection
		}
		row, loadErr := s.queries.GetOzonPriceChangeByID(ctx, a.ChangeID)
		if loadErr != nil {
			return loadErr
		}
		approval, approvalErr := managerApprovalFromContext(row.DecisionContext)
		if approvalErr != nil || (approval != nil && (!managerOzonObserved(*approval, p, true) || approval.ExecutorCredentialsDigest != ManagerPriceCredentialsDigest("ozon", creds.ClientID, creds.APIKey) || !managerOzonRowMatches(*approval, row))) {
			continue // A matching amount alone does not prove the preserved context.
		}
		encoded, _ := json.Marshal(p)
		if err := s.queries.ObserveOzonPrice(ctx, a.ChangeID, sqlcgen.OzonPriceObservation{
			JSON: encoded, PriceCents: p.PriceCents, OldPriceCents: p.OldPriceCents, MinPriceCents: p.MinPriceCents,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *OzonRepricerService) reconcileCabinetPriceWrites(ctx context.Context, cabinetID uuid.UUID, creds ozon.Credentials) error {
	if err := s.queries.RecoverOzonPriceClaims(ctx, uuidToPgtype(cabinetID)); err != nil {
		return err
	}
	var after pgtype.UUID
	for {
		rows, err := s.queries.ListActiveOzonPriceLeases(ctx, uuidToPgtype(cabinetID), after, 200)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]pgtype.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ChangeID)
		}
		if err := s.reconcilePriceIDs(ctx, cabinetID, creds, ids); err != nil {
			return err
		}
		after = rows[len(rows)-1].ChangeID
	}
}

// ReconcilePriceChange is read-only at Ozon. It never resends a command and
// applies the same workspace ownership gate as the existing audit/rollback.
func (s *OzonRepricerService) ReconcilePriceChange(ctx context.Context, workspaceID, changeID uuid.UUID) (*domain.OzonPriceChange, error) {
	row, err := s.queries.GetOzonPriceChangeByID(ctx, uuidToPgtype(changeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "price change not found")
	}
	if err != nil {
		return nil, err
	}
	cabinet, creds, err := s.resolveCabinet(ctx, workspaceID, uuidFromPgtype(row.SellerCabinetID))
	if err != nil {
		return nil, err
	}
	lease, leaseErr := s.queries.GetOzonPriceLease(ctx, row.ID)
	if leaseErr != nil && !errors.Is(leaseErr, pgx.ErrNoRows) {
		return nil, leaseErr
	}
	if leaseErr == nil && lease.ResourceKey.Valid {
		if err := s.queries.RecoverOzonPriceClaims(ctx, row.SellerCabinetID); err != nil {
			return nil, err
		}
		if err := s.reconcilePriceIDs(ctx, cabinet.ID, creds, []pgtype.UUID{row.ID}); err != nil {
			return nil, err
		}
	}
	fresh, err := s.queries.GetOzonPriceChangeByID(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	change := ozonPriceChangeFromSqlc(fresh)
	return &change, nil
}
