package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ApprovedPriceCommand is accepted only from an authenticated approval adapter.
// The required validator rechecks the financial owner's current approval under
// the physical hold. No public HTTP route accepts this structure yet.
type ApprovedPriceCommand struct {
	Version                   string              `json:"version"`
	OperationID               uuid.UUID           `json:"operation_id"`
	ProductID                 uuid.UUID           `json:"product_id"`
	ExternalWorkspaceID       string              `json:"workspace_id"`
	ExternalUserID            string              `json:"user_id"`
	IntegrationID             string              `json:"integration_id"`
	ProposalFingerprint       string              `json:"proposal_fingerprint"`
	FinancialReviewHash       string              `json:"financial_review_hash"`
	CredentialBinding         string              `json:"credential_binding"`
	ExecutorCredentialsDigest string              `json:"executor_credentials_digest"`
	Marketplace               string              `json:"marketplace"`
	SellerPriceCents          int64               `json:"seller_price_cents"`
	WBBasePriceRub            int64               `json:"wb_base_price_rub,omitempty"`
	BeforeWB                  *wb.PriceSnapshot   `json:"before_wb,omitempty"`
	BeforeOzon                *ozon.PriceSnapshot `json:"before_ozon,omitempty"`
	IssuedAt                  time.Time           `json:"issued_at"`
	ExpiresAt                 time.Time           `json:"expires_at"`
	ExecutionControlVersion   *int64              `json:"execution_control_version,omitempty"`
}

type ManagerPriceValidator func(context.Context, ApprovedPriceCommand) error

type ManagerPriceReceipt struct {
	Version             string    `json:"version"`
	OperationID         uuid.UUID `json:"operation_id"`
	NativeChangeID      uuid.UUID `json:"native_change_id"`
	Marketplace         string    `json:"marketplace"`
	ProposalFingerprint string    `json:"proposal_fingerprint"`
	Status              string    `json:"status"`
	NativeState         string    `json:"native_state"`
}

var managerPriceHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var managerExternalID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func (c ApprovedPriceCommand) validate(marketplace string, fresh bool) error {
	if c.ExecutionControlVersion != nil && (*c.ExecutionControlVersion < 0 || *c.ExecutionControlVersion > 9007199254740991) {
		return errors.New("invalid original price execution control version")
	}
	if c.Version != "manager_price_approval_v1" || c.OperationID == uuid.Nil || c.ProductID == uuid.Nil || c.Marketplace != marketplace ||
		!managerExternalID.MatchString(c.ExternalWorkspaceID) || !managerExternalID.MatchString(c.ExternalUserID) || !managerExternalID.MatchString(c.IntegrationID) ||
		!managerPriceHash.MatchString(c.ProposalFingerprint) || !managerPriceHash.MatchString(c.FinancialReviewHash) || !managerPriceHash.MatchString(c.CredentialBinding) || !managerPriceHash.MatchString(c.ExecutorCredentialsDigest) ||
		c.SellerPriceCents <= 0 || c.SellerPriceCents > 100_000_000_000 || c.IssuedAt.IsZero() || c.ExpiresAt.IsZero() || !c.ExpiresAt.After(c.IssuedAt) || c.ExpiresAt.Sub(c.IssuedAt) > 5*time.Minute {
		return errors.New("unverified immutable manager price approval")
	}
	if fresh && (!c.ExpiresAt.After(time.Now()) || c.IssuedAt.After(time.Now().Add(5*time.Second))) {
		return errors.New("manager price approval expired or is future dated")
	}
	if marketplace == "wb" {
		if c.BeforeOzon != nil || c.BeforeWB == nil || c.WBBasePriceRub <= 0 || c.WBBasePriceRub > 1_000_000_000 || !managerSimpleWB(*c.BeforeWB) ||
			c.WBBasePriceRub*int64(100-c.BeforeWB.DiscountPercent) != c.SellerPriceCents || c.WBBasePriceRub*100 == c.BeforeWB.Sizes[0].PriceCents {
			return errors.New("unrepresentable exact WB manager target")
		}
	} else if marketplace == "ozon" {
		p := c.BeforeOzon
		if c.BeforeWB != nil || c.WBBasePriceRub != 0 || p == nil || p.ProductID <= 0 || p.OfferID == "" || p.PriceCents <= 0 || p.PriceCents == c.SellerPriceCents ||
			p.OldPriceCents == nil || p.MinPriceCents == nil || *p.OldPriceCents < 0 || *p.MinPriceCents < 0 || p.PriceCents > 100_000_000_000 || *p.OldPriceCents > 100_000_000_000 || *p.MinPriceCents > 100_000_000_000 || !p.HasSimpleConditions() {
			return errors.New("unverified simple Ozon manager target")
		}
	} else {
		return errors.New("unsupported manager price marketplace")
	}
	return nil
}

