package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const priceAccountControlScope = "configured_price_account"

var priceExecutionChecks = []string{"price_account_scope_checked", "price_service_effect_checked", "price_reopening_checked"}

type priceControlWBReader interface {
	ReadPriceSnapshots(context.Context, string, []int64) (map[int64]wb.PriceSnapshot, error)
}
type priceControlOzonReader interface {
	ReadPriceSnapshots(context.Context, ozon.Credentials, []int64) (map[int64]ozon.PriceSnapshot, error)
}
type ManagerPriceExecutionControlService struct {
	q       *sqlcgen.Queries
	crm     managerAdAuthority
	ads     *ManagerAdsService
	wb      priceControlWBReader
	ozon    priceControlOzonReader
	key     []byte
	enabled bool
}

func NewManagerPriceExecutionControlService(q *sqlcgen.Queries, crm managerAdAuthority, w priceControlWBReader, o priceControlOzonReader, key []byte, enabled bool) *ManagerPriceExecutionControlService {
	return &ManagerPriceExecutionControlService{q: q, crm: crm, ads: NewManagerAdsService(q, crm), wb: w, ozon: o, key: key, enabled: enabled}
}
func priceControlCanManage(identity domain.ManagerAccess) bool {
	return identity.PersonalAccountVerified && identity.Allows("marketing.manage") && identity.Allows("unit_economics.calculate")
}
func (s *ManagerPriceExecutionControlService) access(ctx context.Context, identity domain.ManagerAccess, token string, write bool) (domain.ManagerAccess, error) {
	if s == nil || s.q == nil || s.crm == nil {
		return domain.ManagerAccess{}, apperror.New(errManagerAdsUnavailable, "price execution controls unavailable")
	}
	fresh, err := s.crm.VerifyManagerAccess(ctx, token, identity.ExternalWorkspaceID)
	if err != nil {
		return domain.ManagerAccess{}, err
	}
	if fresh == nil || fresh.ExternalUserID != identity.ExternalUserID || fresh.ExternalWorkspaceID != identity.ExternalWorkspaceID {
		return domain.ManagerAccess{}, apperror.New(apperror.ErrForbidden, "current price control owner required")
	}
	for _, permission := range []string{"products.view", "marketing.view", "unit_economics.view", "integrations.view"} {
		if !fresh.Allows(permission) {
			return domain.ManagerAccess{}, apperror.New(apperror.ErrForbidden, "current price control permissions required")
		}
	}
	if write && !priceControlCanManage(*fresh) {
		return domain.ManagerAccess{}, apperror.New(apperror.ErrForbidden, "current personal price control permission required")
	}
	return *fresh, nil
}

type priceExecutionTarget struct {
	identity                     domain.ManagerAccess
	integration                  *domain.ManagerIntegration
	cabinet                      *domain.ManagerAdsCabinet
	accountKey, digest, resource string
	read                         func(context.Context) (string, error)
}

