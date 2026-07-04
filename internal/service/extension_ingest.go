package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

var (
	allowedExtensionPageTypes = map[string]struct{}{
		"campaign": {},
		"query":    {},
		"product":  {},
		"search":   {},
		"auction":  {},
		"cabinet":  {},
	}
	allowedExtensionEndpointKeys = map[string]struct{}{
		"wb.adverts":               {},
		"wb.balance":               {},
		"wb.bid.estimate":          {},
		"wb.bid.min":               {},
		"wb.bid.product":           {},
		"wb.bid.recommendations":   {},
		"wb.budget":                {},
		"wb.campaign.inventory":    {},
		"wb.campaign.settings":     {},
		"wb.campaign.stats":        {},
		"wb.finance":               {},
		"wb.positions":             {},
		"wb.query.bids":            {},
		"wb.query.clusters":        {},
		"wb.query.minus":           {},
		"wb.query.stats":           {},
		"wb.sales_funnel.products": {},
		"wb.search_report":         {},
		"wb.serp.snapshot":         {},
		"wb.ui.advert":             {},
		"wb.ui.analyst":            {},
		"wb.ui.auction":            {},
		"wb.ui.campaign.stats":     {},
		"wb.ui.placement":          {},
		"wb.ui.search":             {},
	}
	allowedExtensionTableRoles = map[string]struct{}{
		"campaigns": {},
		"products":  {},
		"queries":   {},
		"bids":      {},
		"stats":     {},
		"finance":   {},
		"unknown":   {},
	}
	allowedExtensionEndpointPathNeedles = map[string][]string{
		"wb.adverts":               {"/adv/v1/promotion/adverts", "/adv/v0/advert"},
		"wb.balance":               {"/adv/v1/balance"},
		"wb.bid.estimate":          {"/adv/v2/recommended-bids"},
		"wb.bid.min":               {"/api/advert/v1/bids/min"},
		"wb.bid.product":           {"/api/advert/v1/bids"},
		"wb.bid.recommendations":   {"/api/advert/v0/bids/recommendations", "/api/v1/advert/preset-bids"},
		"wb.budget":                {"/adv/v1/budget"},
		"wb.campaign.inventory":    {"/adv/v1/promotion/count"},
		"wb.campaign.settings":     {"/api/advert/v2/adverts"},
		"wb.campaign.stats":        {"/adv/v3/fullstats", "/adv/v0/stats"},
		"wb.finance":               {"/adv/v1/upd", "/adv/v1/payments"},
		"wb.positions":             {"/positions", "/position"},
		"wb.query.bids":            {"/adv/v0/normquery/get-bids", "/adv/v0/normquery/bids"},
		"wb.query.clusters":        {"/adv/v0/normquery"},
		"wb.query.minus":           {"/adv/v0/normquery/set-minus"},
		"wb.query.stats":           {"/adv/v1/normquery/stats"},
		"wb.sales_funnel.products": {"/api/analytics/v3/sales-funnel/products"},
		"wb.search_report":         {"/api/v2/search-report"},
		"wb.serp.snapshot":         {"/search"},
		"wb.ui.advert":             {"/api/v1/advert/"},
		"wb.ui.analyst":            {"/api/v5/analyst-info"},
		"wb.ui.auction":            {"/adv/v1/auction/adverts"},
		"wb.ui.campaign.stats":     {"/api/v5/fullstat"},
		"wb.ui.placement":          {"/placement"},
		"wb.ui.search":             {"/search"},
	}
)

var extensionSensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`(?i)\b(access_token|refresh_token|auth_token|token|jwt|authorization)=([^&\s"'<>]+)`),
	regexp.MustCompile(`\b[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`(?i)\b(sessionid|session_id|sid|jwt|token|access_token|refresh_token)=([^;\s]+)`),
}

type CreateExtensionPageContextInput struct {
	URL             string
	PageType        string
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	PhraseID        *uuid.UUID
	ProductID       *uuid.UUID
	Query           *string
	Region          *string
	ActiveFilters   json.RawMessage
	Metadata        json.RawMessage
	CapturedAt      *time.Time
}

type CreateExtensionBidSnapshotInput struct {
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	PhraseID        *uuid.UUID
	Query           *string
	Region          *string
	VisibleBid      *int64
	RecommendedBid  *int64
	CompetitiveBid  *int64
	LeadershipBid   *int64
	CPMMin          *int64
	Confidence      float64
	Metadata        json.RawMessage
	CapturedAt      *time.Time
}

type CreateExtensionPositionSnapshotInput struct {
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	PhraseID        *uuid.UUID
	ProductID       *uuid.UUID
	Query           string
	Region          string
	VisiblePosition int
	VisiblePage     *int
	PageSubtype     *string
	Confidence      float64
	Metadata        json.RawMessage
	CapturedAt      *time.Time
}

type CreateExtensionUISignalInput struct {
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	PhraseID        *uuid.UUID
	ProductID       *uuid.UUID
	Query           *string
	Region          *string
	SignalType      string
	Severity        string
	Title           string
	Message         *string
	Confidence      float64
	Metadata        json.RawMessage
	CapturedAt      *time.Time
}

type CreateExtensionNetworkCaptureInput struct {
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	PhraseID        *uuid.UUID
	ProductID       *uuid.UUID
	PageType        string
	EndpointKey     string
	Query           *string
	Region          *string
	Payload         json.RawMessage
	CapturedAt      *time.Time
}

type CreateExtensionDOMRowSnapshotInput struct {
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	PhraseID        *uuid.UUID
	ProductID       *uuid.UUID
	PageType        string
	TableRole       string
	RowKey          string
	Query           *string
	Region          *string
	VisibleText     string
	Cells           json.RawMessage
	Metadata        json.RawMessage
	Confidence      float64
	CapturedAt      *time.Time
}

func (s *ExtensionService) resolveProductID(ctx context.Context, workspaceID uuid.UUID, productID *uuid.UUID, metadata json.RawMessage) (*uuid.UUID, error) {
	if productID != nil {
		if _, err := s.queries.GetProductByIDAndWorkspace(ctx, sqlcgen.GetProductByIDAndWorkspaceParams{
			ID:          uuidToPgtype(*productID),
			WorkspaceID: uuidToPgtype(workspaceID),
		}); errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrValidation, "product_id does not belong to workspace")
		} else if err != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to validate product workspace")
		}
		return productID, nil
	}
	wbProductID := metadataInt64(metadata, "wb_product_id", "wbProductID")
	if wbProductID <= 0 {
		return nil, nil
	}
	row, err := s.queries.GetProductByWBProductID(ctx, sqlcgen.GetProductByWBProductIDParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		WbProductID: wbProductID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to resolve product by wb_product_id")
	}
	id := uuidFromPgtype(row.ID)
	return &id, nil
}

func (s *ExtensionService) resolveCampaignID(ctx context.Context, workspaceID uuid.UUID, campaignID *uuid.UUID, metadata json.RawMessage) (*uuid.UUID, error) {
	if campaignID != nil {
		if _, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
			ID:          uuidToPgtype(*campaignID),
			WorkspaceID: uuidToPgtype(workspaceID),
		}); errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrValidation, "campaign_id does not belong to workspace")
		} else if err != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to validate campaign workspace")
		}
		return campaignID, nil
	}
	wbCampaignID := metadataInt64(metadata, "wb_campaign_id", "wbCampaignID")
	if wbCampaignID <= 0 {
		return nil, nil
	}
	row, err := s.queries.GetCampaignByWBCampaignID(ctx, sqlcgen.GetCampaignByWBCampaignIDParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		WbCampaignID: wbCampaignID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to resolve campaign by wb_campaign_id")
	}
	id := uuidFromPgtype(row.ID)
	return &id, nil
}

func (s *ExtensionService) resolveSellerCabinetID(ctx context.Context, workspaceID uuid.UUID, sellerCabinetID *uuid.UUID) (*uuid.UUID, error) {
	if sellerCabinetID == nil {
		return nil, nil
	}
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(*sellerCabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrValidation, "seller_cabinet_id does not belong to workspace")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to validate seller cabinet workspace")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrValidation, "seller_cabinet_id does not belong to workspace")
	}
	return sellerCabinetID, nil
}

func (s *ExtensionService) CreatePageContext(ctx context.Context, userID, workspaceID uuid.UUID, input CreateExtensionPageContextInput) (*domain.ExtensionPageContext, error) {
	pageType := strings.TrimSpace(strings.ToLower(input.PageType))
	if _, ok := allowedExtensionPageTypes[pageType]; !ok {
		return nil, apperror.New(apperror.ErrValidation, "unsupported extension page_type")
	}
	if strings.TrimSpace(input.URL) == "" {
		return nil, apperror.New(apperror.ErrValidation, "url is required")
	}

	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}

	capturedAt := extensionCapturedAt(input.CapturedAt)
	metadata := normalizeJSON(input.Metadata)
	activeFilters := normalizeJSON(input.ActiveFilters)
	campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
	if err != nil {
		return nil, err
	}
	productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, metadata)
	if err != nil {
		return nil, err
	}
	sellerCabinetID, err := s.resolveSellerCabinetID(ctx, workspaceID, input.SellerCabinetID)
	if err != nil {
		return nil, err
	}
	query := normalizeOptionalText(input.Query)
	region := normalizeOptionalText(input.Region)

	contextMeta, _ := json.Marshal(map[string]any{
		"url":               strings.TrimSpace(input.URL),
		"page_type":         pageType,
		"seller_cabinet_id": uuidString(input.SellerCabinetID),
		"campaign_id":       uuidString(campaignID),
		"phrase_id":         uuidString(input.PhraseID),
		"product_id":        uuidString(productID),
		"query":             stringOrNil(input.Query),
		"region":            stringOrNil(input.Region),
	})

	if _, err := s.queries.CreateExtensionContextEvent(ctx, sqlcgen.CreateExtensionContextEventParams{
		SessionID:   session.ID,
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(userID),
		Url:         strings.TrimSpace(input.URL),
		PageType:    pageType,
		Metadata:    contextMeta,
	}); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create extension context event")
	}

	row, err := s.queries.CreateExtensionPageContext(ctx, sqlcgen.CreateExtensionPageContextParams{
		SessionID:       session.ID,
		WorkspaceID:     uuidToPgtype(workspaceID),
		UserID:          uuidToPgtype(userID),
		Url:             strings.TrimSpace(input.URL),
		PageType:        pageType,
		SellerCabinetID: uuidToPgtypePtr(sellerCabinetID),
		CampaignID:      uuidToPgtypePtr(campaignID),
		PhraseID:        uuidToPgtypePtr(input.PhraseID),
		ProductID:       uuidToPgtypePtr(productID),
		Query:           textToPgtype(query),
		Region:          textToPgtype(region),
		ActiveFilters:   activeFilters,
		Metadata:        metadata,
		DedupeKey: buildExtensionDedupeKey(
			"page_context",
			capturedAt,
			pageType,
			strings.TrimSpace(input.URL),
			uuidString(campaignID),
			uuidString(input.PhraseID),
			uuidString(productID),
			query,
			region,
		),
		CapturedAt: timePtrToPgtype(&capturedAt),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to store extension page context")
	}
	if err := s.touchSession(ctx, session.ID); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}

	result := extensionPageContextFromSqlc(row)
	return &result, nil
}

func (s *ExtensionService) CreateBidSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionBidSnapshotInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one bid snapshot is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	accepted := 0
	skipped := 0
	for _, input := range inputs {
		metadata := normalizeJSON(input.Metadata)
		if input.PhraseID == nil && input.Query == nil && input.CampaignID == nil && metadataInt64(metadata, "wb_campaign_id", "wbCampaignID") <= 0 {
			log.Printf("[WARN] extension bid snapshot skipped: workspace_id=%s reason=%s", workspaceID, "bid snapshot requires phrase, query or campaign context")
			skipped++
			continue
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension bid snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		sellerCabinetID, err := s.resolveSellerCabinetID(ctx, workspaceID, input.SellerCabinetID)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension bid snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		confidence, convErr := numericFromFloat64(input.Confidence)
		if convErr != nil {
			log.Printf("[WARN] extension bid snapshot skipped: workspace_id=%s reason=%s", workspaceID, "invalid bid snapshot confidence")
			skipped++
			continue
		}
		row, createErr := s.queries.CreateExtensionBidSnapshot(ctx, sqlcgen.CreateExtensionBidSnapshotParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(sellerCabinetID),
			CampaignID:      uuidToPgtypePtr(campaignID),
			PhraseID:        uuidToPgtypePtr(input.PhraseID),
			Query:           textToPgtype(query),
			Region:          textToPgtype(region),
			VisibleBid:      optionalInt64ToPgInt8(input.VisibleBid),
			RecommendedBid:  optionalInt64ToPgInt8(input.RecommendedBid),
			CompetitiveBid:  optionalInt64ToPgInt8(input.CompetitiveBid),
			LeadershipBid:   optionalInt64ToPgInt8(input.LeadershipBid),
			CpmMin:          optionalInt64ToPgInt8(input.CPMMin),
			Source:          domain.SourceExtension,
			Confidence:      confidence,
			Metadata:        metadata,
			DedupeKey: buildExtensionDedupeKey(
				"bid_snapshot",
				capturedAt,
				uuidString(campaignID),
				uuidString(input.PhraseID),
				query,
				region,
				int64String(input.VisibleBid),
				int64String(input.RecommendedBid),
				int64String(input.CompetitiveBid),
			),
			CapturedAt: timePtrToPgtype(&capturedAt),
		})
		if createErr != nil {
			return accepted, apperror.New(apperror.ErrInternal, "failed to store extension bid snapshot")
		}
		_ = row
		accepted++
	}
	_ = skipped

	if err := s.touchSession(ctx, session.ID); err != nil {
		return accepted, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return accepted, nil
}

func (s *ExtensionService) CreatePositionSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionPositionSnapshotInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one position snapshot is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	accepted := 0
	skipped := 0
	for _, input := range inputs {
		metadata := normalizeJSON(input.Metadata)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		sellerCabinetID, err := s.resolveSellerCabinetID(ctx, workspaceID, input.SellerCabinetID)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		if productID == nil {
			log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%s", workspaceID, "position snapshot requires product_id")
			skipped++
			continue
		}
		if strings.TrimSpace(input.Query) == "" || strings.TrimSpace(input.Region) == "" {
			log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%s", workspaceID, "position snapshot requires query and region")
			skipped++
			continue
		}
		if input.VisiblePosition <= 0 {
			log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%s", workspaceID, "visible_position must be positive")
			skipped++
			continue
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := strings.TrimSpace(input.Query)
		region := strings.TrimSpace(input.Region)
		confidence, convErr := numericFromFloat64(input.Confidence)
		if convErr != nil {
			log.Printf("[WARN] extension position snapshot skipped: workspace_id=%s reason=%s", workspaceID, "invalid position snapshot confidence")
			skipped++
			continue
		}
		row, createErr := s.queries.CreateExtensionPositionSnapshot(ctx, sqlcgen.CreateExtensionPositionSnapshotParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(sellerCabinetID),
			CampaignID:      uuidToPgtypePtr(campaignID),
			PhraseID:        uuidToPgtypePtr(input.PhraseID),
			ProductID:       uuidToPgtypePtr(productID),
			Query:           query,
			Region:          region,
			VisiblePosition: int32(input.VisiblePosition),
			VisiblePage:     optionalIntToPgInt4(input.VisiblePage),
			PageSubtype:     textToPgtype(normalizeOptionalText(input.PageSubtype)),
			Source:          domain.SourceExtension,
			Confidence:      confidence,
			Metadata:        metadata,
			DedupeKey: buildExtensionDedupeKey(
				"position_snapshot",
				capturedAt,
				uuidString(campaignID),
				uuidString(input.PhraseID),
				uuidString(productID),
				query,
				region,
				fmt.Sprintf("%d", input.VisiblePosition),
				intString(input.VisiblePage),
			),
			CapturedAt: timePtrToPgtype(&capturedAt),
		})
		if createErr != nil {
			return accepted, apperror.New(apperror.ErrInternal, "failed to store extension position snapshot")
		}
		_ = row
		accepted++
	}
	_ = skipped

	if err := s.touchSession(ctx, session.ID); err != nil {
		return accepted, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return accepted, nil
}

func (s *ExtensionService) CreateUISignals(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionUISignalInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one ui signal is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	accepted := 0
	skipped := 0
	for _, input := range inputs {
		if strings.TrimSpace(input.SignalType) == "" || strings.TrimSpace(input.Title) == "" {
			log.Printf("[WARN] extension ui signal skipped: workspace_id=%s reason=%s", workspaceID, "ui signal requires signal_type and title")
			skipped++
			continue
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		metadata := normalizeJSON(input.Metadata)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension ui signal skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension ui signal skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		sellerCabinetID, err := s.resolveSellerCabinetID(ctx, workspaceID, input.SellerCabinetID)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension ui signal skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		confidence, convErr := numericFromFloat64(input.Confidence)
		if convErr != nil {
			log.Printf("[WARN] extension ui signal skipped: workspace_id=%s reason=%s", workspaceID, "invalid ui signal confidence")
			skipped++
			continue
		}
		row, createErr := s.queries.CreateExtensionUISignal(ctx, sqlcgen.CreateExtensionUISignalParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(sellerCabinetID),
			CampaignID:      uuidToPgtypePtr(campaignID),
			PhraseID:        uuidToPgtypePtr(input.PhraseID),
			ProductID:       uuidToPgtypePtr(productID),
			Query:           textToPgtype(query),
			Region:          textToPgtype(region),
			SignalType:      strings.TrimSpace(input.SignalType),
			Severity:        defaultString(strings.TrimSpace(input.Severity), "info"),
			Title:           strings.TrimSpace(input.Title),
			Message:         textToPgtype(normalizeOptionalText(input.Message)),
			Confidence:      confidence,
			Metadata:        metadata,
			DedupeKey: buildExtensionDedupeKey(
				"ui_signal",
				capturedAt,
				strings.TrimSpace(input.SignalType),
				uuidString(campaignID),
				uuidString(input.PhraseID),
				uuidString(productID),
				query,
				region,
				strings.TrimSpace(input.Title),
			),
			CapturedAt: timePtrToPgtype(&capturedAt),
		})
		if createErr != nil {
			return accepted, apperror.New(apperror.ErrInternal, "failed to store extension ui signal")
		}
		_ = row
		accepted++
	}
	_ = skipped

	if err := s.touchSession(ctx, session.ID); err != nil {
		return accepted, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return accepted, nil
}

func (s *ExtensionService) CreateNetworkCaptures(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionNetworkCaptureInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one network capture is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	accepted := 0
	skipped := 0
	var derivedBidInputs []CreateExtensionBidSnapshotInput
	var derivedPositionInputs []CreateExtensionPositionSnapshotInput
	var derivedSignalInputs []CreateExtensionUISignalInput
	for _, input := range inputs {
		pageType := strings.TrimSpace(strings.ToLower(input.PageType))
		if _, ok := allowedExtensionPageTypes[pageType]; !ok {
			log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%s", workspaceID, "unsupported network capture page_type")
			skipped++
			continue
		}
		endpointKey := strings.TrimSpace(strings.ToLower(input.EndpointKey))
		if _, ok := allowedExtensionEndpointKeys[endpointKey]; !ok {
			log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%s", workspaceID, "unsupported network capture endpoint_key")
			skipped++
			continue
		}
		if len(input.Payload) == 0 {
			log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%s", workspaceID, "network capture payload is required")
			skipped++
			continue
		}
		rawPayload := normalizeJSON(input.Payload)
		if err := validateExtensionNetworkCaptureURL(endpointKey, rawPayload); err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		payload := sanitizeExtensionPayload(rawPayload)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, payload)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, payload)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		sellerCabinetID, err := s.resolveSellerCabinetID(ctx, workspaceID, input.SellerCabinetID)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension network capture skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		derivedBidInputs = append(derivedBidInputs, deriveBidSnapshotsFromNetworkCapture(input, payload)...)
		derivedPositionInputs = append(derivedPositionInputs, derivePositionSnapshotsFromNetworkCapture(input, payload)...)
		derivedSignalInputs = append(derivedSignalInputs, deriveUISignalsFromNetworkCapture(input, payload)...)
		row, createErr := s.queries.CreateExtensionNetworkCapture(ctx, sqlcgen.CreateExtensionNetworkCaptureParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(sellerCabinetID),
			CampaignID:      uuidToPgtypePtr(campaignID),
			PhraseID:        uuidToPgtypePtr(input.PhraseID),
			ProductID:       uuidToPgtypePtr(productID),
			PageType:        pageType,
			EndpointKey:     endpointKey,
			Query:           textToPgtype(query),
			Region:          textToPgtype(region),
			Payload:         payload,
			DedupeKey: buildExtensionDedupeKey(
				"network_capture",
				capturedAt,
				pageType,
				endpointKey,
				uuidString(campaignID),
				uuidString(input.PhraseID),
				uuidString(productID),
				query,
				region,
				string(payload),
			),
			CapturedAt: timePtrToPgtype(&capturedAt),
		})
		if createErr != nil {
			return accepted, apperror.New(apperror.ErrInternal, "failed to store extension network capture")
		}
		_ = row
		accepted++
		if budget := deriveCampaignBudgetFromNetworkCapture(input, payload); budget != nil && campaignID != nil {
			if _, budgetErr := s.queries.UpsertCampaignBudget(ctx, sqlcgen.UpsertCampaignBudgetParams{
				CampaignID: uuidToPgtype(*campaignID),
				Cash:       budget.cash,
				Netting:    budget.netting,
				Total:      budget.total,
				CapturedAt: timePtrToPgtype(&capturedAt),
			}); budgetErr != nil {
				log.Printf("[WARN] extension network budget normalization failed: workspace_id=%s campaign_id=%s err=%v", workspaceID, campaignID.String(), budgetErr)
			}
		}
	}

	_ = skipped
	if err := s.touchSession(ctx, session.ID); err != nil {
		return accepted, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	if len(derivedBidInputs) > 0 {
		if _, err := s.CreateBidSnapshots(ctx, userID, workspaceID, derivedBidInputs); err != nil {
			log.Printf("[WARN] extension network bid normalization failed: workspace_id=%s items=%d err=%v", workspaceID, len(derivedBidInputs), err)
		}
	}
	if len(derivedPositionInputs) > 0 {
		if _, err := s.CreatePositionSnapshots(ctx, userID, workspaceID, derivedPositionInputs); err != nil {
			log.Printf("[WARN] extension network position normalization failed: workspace_id=%s items=%d err=%v", workspaceID, len(derivedPositionInputs), err)
		}
	}
	if len(derivedSignalInputs) > 0 {
		if _, err := s.CreateUISignals(ctx, userID, workspaceID, derivedSignalInputs); err != nil {
			log.Printf("[WARN] extension network ui signal normalization failed: workspace_id=%s items=%d err=%v", workspaceID, len(derivedSignalInputs), err)
		}
	}
	return accepted, nil
}

func (s *ExtensionService) CreateDOMRowSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionDOMRowSnapshotInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one dom row snapshot is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	accepted := 0
	skipped := 0
	for _, input := range inputs {
		pageType := strings.TrimSpace(strings.ToLower(input.PageType))
		if _, ok := allowedExtensionPageTypes[pageType]; !ok {
			log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%s", workspaceID, "unsupported dom row page_type")
			skipped++
			continue
		}
		tableRole := strings.TrimSpace(strings.ToLower(input.TableRole))
		if tableRole == "" {
			tableRole = "unknown"
		}
		if _, ok := allowedExtensionTableRoles[tableRole]; !ok {
			log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%s", workspaceID, "unsupported dom row table_role")
			skipped++
			continue
		}
		rowKey := truncateExtensionText(redactSensitiveString(strings.TrimSpace(input.RowKey)), 300)
		visibleText := truncateExtensionText(redactSensitiveString(strings.TrimSpace(input.VisibleText)), 1200)
		if rowKey == "" || visibleText == "" {
			log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%s", workspaceID, "dom row snapshot requires row_key and visible_text")
			skipped++
			continue
		}

		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		metadata := sanitizeExtensionPayload(normalizeJSON(input.Metadata))
		cells := sanitizeExtensionPayload(normalizeJSON(input.Cells))
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, metadata)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		sellerCabinetID, err := s.resolveSellerCabinetID(ctx, workspaceID, input.SellerCabinetID)
		if err != nil {
			if apperror.Is(err, apperror.ErrValidation) {
				log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%v", workspaceID, err)
				skipped++
				continue
			}
			return accepted, err
		}
		confidenceValue := input.Confidence
		if confidenceValue <= 0 {
			confidenceValue = 0.65
		}
		confidence, convErr := numericFromFloat64(confidenceValue)
		if convErr != nil {
			log.Printf("[WARN] extension dom row snapshot skipped: workspace_id=%s reason=%s", workspaceID, "invalid dom row snapshot confidence")
			skipped++
			continue
		}
		row, createErr := s.queries.CreateExtensionDOMRowSnapshot(ctx, sqlcgen.CreateExtensionDOMRowSnapshotParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(sellerCabinetID),
			CampaignID:      uuidToPgtypePtr(campaignID),
			PhraseID:        uuidToPgtypePtr(input.PhraseID),
			ProductID:       uuidToPgtypePtr(productID),
			PageType:        pageType,
			TableRole:       tableRole,
			RowKey:          rowKey,
			Query:           textToPgtype(query),
			Region:          textToPgtype(region),
			VisibleText:     visibleText,
			Cells:           cells,
			Metadata:        metadata,
			Source:          domain.SourceExtension,
			Confidence:      confidence,
			DedupeKey: buildExtensionDedupeKey(
				"dom_row_snapshot",
				capturedAt,
				pageType,
				tableRole,
				rowKey,
				uuidString(campaignID),
				uuidString(input.PhraseID),
				uuidString(productID),
				query,
				region,
			),
			CapturedAt: timePtrToPgtype(&capturedAt),
		})
		if createErr != nil {
			return accepted, apperror.New(apperror.ErrInternal, "failed to store extension dom row snapshot")
		}
		_ = row
		accepted++
	}
	_ = skipped

	if err := s.touchSession(ctx, session.ID); err != nil {
		return accepted, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return accepted, nil
}

func buildExtensionDedupeKey(kind string, capturedAt time.Time, parts ...string) string {
	bucket := capturedAt.UTC().Truncate(time.Minute).Format(time.RFC3339)
	payload := kind + "|" + bucket + "|" + strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func truncateExtensionText(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxLen {
		return value
	}
	return strings.TrimSpace(string(runes[:maxLen]))
}

func extensionCapturedAt(value *time.Time) time.Time {
	if value == nil {
		return time.Now().UTC()
	}
	return value.UTC()
}

func normalizeJSON(value json.RawMessage) []byte {
	if len(value) == 0 {
		return nil
	}
	return value
}

func sanitizeExtensionPayload(value json.RawMessage) []byte {
	if len(value) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(value, &payload); err != nil {
		return value
	}
	redactSensitiveFields(payload)
	out, err := json.Marshal(payload)
	if err != nil {
		return value
	}
	return out
}

func validateExtensionNetworkCaptureURL(endpointKey string, payload json.RawMessage) error {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return apperror.New(apperror.ErrValidation, "network capture payload must be valid JSON")
	}
	rawURL, _ := raw["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return apperror.New(apperror.ErrValidation, "network capture payload url is required")
	}
	if extensionCaptureURLHasSensitiveQuery(rawURL) {
		return apperror.New(apperror.ErrValidation, "network capture url contains sensitive query parameters")
	}
	path, host, err := parseExtensionCaptureURL(rawURL)
	if err != nil {
		return err
	}
	if host != "" && !isAllowedExtensionCaptureHost(host) {
		return apperror.New(apperror.ErrValidation, "network capture host is not allowed")
	}
	needles := allowedExtensionEndpointPathNeedles[endpointKey]
	if len(needles) == 0 {
		return nil
	}
	pathLower := strings.ToLower(path)
	for _, needle := range needles {
		if strings.Contains(pathLower, strings.ToLower(needle)) {
			return nil
		}
	}
	return apperror.New(apperror.ErrValidation, "network capture url does not match endpoint_key")
}

func parseExtensionCaptureURL(rawURL string) (path string, host string, err error) {
	if strings.HasPrefix(rawURL, "/") {
		return rawURL, "", nil
	}
	parsed, parseErr := url.Parse(rawURL)
	if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", apperror.New(apperror.ErrValidation, "network capture url is invalid")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", "", apperror.New(apperror.ErrValidation, "network capture url scheme is not allowed")
	}
	return parsed.EscapedPath(), strings.ToLower(parsed.Hostname()), nil
}

func extensionCaptureURLHasSensitiveQuery(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for key := range parsed.Query() {
		if isSensitiveExtensionPayloadKey(key) {
			return true
		}
	}
	return false
}

func isAllowedExtensionCaptureHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "seller.wildberries.ru" ||
		host == "cmp.wildberries.ru" ||
		host == "advert-api.wildberries.ru" ||
		host == "statistics-api.wildberries.ru" ||
		strings.HasSuffix(host, ".wildberries.ru") ||
		strings.HasSuffix(host, ".wb.ru") ||
		strings.HasSuffix(host, ".rwb.ru") ||
		strings.HasSuffix(host, ".wbbasket.ru") ||
		strings.HasSuffix(host, ".wbcontent.net")
}

func redactSensitiveFields(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if isSensitiveExtensionPayloadKey(key) {
				typed[key] = "[redacted]"
				continue
			}
			if text, ok := nested.(string); ok {
				typed[key] = redactSensitiveString(text)
				continue
			}
			redactSensitiveFields(nested)
		}
	case []any:
		for index, nested := range typed {
			if text, ok := nested.(string); ok {
				typed[index] = redactSensitiveString(text)
				continue
			}
			redactSensitiveFields(nested)
		}
	}
}

func redactSensitiveString(value string) string {
	out := value
	for _, pattern := range extensionSensitiveValuePatterns {
		out = pattern.ReplaceAllString(out, "[redacted]")
	}
	return out
}

func isSensitiveExtensionPayloadKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "authorization", "cookie", "set-cookie", "x-user-token", "x-access-token", "access_token", "access-token", "accesstoken", "refresh_token", "refresh-token", "refreshtoken", "session_id", "session-id", "sessionid", "sid", "token", "jwt", "password", "passwd", "secret", "api_key", "api-key", "apikey":
		return true
	default:
		return strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "authorization") ||
			strings.Contains(normalized, "access_token") ||
			strings.Contains(normalized, "refresh_token") ||
			strings.Contains(normalized, "session_id") ||
			strings.Contains(normalized, "sessionid") ||
			strings.Contains(normalized, "session_token")
	}
}

func normalizeOptionalText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func uuidString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func stringOrNil(value *string) any {
	if value == nil {
		return nil
	}
	v := strings.TrimSpace(*value)
	if v == "" {
		return nil
	}
	return v
}

func int64String(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%d", *value)
}

func intString(value *int) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%d", *value)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