// ManagerPriceCredentialsDigest binds an approval to the credentials actually
// used by the native writer, independently of the financial owner's private
// credential binding. Lengths count UTF-8 bytes, as PHP strlen does.
func ManagerPriceCredentialsDigest(marketplace, clientID, key string) string {
	if marketplace == "wb" {
		marketplace = "wildberries"
	}
	h := sha256.New()
	h.Write([]byte("manager-price-executor-credentials-v1\x00"))
	for _, v := range []string{marketplace, clientID, key} {
		h.Write([]byte(strconv.Itoa(len(v)) + ":" + v))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func managerCabinetMatches(c ApprovedPriceCommand, cabinet domain.SellerCabinet) bool {
	return cabinet.Marketplace == c.Marketplace && cabinet.DeletedAt == nil &&
		cabinet.ExternalIntegrationID != nil && *cabinet.ExternalIntegrationID == c.IntegrationID
}

func (s *RepricerService) managerWBCredentials(ctx context.Context, workspace, cabinetID uuid.UUID, c ApprovedPriceCommand) (string, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return "", err
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.WorkspaceID != workspace || !managerCabinetMatches(c, cabinet) {
		return "", errors.New("manager WB cabinet binding changed")
	}
	token, err := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
	if err != nil || token == "" || c.ExecutorCredentialsDigest != ManagerPriceCredentialsDigest("wb", "", token) {
		return "", errors.New("manager WB credentials changed or unavailable")
	}
	return token, nil
}

func (s *OzonRepricerService) managerOzonCredentials(ctx context.Context, workspace, cabinetID uuid.UUID, c ApprovedPriceCommand) (ozon.Credentials, error) {
	cabinet, creds, err := s.resolveCabinet(ctx, workspace, cabinetID)
	if err != nil {
		return ozon.Credentials{}, err
	}
	if !managerCabinetMatches(c, *cabinet) || c.ExecutorCredentialsDigest != ManagerPriceCredentialsDigest("ozon", creds.ClientID, creds.APIKey) {
		return ozon.Credentials{}, errors.New("manager Ozon cabinet credentials changed")
	}
	return creds, nil
}

func managerSimpleWB(p wb.PriceSnapshot) bool {
	if p.NmID <= 0 || p.DiscountPercent < 0 || p.DiscountPercent > 99 || p.ClubDiscountPercent == nil || *p.ClubDiscountPercent != 0 ||
		p.EditableSizePrice == nil || *p.EditableSizePrice || p.WholesaleConditions == nil || len(*p.WholesaleConditions) != 0 || len(p.Sizes) == 0 {
		return false
	}
	seen := map[int64]bool{}
	base := p.Sizes[0].PriceCents
	for _, s := range p.Sizes {
		if s.SizeID <= 0 || seen[s.SizeID] || base <= 0 || base%100 != 0 || s.PriceCents != base ||
			s.DiscountedPriceCents != base/100*int64(100-p.DiscountPercent) || s.ClubPriceCents != s.DiscountedPriceCents {
			return false
		}
		seen[s.SizeID] = true
	}
	return true
}

func managerWBObserved(c ApprovedPriceCommand, p wb.PriceSnapshot, after bool) bool {
	if c.BeforeWB == nil || !managerSimpleWB(p) || p.NmID != c.BeforeWB.NmID || p.DiscountPercent != c.BeforeWB.DiscountPercent || len(p.Sizes) != len(c.BeforeWB.Sizes) {
		return false
	}
	if c.BeforeWB.VendorCode != "" && p.VendorCode != c.BeforeWB.VendorCode {
		return false
	}
	wanted := map[int64]wb.PriceSizeSnapshot{}
	for _, s := range c.BeforeWB.Sizes {
		wanted[s.SizeID] = s
	}
	for _, s := range p.Sizes {
		old, ok := wanted[s.SizeID]
		if !ok {
			return false
		}
		if after {
			if s.PriceCents != c.WBBasePriceRub*100 || s.DiscountedPriceCents != c.SellerPriceCents {
				return false
			}
		} else if s != old {
			return false
		}
	}
	return true
}

func managerOzonObserved(c ApprovedPriceCommand, p ozon.PriceSnapshot, after bool) bool {
	expected := c.BeforeOzon
	if expected == nil || p.ProductID != expected.ProductID || p.OfferID != expected.OfferID || !p.HasSimpleConditions() || p.OldPriceCents == nil || p.MinPriceCents == nil ||
		expected.OldPriceCents == nil || expected.MinPriceCents == nil || *p.OldPriceCents != *expected.OldPriceCents || *p.MinPriceCents != *expected.MinPriceCents {
		return false
	}
	if after {
		return p.PriceCents == c.SellerPriceCents
	}
	return p.PriceCents == expected.PriceCents
}

func managerApprovalFromContext(raw []byte) (*ApprovedPriceCommand, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("unreadable price decision context")
	}
	data, exists := fields["manager_approval"]
	if !exists {
		return nil, nil
	}
	var c ApprovedPriceCommand
	if json.Unmarshal(data, &c) != nil || c.validate(c.Marketplace, false) != nil {
		return nil, errors.New("invalid manager approval in price journal")
	}
	return &c, nil
}

func managerCommandRecord(c ApprovedPriceCommand, workspace, cabinet, actor uuid.UUID) (sqlcgen.ManagerPriceCommand, []byte, error) {
	if workspace == uuid.Nil || cabinet == uuid.Nil || actor == uuid.Nil {
		return sqlcgen.ManagerPriceCommand{}, nil, errors.New("manager operation requires exact actor and cabinet")
	}
	c.IssuedAt, c.ExpiresAt = c.IssuedAt.UTC().Truncate(time.Microsecond), c.ExpiresAt.UTC().Truncate(time.Microsecond)
	encoded, err := json.Marshal(c)
	if err != nil {
		return sqlcgen.ManagerPriceCommand{}, nil, err
	}
	sum := sha256.Sum256(encoded)
	contextJSON, err := json.Marshal(map[string]any{"actor_type": "manager_personal_approval", "write_protocol": "observed_v1", "manager_approval": c})
	return sqlcgen.ManagerPriceCommand{OperationID: uuidToPgtype(c.OperationID), WorkspaceID: uuidToPgtype(workspace), SellerCabinetID: uuidToPgtype(cabinet), ActorID: uuidToPgtype(actor),
		Marketplace: c.Marketplace, Fingerprint: hex.EncodeToString(sum[:]), Approval: encoded, ExpiresAt: c.ExpiresAt}, contextJSON, err
}

func managerReceipt(row sqlcgen.ManagerPriceCommand, c ApprovedPriceCommand, state string) *ManagerPriceReceipt {
	status := "unknown"
	switch state {
	case "pending", "submitting":
		status = "executing"
	case "uploaded", "verifying":
		status = "verifying"
	case "applied":
		status = "succeeded"
	case "failed":
		status = "failed"
	}
	return &ManagerPriceReceipt{Version: "manager_price_receipt_v1", OperationID: c.OperationID, NativeChangeID: uuidFromPgtype(row.NativeChangeID), Marketplace: c.Marketplace, ProposalFingerprint: c.ProposalFingerprint, Status: status, NativeState: state}
}

func sameManagerRecord(a, b sqlcgen.ManagerPriceCommand) bool {
	return a.OperationID == b.OperationID && a.WorkspaceID == b.WorkspaceID && a.SellerCabinetID == b.SellerCabinetID && a.ActorID == b.ActorID && a.Marketplace == b.Marketplace && a.Fingerprint == b.Fingerprint && a.ExpiresAt.Equal(b.ExpiresAt)
}

func verifiedManagerRecord(row sqlcgen.ManagerPriceCommand, nativeContext []byte) (ApprovedPriceCommand, error) {
	var c ApprovedPriceCommand
	if json.Unmarshal(row.Approval, &c) != nil || c.validate(row.Marketplace, false) != nil {
		return c, sqlcgen.ErrManagerPriceCommandConflict
	}
	record, _, err := managerCommandRecord(c, uuidFromPgtype(row.WorkspaceID), uuidFromPgtype(row.SellerCabinetID), uuidFromPgtype(row.ActorID))
	if err != nil || !sameManagerRecord(row, record) {
		return c, sqlcgen.ErrManagerPriceCommandConflict
	}
	original, err := managerApprovalFromContext(nativeContext)
	if err != nil || original == nil {
		return c, sqlcgen.ErrManagerPriceCommandConflict
	}
	nativeRecord, _, err := managerCommandRecord(*original, uuidFromPgtype(row.WorkspaceID), uuidFromPgtype(row.SellerCabinetID), uuidFromPgtype(row.ActorID))
	if err != nil || !sameManagerRecord(row, nativeRecord) {
		return c, sqlcgen.ErrManagerPriceCommandConflict
	}
	return c, nil
}

func wbDiscountInt32(d int) (int32, bool) {
	if d < 0 || d > 100 {
		return 0, false
	}
	return int32(d), true // #nosec G115 -- bounded to [0, 100] above
}

func managerWBRowMatches(c ApprovedPriceCommand, row sqlcgen.PriceChange) bool {
	if c.BeforeWB == nil {
		return false
	}
	disc, ok := wbDiscountInt32(c.BeforeWB.DiscountPercent)
	if !ok {
		return false
	}
	return row.Source == domain.PriceSourceManual && row.WbProductID == c.BeforeWB.NmID &&
		row.OldPriceRub == c.BeforeWB.Sizes[0].PriceCents/100 && row.NewPriceRub == c.WBBasePriceRub &&
		row.OldDiscountPercent == disc && row.NewDiscountPercent == disc
}
func managerOzonRowMatches(c ApprovedPriceCommand, row sqlcgen.OzonPriceChange) bool {
	p := c.BeforeOzon
	if p == nil || p.OldPriceCents == nil || p.MinPriceCents == nil || row.Source != domain.PriceSourceManual || row.Sku != p.ProductID || !row.OfferID.Valid || row.OfferID.String != p.OfferID {
		return false
	}
	for _, v := range []struct {
		valid    bool
		amount   float64
		expected int64
	}{{row.OldPriceRub.Valid, pgNumericToFloat(row.OldPriceRub), p.PriceCents}, {row.NewPriceRub.Valid, pgNumericToFloat(row.NewPriceRub), c.SellerPriceCents}, {row.OldOldPriceRub.Valid, pgNumericToFloat(row.OldOldPriceRub), *p.OldPriceCents}, {row.NewOldPriceRub.Valid, pgNumericToFloat(row.NewOldPriceRub), *p.OldPriceCents}, {row.MinPriceRub.Valid, pgNumericToFloat(row.MinPriceRub), *p.MinPriceCents}} {
		cents, err := exactOzonPriceCents(v.amount, false)
		if !v.valid || err != nil || cents != v.expected {
			return false
		}
	}
	return true
}

// ApplyApprovedManagerPrice never clamps, rounds or steps the confirmed target.
// Its native claim is atomic with the operation registry, so an aliasing worker
// cannot dispatch this command without passing the same financial validation.
func (s *RepricerService) ApplyApprovedManagerPrice(ctx context.Context, workspace, cabinet, actor uuid.UUID, c ApprovedPriceCommand) (*ManagerPriceReceipt, error) {
	if err := c.validate("wb", false); err != nil {
		return nil, err
	}
	record, decision, err := managerCommandRecord(c, workspace, cabinet, actor)
	if err != nil {
		return nil, err
	}
	if old, e := s.queries.GetManagerPriceCommand(ctx, record.OperationID, record.WorkspaceID, record.ActorID); e == nil {
		if !sameManagerRecord(old, record) {
			return nil, sqlcgen.ErrManagerPriceCommandConflict
		}
		return s.ManagerPriceReceipt(ctx, workspace, actor, c.OperationID)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if err = c.validate("wb", true); err != nil {
		return nil, err
	}
	if s.managerPriceValidator == nil {
		return nil, errors.New("financial approval validator is not configured")
	}
	if s.pricesEndpointCoolingDown(ctx, cabinet, wbEndpointPricesUpload) {
		return nil, errors.New("WB price upload is cooling down; no manager command sent")
	}
	token, err := s.managerWBCredentials(ctx, workspace, cabinet, c)
	if err != nil {
		return nil, err
	}
	it := priceChangeIntent{NmID: c.BeforeWB.NmID, OldPriceRub: c.BeforeWB.Sizes[0].PriceCents / 100, NewPriceRub: c.WBBasePriceRub, OldDiscount: c.BeforeWB.DiscountPercent, NewDiscount: c.BeforeWB.DiscountPercent, Source: domain.PriceSourceManual}
	if err = checkQuarantineRisk(it); err != nil {
		return nil, err
	}
	if err = checkRaiseAnomaly(it); err != nil {
		return nil, err
	}
	if err = s.managerPriceValidator(ctx, c); err != nil {
		return nil, err
	}
	before := c.BeforeWB
	beforeDisc, ok := wbDiscountInt32(before.DiscountPercent)
	if !ok {
		return nil, errors.New("WB discount percent out of range")
	}
	_, claimed, created, err := s.queries.CreateManagerWBPriceCommand(ctx, record, sqlcgen.CreatePriceChangeParams{
		WorkspaceID: record.WorkspaceID, SellerCabinetID: record.SellerCabinetID, WbProductID: before.NmID, OldPriceRub: before.Sizes[0].PriceCents / 100,
		NewPriceRub: c.WBBasePriceRub, OldDiscountPercent: beforeDisc, NewDiscountPercent: beforeDisc,
		Source: domain.PriceSourceManual, WbStatus: domain.PriceStatusPending, Reason: "personally approved exact manager price", DecisionContext: decision, CreatedBy: record.ActorID}, uuidToPgtype(uuid.New()))
	if err != nil {
		return nil, err
	}
	if !created {
		return s.ManagerPriceReceipt(ctx, workspace, actor, c.OperationID)
	}
	_, dispatchErr := s.uploadClaimedChunk(ctx, workspace, cabinet, token, claimed)
	receipt, readErr := s.ManagerPriceReceipt(context.WithoutCancel(ctx), workspace, actor, c.OperationID)
	return receipt, errors.Join(dispatchErr, readErr)
}

func (s *RepricerService) ManagerPriceReceipt(ctx context.Context, workspace, actor, operation uuid.UUID) (*ManagerPriceReceipt, error) {
	row, err := s.queries.GetManagerPriceCommand(ctx, uuidToPgtype(operation), uuidToPgtype(workspace), uuidToPgtype(actor))
	if err != nil {
		return nil, err
	}
	if row.Marketplace != "wb" || !row.NativeChangeID.Valid {
		return nil, sqlcgen.ErrManagerPriceCommandConflict
	}
	if err = s.queries.ExpireUnsentManagerPriceCommand(ctx, row); err != nil {
		return nil, err
	}
	native, err := s.queries.GetPriceChange(ctx, row.NativeChangeID)
	if err != nil {
		return nil, err
	}
	c, err := verifiedManagerRecord(row, native.DecisionContext)
	if err != nil || c.OperationID != operation || native.WorkspaceID != row.WorkspaceID || native.SellerCabinetID != row.SellerCabinetID || native.CreatedBy != row.ActorID || !managerWBRowMatches(c, native) {
		return nil, sqlcgen.ErrManagerPriceCommandConflict
	}
	return managerReceipt(row, c, native.WbStatus), nil
}

func (s *OzonRepricerService) ApplyApprovedManagerPrice(ctx context.Context, workspace, cabinet, actor uuid.UUID, c ApprovedPriceCommand) (*ManagerPriceReceipt, error) {
	if err := c.validate("ozon", false); err != nil {
		return nil, err
	}
	record, decision, err := managerCommandRecord(c, workspace, cabinet, actor)
	if err != nil {
		return nil, err
	}
	if old, e := s.queries.GetManagerPriceCommand(ctx, record.OperationID, record.WorkspaceID, record.ActorID); e == nil {
		if !sameManagerRecord(old, record) {
			return nil, sqlcgen.ErrManagerPriceCommandConflict
		}
		return s.ManagerPriceReceipt(ctx, workspace, actor, c.OperationID)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if err = c.validate("ozon", true); err != nil {
		return nil, err
	}
	if s.managerPriceValidator == nil {
		return nil, errors.New("financial approval validator is not configured")
	}
	creds, err := s.managerOzonCredentials(ctx, workspace, cabinet, c)
	if err != nil {
		return nil, err
	}
	if err = s.managerPriceValidator(ctx, c); err != nil {
		return nil, err
	}
	before := c.BeforeOzon
	stored, created, err := s.queries.CreateManagerOzonPriceCommand(ctx, record, sqlcgen.InsertOzonPriceChangeParams{
		SellerCabinetID: record.SellerCabinetID, Sku: before.ProductID, OfferID: textToPgtype(before.OfferID), OldPriceRub: floatToPgNumeric(float64(before.PriceCents) / 100),
		NewPriceRub: floatToPgNumeric(float64(c.SellerPriceCents) / 100), OldOldPriceRub: floatToPgNumeric(float64(*before.OldPriceCents) / 100),
		NewOldPriceRub: floatToPgNumeric(float64(*before.OldPriceCents) / 100), MinPriceRub: floatToPgNumeric(float64(*before.MinPriceCents) / 100),
		Reason: textToPgtype("personally approved exact manager price"), Source: domain.PriceSourceManual, Status: domain.OzonPriceStatusPending, DecisionContext: decision})
	if err != nil {
		return nil, err
	}
	if !created {
		return s.ManagerPriceReceipt(ctx, workspace, actor, c.OperationID)
	}
	dispatchErr := s.dispatchPriceIntents(ctx, cabinet, creds, []ozonPriceIntent{{ChangeID: stored.NativeChangeID, SKU: before.ProductID}})
	receipt, readErr := s.ManagerPriceReceipt(context.WithoutCancel(ctx), workspace, actor, c.OperationID)
	return receipt, errors.Join(dispatchErr, readErr)
}

func (s *OzonRepricerService) ManagerPriceReceipt(ctx context.Context, workspace, actor, operation uuid.UUID) (*ManagerPriceReceipt, error) {
	row, err := s.queries.GetManagerPriceCommand(ctx, uuidToPgtype(operation), uuidToPgtype(workspace), uuidToPgtype(actor))
	if err != nil {
		return nil, err
	}
	if row.Marketplace != "ozon" || !row.NativeChangeID.Valid {
		return nil, sqlcgen.ErrManagerPriceCommandConflict
	}
	if err = s.queries.ExpireUnsentManagerPriceCommand(ctx, row); err != nil {
		return nil, err
	}
	native, err := s.queries.GetOzonPriceChangeByID(ctx, row.NativeChangeID)
	if err != nil {
		return nil, err
	}
	c, err := verifiedManagerRecord(row, native.DecisionContext)
	if err != nil || c.OperationID != operation || native.SellerCabinetID != row.SellerCabinetID || !managerOzonRowMatches(c, native) {
		return nil, sqlcgen.ErrManagerPriceCommandConflict
	}
	return managerReceipt(row, c, native.Status), nil
}

func (s *RepricerService) validateManagerWBDispatch(ctx context.Context, row sqlcgen.PriceChange, p wb.PriceSnapshot, transportToken string) error {
	c, err := managerApprovalFromContext(row.DecisionContext)
	if err != nil {
		return err
	}
	if c == nil {
		if row.Reason == "personally approved exact manager price" {
			return errors.New("manager approval missing from WB journal")
		}
		return nil
	}
	if c.Marketplace != "wb" || c.validate("wb", true) != nil || !managerWBObserved(*c, p, false) || !managerWBRowMatches(*c, row) || s.managerPriceValidator == nil {
		return errors.New("manager WB approval or native baseline changed")
	}
	token, err := s.managerWBCredentials(ctx, uuidFromPgtype(row.WorkspaceID), uuidFromPgtype(row.SellerCabinetID), *c)
	if err != nil || token != transportToken {
		return errors.New("manager WB transport credential binding changed")
	}
	if err = s.managerPriceValidator(ctx, *c); err != nil {
		return fmt.Errorf("manager financial approval unavailable: %w", err)
	}
	return c.validate("wb", true)
}

func (s *OzonRepricerService) validateManagerOzonDispatch(ctx context.Context, row sqlcgen.OzonPriceChange, p ozon.PriceSnapshot, transportCreds ozon.Credentials) error {
	c, err := managerApprovalFromContext(row.DecisionContext)
	if err != nil {
		return err
	}
	if c == nil {
		if row.Reason.String == "personally approved exact manager price" {
			return errors.New("manager approval missing from Ozon journal")
		}
		return nil
	}
	if c.Marketplace != "ozon" || c.validate("ozon", true) != nil || !managerOzonObserved(*c, p, false) || !managerOzonRowMatches(*c, row) || s.managerPriceValidator == nil {
		return errors.New("manager Ozon approval or native baseline changed")
	}
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, row.SellerCabinetID)
	if err != nil {
		return err
	}
	creds, err := s.managerOzonCredentials(ctx, uuidFromPgtype(cabinet.WorkspaceID), uuidFromPgtype(row.SellerCabinetID), *c)
	if err != nil || creds.ClientID != transportCreds.ClientID || creds.APIKey != transportCreds.APIKey {
		return errors.New("manager Ozon transport credential binding changed")
	}
	if err = s.managerPriceValidator(ctx, *c); err != nil {
		return fmt.Errorf("manager financial approval unavailable: %w", err)
	}
	return c.validate("ozon", true)
}

// Options are deliberately explicit. Ordinary repricer construction cannot
// approve these operations without a financial-owner validation adapter.
func WithManagerWBPriceValidator(v ManagerPriceValidator) RepricerOption {
	return func(s *RepricerService) { s.managerPriceValidator = v }
}
func (s *OzonRepricerService) WithManagerPriceValidator(v ManagerPriceValidator) *OzonRepricerService {
	s.managerPriceValidator = v
	return s
}