func (s *ManagerPriceExecutionControlService) target(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration, resource string, write bool) (priceExecutionTarget, error) {
	var result priceExecutionTarget
	id, err := strconv.ParseInt(resource, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != resource {
		return result, apperror.New(apperror.ErrValidation, "exact native price resource required")
	}
	fresh, err := s.access(ctx, identity, token, write)
	if err != nil {
		return result, err
	}
	current, cab, err := s.ads.resolveCabinet(ctx, fresh, workspace, token, integration)
	if err != nil {
		return result, err
	}
	stored, err := s.q.GetSellerCabinetByID(ctx, uuidToPgtype(cab.ID))
	if err != nil {
		return result, apperror.New(errManagerAdsUnavailable, "price cabinet binding unavailable")
	}
	private := sellerCabinetFromSqlc(stored)
	if private.WorkspaceID != workspace || private.DeletedAt != nil || private.Marketplace != cab.Marketplace || private.ExternalIntegrationID == nil || *private.ExternalIntegrationID != integration || strings.TrimSpace(current.Name) == "" || len(current.Name) > 512 {
		return result, apperror.New(apperror.ErrForbidden, "price cabinet scope changed")
	}
	result = priceExecutionTarget{identity: fresh, integration: current, cabinet: cab, resource: resource}
	if cab.Marketplace == "wb" {
		if s.wb == nil {
			return result, apperror.New(errManagerAdsUnavailable, "native WB price proof unavailable")
		}
		secret, decryptErr := crypto.Decrypt(private.EncryptedToken, s.key)
		if decryptErr != nil {
			return result, apperror.New(errManagerAdsUnavailable, "WB price credentials unavailable")
		}
		result.accountKey, err = adguard.WBAccountKey(secret)
		if err != nil {
			return result, apperror.New(errManagerAdsUnavailable, "WB seller identity unavailable")
		}
		result.digest = ManagerPriceCredentialsDigest("wb", "", secret)
		result.read = func(readCtx context.Context) (string, error) {
			rows, e := s.wb.ReadPriceSnapshots(readCtx, secret, []int64{id})
			if e != nil {
				return "", e
			}
			p, ok := rows[id]
			if !ok || len(rows) != 1 || p.NmID != id {
				return "", errors.New("WB did not confirm the exact price resource")
			}
			return boundedControlName(p.VendorCode), nil
		}
	} else if cab.Marketplace == "ozon" {
		if s.ozon == nil {
			return result, apperror.New(errManagerAdsUnavailable, "native Ozon price proof unavailable")
		}
		privateCreds, decryptErr := decryptOzonCredentialsBlob(private.EncryptedCredentials, s.key)
		if decryptErr != nil {
			return result, apperror.New(errManagerAdsUnavailable, "Ozon price credentials unavailable")
		}
		creds := ozonClientCreds(privateCreds)
		if creds.ClientID == "" || creds.APIKey == "" {
			return result, apperror.New(errManagerAdsUnavailable, "Ozon Seller credentials unavailable")
		}
		key, keyErr := ozonPriceAccountKey(creds)
		if keyErr != nil {
			return result, apperror.New(errManagerAdsUnavailable, "Ozon seller identity unavailable")
		}
		result.accountKey = "ozon:" + key
		result.digest = ManagerPriceCredentialsDigest("ozon", creds.ClientID, creds.APIKey)
		result.read = func(readCtx context.Context) (string, error) {
			rows, e := s.ozon.ReadPriceSnapshots(readCtx, creds, []int64{id})
			if e != nil {
				return "", e
			}
			p, ok := rows[id]
			if !ok || len(rows) != 1 || p.ProductID != id {
				return "", errors.New("Ozon did not confirm the exact price resource")
			}
			return boundedControlName(p.OfferID), nil
		}
	} else {
		return result, apperror.New(apperror.ErrValidation, "unsupported native price account")
	}
	return result, nil
}
func samePriceExecutionTarget(a, b priceExecutionTarget) bool {
	return a.identity.ExternalUserID == b.identity.ExternalUserID && a.identity.ExternalWorkspaceID == b.identity.ExternalWorkspaceID && a.cabinet.ID == b.cabinet.ID && a.cabinet.Marketplace == b.cabinet.Marketplace && a.integration.ID == b.integration.ID && a.integration.Name == b.integration.Name && a.accountKey == b.accountKey && a.digest == b.digest && a.resource == b.resource
}
func priceControlContextFingerprint(c domain.PriceExecutionControlContext, t priceExecutionTarget) string {
	return adDigest([]any{"price_execution_context_v1", t.identity.ExternalUserID, t.identity.ExternalWorkspaceID, t.cabinet.ID, t.accountKey, t.digest, c.IntegrationID, c.IntegrationName, c.Marketplace, c.Scope, c.Control, c.AccountProof.ResourceID, c.AccountProof.ResourceLabel, c.AccountProof.Source})
}
func priceControlOperationFingerprint(o domain.PriceExecutionControlOperation) string {
	return adDigest([]any{"price_execution_control_v1", o.ID, o.WorkspaceID, o.ActorID, o.ExternalUserID, o.ExternalWorkspaceID, o.IntegrationID, o.IntegrationName, o.CabinetID, o.Marketplace, o.AccountKey, o.CredentialDigest, o.Scope, o.TargetMode, o.Before, o.Proof, o.Reason, o.CreatedAt.UTC(), o.ExpiresAt.UTC()})
}
func (s *ManagerPriceExecutionControlService) context(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration, resource string, write bool) (*domain.PriceExecutionControlContext, priceExecutionTarget, error) {
	target, err := s.target(ctx, identity, workspace, token, integration, resource, write)
	if err != nil {
		return nil, target, err
	}
	label, err := target.read(ctx)
	if err != nil {
		return nil, target, apperror.New(errManagerAdsUnavailable, "current native price account proof unavailable")
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	after, err := s.target(ctx, target.identity, workspace, token, integration, resource, write)
	if err != nil {
		return nil, target, err
	}
	if !samePriceExecutionTarget(target, after) {
		return nil, target, apperror.New(apperror.ErrConflict, "price account binding changed during verification")
	}
	target = after
	state, err := s.q.PriceExecutionControl(ctx, target.accountKey, target.cabinet.Marketplace)
	if err != nil {
		return nil, target, apperror.New(errManagerAdsUnavailable, "price execution control could not be read")
	}
	source := "WB Prices API"
	if target.cabinet.Marketplace == "ozon" {
		source = "Ozon Seller API"
	}
	result := &domain.PriceExecutionControlContext{WorkspaceID: target.identity.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: target.integration.Name, Marketplace: target.cabinet.Marketplace, Scope: priceAccountControlScope, AccountProof: domain.PriceExecutionAccountProof{ResourceID: resource, ResourceLabel: label, Source: source, ObservedAt: observed}, Control: state, ApprovalAvailable: s.enabled && priceControlCanManage(target.identity)}
	result.ContextFingerprint = priceControlContextFingerprint(*result, target)
	return result, target, nil
}
func (s *ManagerPriceExecutionControlService) Context(ctx context.Context, identity domain.ManagerAccess, workspace uuid.UUID, token, integration, resource string) (*domain.PriceExecutionControlContext, error) {
	c, target, err := s.context(ctx, identity, workspace, token, integration, resource, false)
	if err != nil {
		return nil, err
	}
	// A financial proposal is prepared from this read. Unresolved legacy
	// admissions hold every new Native command of the account, so the caller must
	// learn about them before asking for an approval that cannot be dispatched.
	// Unavailable journal storage is an error, never an empty journal.
	held, err := s.q.LegacyPriceHeldCount(ctx, target.accountKey)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current legacy price holds unavailable")
	}
	c.LegacyHeldCount = held
	return c, nil
}

func (s *ManagerPriceExecutionControlService) Prepare(ctx context.Context, identity domain.ManagerAccess, workspace, actor uuid.UUID, token, integration string, resource string, targetMode, reason, contextFingerprint string) (*domain.PriceExecutionControlOperation, error) {
	if s == nil || !s.enabled {
		return nil, apperror.New(errManagerAdsUnavailable, "new price execution controls disabled")
	}
	if actor == uuid.Nil {
		return nil, apperror.New(apperror.ErrForbidden, "verified local actor required")
	}
	reason = strings.TrimSpace(reason)
	if (targetMode != "stopped" && targetMode != "manual_only") || len([]rune(reason)) < 3 || len([]rune(reason)) > 500 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || len(contextFingerprint) != 64 {
		return nil, apperror.New(apperror.ErrValidation, "exact target mode, reason and current account context required")
	}
	current, target, err := s.context(ctx, identity, workspace, token, integration, resource, true)
	if err != nil {
		return nil, err
	}
	if current.ContextFingerprint != contextFingerprint {
		return nil, apperror.New(apperror.ErrConflict, "price account or execution control changed")
	}
	if current.Control.Mode == targetMode {
		return nil, apperror.New(apperror.ErrConflict, "price execution control already has this mode")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := domain.PriceExecutionControlOperation{ID: uuid.New(), WorkspaceID: workspace, ActorID: actor, ExternalUserID: target.identity.ExternalUserID, ExternalWorkspaceID: target.identity.ExternalWorkspaceID, IntegrationID: integration, IntegrationName: current.IntegrationName, CabinetID: target.cabinet.ID, Marketplace: current.Marketplace, AccountKey: target.accountKey, CredentialDigest: target.digest, Scope: priceAccountControlScope, TargetMode: targetMode, Before: current.Control, Proof: current.AccountProof, Reason: reason, Status: "awaiting_confirmation", ApprovalAvailable: true, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	o.Fingerprint = priceControlOperationFingerprint(o)
	if err = s.q.CreatePriceExecutionControlOperation(ctx, o); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "price execution control proposal could not be saved")
	}
	return s.receipt(ctx, o)
}
func (s *ManagerPriceExecutionControlService) owned(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string, write bool) (domain.PriceExecutionControlOperation, error) {
	var empty domain.PriceExecutionControlOperation
	if s == nil || s.q == nil || s.crm == nil {
		return empty, apperror.New(errManagerAdsUnavailable, "price execution control unavailable")
	}
	fresh, err := s.access(ctx, identity, token, write)
	if err != nil {
		return empty, err
	}
	o, err := s.q.GetPriceExecutionControlOperation(ctx, id, workspace, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, apperror.New(apperror.ErrNotFound, "personal control proposal not found")
	}
	if err != nil {
		return empty, apperror.New(errManagerAdsUnavailable, "control proposal could not be verified")
	}
	if o.ExternalWorkspaceID != fresh.ExternalWorkspaceID || o.ExternalUserID != fresh.ExternalUserID || o.WorkspaceID != workspace || o.ActorID != actor || o.Scope != priceAccountControlScope || o.NativePricesChanged || priceControlOperationFingerprint(o) != o.Fingerprint {
		return empty, apperror.New(apperror.ErrConflict, "control proposal does not match personal scope")
	}
	if err = s.q.ExpirePriceExecutionControlOperation(ctx, o); err != nil {
		return empty, apperror.New(errManagerAdsUnavailable, "control proposal expiry unavailable")
	}
	saved, err := s.q.GetPriceExecutionControlOperation(ctx, id, workspace, actor)
	if err == nil {
		saved.ApprovalAvailable = priceControlCanManage(fresh)
	}
	return saved, err
}
func (s *ManagerPriceExecutionControlService) receipt(ctx context.Context, o domain.PriceExecutionControlOperation) (*domain.PriceExecutionControlOperation, error) {
	current, err := s.q.PriceExecutionControl(ctx, o.AccountKey, o.Marketplace)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "current execution control unavailable")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	o.CurrentControl = &current
	o.CurrentCheckedAt = &now
	o.ApprovalAvailable = o.ApprovalAvailable && s.enabled && o.Status == "awaiting_confirmation" && o.ExpiresAt.After(now) && current.Version == o.Before.Version && current.Mode == o.Before.Mode
	return &o, nil
}
func (s *ManagerPriceExecutionControlService) Receipt(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token string) (*domain.PriceExecutionControlOperation, error) {
	o, err := s.owned(ctx, identity, workspace, actor, id, token, false)
	if err != nil {
		return nil, err
	}
	return s.receipt(ctx, o)
}
func (s *ManagerPriceExecutionControlService) Cancel(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string) (*domain.PriceExecutionControlOperation, error) {
	o, err := s.owned(ctx, identity, workspace, actor, id, token, false)
	if err != nil {
		return nil, err
	}
	if fingerprint != o.Fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "control proposal changed")
	}
	if err = s.q.CancelPriceExecutionControlOperation(ctx, o); err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "control cancellation unavailable")
	}
	return s.Receipt(ctx, identity, workspace, actor, id, token)
}
func (s *ManagerPriceExecutionControlService) Approve(ctx context.Context, identity domain.ManagerAccess, workspace, actor, id uuid.UUID, token, fingerprint string, checks map[string]bool) (*domain.PriceExecutionControlOperation, error) {
	if s == nil || !s.enabled {
		return nil, apperror.New(errManagerAdsUnavailable, "new price execution controls disabled")
	}
	if actor == uuid.Nil {
		return nil, apperror.New(apperror.ErrForbidden, "verified local actor required")
	}
	o, err := s.owned(ctx, identity, workspace, actor, id, token, true)
	if err != nil {
		return nil, err
	}
	if fingerprint != o.Fingerprint {
		return nil, apperror.New(apperror.ErrConflict, "control proposal changed")
	}
	if o.Status != "awaiting_confirmation" {
		return s.receipt(ctx, o)
	}
	if len(checks) != len(priceExecutionChecks) {
		return nil, apperror.New(apperror.ErrValidation, "three separate personal execution-control checks required")
	}
	for _, key := range priceExecutionChecks {
		if !checks[key] {
			return nil, apperror.New(apperror.ErrValidation, "three separate personal execution-control checks required")
		}
	}
	err = s.q.ApplyPriceExecutionControl(ctx, o, func(locked context.Context) error {
		current, err := s.target(locked, identity, workspace, token, o.IntegrationID, o.Proof.ResourceID, true)
		if err != nil {
			return err
		}
		if current.cabinet.ID != o.CabinetID || current.integration.Name != o.IntegrationName || current.cabinet.Marketplace != o.Marketplace || current.accountKey != o.AccountKey || current.digest != o.CredentialDigest || current.identity.ExternalUserID != o.ExternalUserID || current.resource != o.Proof.ResourceID {
			return apperror.New(apperror.ErrConflict, "price account or credential binding changed")
		}
		// Recheck permissions after the store/credential lookup while the native
		// account lock still prevents a concurrent dispatch marker.
		_, err = s.access(locked, current.identity, token, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	persisted, readErr := s.q.GetPriceExecutionControlOperation(ctx, id, workspace, actor)
	if readErr != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "committed control receipt unavailable; read saved decision before another action")
	}
	return s.receipt(ctx, persisted)
}
