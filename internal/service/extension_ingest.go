package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
		"wb.adverts":        {},
		"wb.campaign.stats": {},
		"wb.query.clusters": {},
		"wb.bid.estimate":   {},
		"wb.positions":      {},
		"wb.serp.snapshot":  {},
		"wb.ui.auction":     {},
		"wb.ui.search":      {},
	}
)

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

func (s *ExtensionService) resolveProductID(ctx context.Context, workspaceID uuid.UUID, productID *uuid.UUID, metadata json.RawMessage) (*uuid.UUID, error) {
	if productID != nil {
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
		SellerCabinetID: uuidToPgtypePtr(input.SellerCabinetID),
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

	for _, input := range inputs {
		if input.PhraseID == nil && input.Query == nil && input.CampaignID == nil {
			return 0, apperror.New(apperror.ErrValidation, "bid snapshot requires phrase, query or campaign context")
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		metadata := normalizeJSON(input.Metadata)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			return 0, err
		}
		confidence, convErr := numericFromFloat64(input.Confidence)
		if convErr != nil {
			return 0, apperror.New(apperror.ErrValidation, "invalid bid snapshot confidence")
		}
		row, createErr := s.queries.CreateExtensionBidSnapshot(ctx, sqlcgen.CreateExtensionBidSnapshotParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(input.SellerCabinetID),
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
			return 0, apperror.New(apperror.ErrInternal, "failed to store extension bid snapshot")
		}
		_ = row
	}

	if err := s.touchSession(ctx, session.ID); err != nil {
		return 0, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return len(inputs), nil
}

func (s *ExtensionService) CreatePositionSnapshots(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionPositionSnapshotInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one position snapshot is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	for _, input := range inputs {
		metadata := normalizeJSON(input.Metadata)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			return 0, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, metadata)
		if err != nil {
			return 0, err
		}
		if productID == nil {
			return 0, apperror.New(apperror.ErrValidation, "position snapshot requires product_id")
		}
		if strings.TrimSpace(input.Query) == "" || strings.TrimSpace(input.Region) == "" {
			return 0, apperror.New(apperror.ErrValidation, "position snapshot requires query and region")
		}
		if input.VisiblePosition <= 0 {
			return 0, apperror.New(apperror.ErrValidation, "visible_position must be positive")
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := strings.TrimSpace(input.Query)
		region := strings.TrimSpace(input.Region)
		confidence, convErr := numericFromFloat64(input.Confidence)
		if convErr != nil {
			return 0, apperror.New(apperror.ErrValidation, "invalid position snapshot confidence")
		}
		row, createErr := s.queries.CreateExtensionPositionSnapshot(ctx, sqlcgen.CreateExtensionPositionSnapshotParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(input.SellerCabinetID),
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
			return 0, apperror.New(apperror.ErrInternal, "failed to store extension position snapshot")
		}
		_ = row
	}

	if err := s.touchSession(ctx, session.ID); err != nil {
		return 0, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return len(inputs), nil
}

func (s *ExtensionService) CreateUISignals(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionUISignalInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one ui signal is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	for _, input := range inputs {
		if strings.TrimSpace(input.SignalType) == "" || strings.TrimSpace(input.Title) == "" {
			return 0, apperror.New(apperror.ErrValidation, "ui signal requires signal_type and title")
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		metadata := normalizeJSON(input.Metadata)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, metadata)
		if err != nil {
			return 0, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, metadata)
		if err != nil {
			return 0, err
		}
		confidence, convErr := numericFromFloat64(input.Confidence)
		if convErr != nil {
			return 0, apperror.New(apperror.ErrValidation, "invalid ui signal confidence")
		}
		row, createErr := s.queries.CreateExtensionUISignal(ctx, sqlcgen.CreateExtensionUISignalParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(input.SellerCabinetID),
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
			return 0, apperror.New(apperror.ErrInternal, "failed to store extension ui signal")
		}
		_ = row
	}

	if err := s.touchSession(ctx, session.ID); err != nil {
		return 0, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return len(inputs), nil
}

func (s *ExtensionService) CreateNetworkCaptures(ctx context.Context, userID, workspaceID uuid.UUID, inputs []CreateExtensionNetworkCaptureInput) (int, error) {
	if len(inputs) == 0 {
		return 0, apperror.New(apperror.ErrValidation, "at least one network capture is required")
	}
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return 0, err
	}

	for _, input := range inputs {
		pageType := strings.TrimSpace(strings.ToLower(input.PageType))
		if _, ok := allowedExtensionPageTypes[pageType]; !ok {
			return 0, apperror.New(apperror.ErrValidation, "unsupported network capture page_type")
		}
		endpointKey := strings.TrimSpace(strings.ToLower(input.EndpointKey))
		if _, ok := allowedExtensionEndpointKeys[endpointKey]; !ok {
			return 0, apperror.New(apperror.ErrValidation, "unsupported network capture endpoint_key")
		}
		if len(input.Payload) == 0 {
			return 0, apperror.New(apperror.ErrValidation, "network capture payload is required")
		}
		capturedAt := extensionCapturedAt(input.CapturedAt)
		query := normalizeOptionalText(input.Query)
		region := normalizeOptionalText(input.Region)
		payload := normalizeJSON(input.Payload)
		campaignID, err := s.resolveCampaignID(ctx, workspaceID, input.CampaignID, payload)
		if err != nil {
			return 0, err
		}
		productID, err := s.resolveProductID(ctx, workspaceID, input.ProductID, payload)
		if err != nil {
			return 0, err
		}
		row, createErr := s.queries.CreateExtensionNetworkCapture(ctx, sqlcgen.CreateExtensionNetworkCaptureParams{
			SessionID:       session.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			UserID:          uuidToPgtype(userID),
			SellerCabinetID: uuidToPgtypePtr(input.SellerCabinetID),
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
			return 0, apperror.New(apperror.ErrInternal, "failed to store extension network capture")
		}
		_ = row
	}

	if err := s.touchSession(ctx, session.ID); err != nil {
		return 0, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}
	return len(inputs), nil
}

func buildExtensionDedupeKey(kind string, capturedAt time.Time, parts ...string) string {
	bucket := capturedAt.UTC().Truncate(time.Minute).Format(time.RFC3339)
	payload := kind + "|" + bucket + "|" + strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
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
