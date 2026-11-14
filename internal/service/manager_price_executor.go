package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type managerPriceSessionKey struct{}

// The personal token exists only in this request's context. It is never stored
// in the command or reconstructed by a background worker after a restart.
func NewManagerPriceFinancialValidator(baseURL string) ManagerPriceValidator {
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, c ApprovedPriceCommand) error {
		token, _ := ctx.Value(managerPriceSessionKey{}).(string)
		if baseURL == "" || token == "" || strings.ContainsAny(token, " \t\r\n") {
			return errors.New("personal financial validation session unavailable")
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/manager/price-proposals/"+c.OperationID.String()+"/validate-execution", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Sellico-Workspace", c.ExternalWorkspaceID)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return errors.New("financial owner validation unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return errors.New("financial owner refused or could not validate the price approval")
		}
		var verdict struct {
			Version             string    `json:"version"`
			OperationID         uuid.UUID `json:"operation_id"`
			ProposalFingerprint string    `json:"proposal_fingerprint"`
			FinancialReviewHash string    `json:"financial_review_hash"`
			ExecutionAllowed    bool      `json:"execution_allowed"`
			ExpiresAt           time.Time `json:"expires_at"`
		}
		decoder := json.NewDecoder(io.LimitReader(res.Body, 8193))
		if decoder.Decode(&verdict) != nil || decoder.Decode(&struct{}{}) != io.EOF || verdict.Version != "manager_price_validation_v1" || verdict.OperationID != c.OperationID || verdict.ProposalFingerprint != c.ProposalFingerprint || verdict.FinancialReviewHash != c.FinancialReviewHash || !verdict.ExecutionAllowed || !verdict.ExpiresAt.Equal(c.ExpiresAt) || !verdict.ExpiresAt.After(time.Now()) {
			return errors.New("financial owner returned an unverified approval")
		}
		return nil
	}
}

type ManagerPriceExecutor struct {
	queries *sqlcgen.Queries
	crm     ManagerIntegrationVerifier
	wb      *RepricerService
	ozon    *OzonRepricerService
	enabled bool
}

func NewManagerPriceExecutor(q *sqlcgen.Queries, crm ManagerIntegrationVerifier, wb *RepricerService, ozon *OzonRepricerService, enabled bool) *ManagerPriceExecutor {
	return &ManagerPriceExecutor{queries: q, crm: crm, wb: wb, ozon: ozon, enabled: enabled}
}

func (s *ManagerPriceExecutor) Apply(ctx context.Context, access domain.ManagerAccess, workspace, actor uuid.UUID, token string, c ApprovedPriceCommand) (*ManagerPriceReceipt, error) {
	if s == nil || !s.enabled || s.wb == nil || s.ozon == nil || !access.Allows("marketing.manage") || !access.Allows("unit_economics.calculate") {
		return nil, errors.New("manager price execution stopped or not authorized")
	}
	if workspace == uuid.Nil || actor == uuid.Nil || c.ExternalUserID != access.ExternalUserID || c.ExternalWorkspaceID != access.ExternalWorkspaceID || token == "" || c.validate(c.Marketplace, false) != nil {
		return nil, errors.New("manager price approval scope is invalid")
	}
	for _, p := range []string{"products.view", "marketing.view", "unit_economics.view", "integrations.view"} {
		if !access.Allows(p) {
			return nil, errors.New("manager price scope permissions missing")
		}
	}
	if s.crm == nil || s.queries == nil {
		return nil, errors.New("manager cabinet verifier unavailable")
	}
	integration, err := s.crm.VerifyManagerIntegration(ctx, token, access.ExternalWorkspaceID, c.IntegrationID)
	if err != nil {
		return nil, err
	}
	cabinet, err := s.queries.ManagerAdsCabinetByIntegration(ctx, workspace, c.IntegrationID)
	if err != nil {
		return nil, err
	}
	if integration == nil || integration.ID != c.IntegrationID || integration.WorkspaceID != c.ExternalWorkspaceID || integration.Marketplace != c.Marketplace || cabinet == nil || cabinet.ID == uuid.Nil || cabinet.WorkspaceID != workspace || cabinet.Marketplace != c.Marketplace || cabinet.IntegrationID != c.IntegrationID {
		return nil, errors.New("manager cabinet mapping changed or is ambiguous")
	}
	ctx = context.WithValue(ctx, managerPriceSessionKey{}, token)
	if c.Marketplace == "wb" {
		return s.wb.ApplyApprovedManagerPrice(ctx, workspace, cabinet.ID, actor, c)
	}
	return s.ozon.ApplyApprovedManagerPrice(ctx, workspace, cabinet.ID, actor, c)
}

func (s *ManagerPriceExecutor) Receipt(ctx context.Context, access domain.ManagerAccess, workspace, actor, operation uuid.UUID) (*ManagerPriceReceipt, error) {
	if s == nil || s.queries == nil || workspace == uuid.Nil || actor == uuid.Nil || !access.Allows("marketing.view") || !access.Allows("unit_economics.view") {
		return nil, errors.New("manager receipt access denied")
	}
	row, err := s.queries.GetManagerPriceCommand(ctx, uuidToPgtype(operation), uuidToPgtype(workspace), uuidToPgtype(actor))
	if err != nil {
		return nil, err
	}
	var c ApprovedPriceCommand
	if json.Unmarshal(row.Approval, &c) != nil || c.ExternalUserID != access.ExternalUserID || c.ExternalWorkspaceID != access.ExternalWorkspaceID {
		return nil, errors.New("manager receipt scope differs")
	}
	if row.Marketplace == "wb" && s.wb != nil {
		receipt, err := s.wb.ManagerPriceReceipt(ctx, workspace, actor, operation)
		if err != nil || receipt.Status != "verifying" {
			return receipt, err
		}
		task, err := s.queries.GetPendingWBPriceTaskForChange(ctx, row.WorkspaceID, row.NativeChangeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return receipt, nil
		}
		if err != nil {
			return nil, err
		}
		token, err := s.wb.managerWBCredentials(ctx, workspace, uuidFromPgtype(row.SellerCabinetID), c)
		if err != nil {
			return receipt, nil
		}
		s.wb.pollOneTask(ctx, workspace, token, task)
		return s.wb.ManagerPriceReceipt(ctx, workspace, actor, operation)
	}
	if row.Marketplace == "ozon" && s.ozon != nil {
		receipt, err := s.ozon.ManagerPriceReceipt(ctx, workspace, actor, operation)
		if err != nil || (receipt.Status != "unknown" && receipt.Status != "verifying" && receipt.Status != "executing") {
			return receipt, err
		}
		if _, err = s.ozon.managerOzonCredentials(ctx, workspace, uuidFromPgtype(row.SellerCabinetID), c); err != nil {
			return receipt, nil
		}
		if _, err = s.ozon.ReconcilePriceChange(ctx, workspace, receipt.NativeChangeID); err != nil {
			return receipt, nil
		}
		return s.ozon.ManagerPriceReceipt(ctx, workspace, actor, operation)
	}
	return nil, errors.New("manager receipt source unavailable")
}
