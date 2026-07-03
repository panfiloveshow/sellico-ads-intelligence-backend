package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type ExtensionEvidenceDebugInput struct {
	Scope      string
	CampaignID *uuid.UUID
	ProductID  *uuid.UUID
	PhraseID   *uuid.UUID
	Query      string
	Limit      int32
}

type ExtensionEvidenceDebug struct {
	WorkspaceID       uuid.UUID
	Scope             string
	CampaignID        *uuid.UUID
	ProductID         *uuid.UUID
	PhraseID          *uuid.UUID
	Query             string
	GeneratedAt       time.Time
	LatestCapturedAt  *time.Time
	Counts            ExtensionEvidenceDebugCounts
	DataStatus        ExtensionWidgetDataStatus
	PageContexts      []domain.ExtensionPageContext
	NetworkCaptures   []domain.ExtensionNetworkCapture
	DOMRowSnapshots   []domain.ExtensionDOMRowSnapshot
	BidSnapshots      []domain.ExtensionBidSnapshot
	PositionSnapshots []domain.ExtensionPositionSnapshot
	UISignals         []domain.ExtensionUISignal
	Issues            []ExtensionWidgetIssue
	NextActions       []ExtensionWidgetAction
}

type ExtensionEvidenceDebugCounts struct {
	PageContexts      int `json:"page_contexts"`
	NetworkCaptures   int `json:"network_captures"`
	DOMRowSnapshots   int `json:"dom_row_snapshots"`
	BidSnapshots      int `json:"bid_snapshots"`
	PositionSnapshots int `json:"position_snapshots"`
	UISignals         int `json:"ui_signals"`
}

type ExtensionEvidenceSupportReport struct {
	WorkspaceID      uuid.UUID
	Scope            string
	CampaignID       *uuid.UUID
	ProductID        *uuid.UUID
	PhraseID         *uuid.UUID
	Query            string
	GeneratedAt      time.Time
	LatestCapturedAt *time.Time
	Summary          ExtensionEvidenceSupportSummary
	Sections         []ExtensionEvidenceSupportSection
	Checklist        []ExtensionEvidenceSupportChecklistItem
	Issues           []ExtensionWidgetIssue
	NextActions      []ExtensionWidgetAction
}

type ExtensionEvidenceSupportSummary struct {
	SourceLabel        string
	Readiness          string
	CapturedSignals    int
	MissingSignals     int
	ConfirmedInCabinet bool
	FreshnessState     string
	Coverage           string
}

type ExtensionEvidenceSupportSection struct {
	ID               string
	Title            string
	Status           string
	Detail           string
	EvidenceCount    int
	LatestCapturedAt *time.Time
}

type ExtensionEvidenceSupportChecklistItem struct {
	ID         string
	Label      string
	Done       bool
	Detail     string
	ActionPath string
}

func (s *ExtensionService) GetEvidenceDebug(ctx context.Context, workspaceID uuid.UUID, input ExtensionEvidenceDebugInput) (*ExtensionEvidenceDebug, error) {
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if scope == "" {
		return nil, apperror.New(apperror.ErrValidation, "scope is required")
	}
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var campaignID, productID, phraseID *uuid.UUID
	query := strings.TrimSpace(input.Query)
	switch scope {
	case "campaign":
		if input.CampaignID == nil {
			return nil, apperror.New(apperror.ErrValidation, "campaign_id is required")
		}
		if err := s.validateCampaignWorkspace(ctx, workspaceID, *input.CampaignID); err != nil {
			return nil, err
		}
		campaignID = input.CampaignID
	case "product":
		if input.ProductID == nil {
			return nil, apperror.New(apperror.ErrValidation, "product_id is required")
		}
		if err := s.validateProductWorkspace(ctx, workspaceID, *input.ProductID); err != nil {
			return nil, err
		}
		productID = input.ProductID
	case "query", "phrase":
		scope = "query"
		if input.PhraseID != nil {
			phrase, err := s.getWorkspacePhrase(ctx, workspaceID, *input.PhraseID)
			if err != nil {
				return nil, err
			}
			phraseID = input.PhraseID
			if query == "" {
				query = phrase.Keyword
			}
		}
		if query == "" && phraseID == nil {
			return nil, apperror.New(apperror.ErrValidation, "query or phrase_id is required")
		}
	default:
		return nil, apperror.New(apperror.ErrValidation, "scope must be campaign, product or query")
	}

	pageContexts, err := s.listEvidenceDebugPageContexts(ctx, workspaceID, scope, campaignID, productID, phraseID, query, limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension page contexts")
	}
	networkCaptures, err := s.listEvidenceDebugNetworkCaptures(ctx, workspaceID, scope, campaignID, productID, phraseID, query, limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension network captures")
	}
	domRowSnapshots, err := s.listEvidenceDebugDOMRowSnapshots(ctx, workspaceID, scope, campaignID, productID, phraseID, query, limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension dom row snapshots")
	}
	bidSnapshots, err := s.listEvidenceDebugBidSnapshots(ctx, workspaceID, campaignID, phraseID, query, limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension bid snapshots")
	}
	positionSnapshots, err := s.listEvidenceDebugPositionSnapshots(ctx, workspaceID, campaignID, productID, phraseID, query, limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension position snapshots")
	}
	uiSignals, err := s.listEvidenceDebugUISignals(ctx, workspaceID, campaignID, productID, phraseID, query, limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension ui signals")
	}

	var latestBid *domain.ExtensionBidSnapshot
	if len(bidSnapshots) > 0 {
		latestBid = &bidSnapshots[0]
	}
	status := buildExtensionWidgetDataStatus(scope, latestBid, positionSnapshots, uiSignals)
	latest := latestEvidenceDebugTime(pageContexts, networkCaptures, domRowSnapshots, bidSnapshots, positionSnapshots, uiSignals)
	issues := append([]ExtensionWidgetIssue{}, status.Issues...)
	if len(networkCaptures)+len(domRowSnapshots) > 0 && len(bidSnapshots)+len(positionSnapshots)+len(uiSignals) == 0 {
		issues = append(issues, ExtensionWidgetIssue{
			Stage:      "normalization",
			Severity:   "warning",
			Message:    "Есть реальные ответы WB, но структурированные данные для этой сущности пока не появились. Проверьте обработку ответов или откройте страницу с более точным контекстом.",
			ActionPath: "refresh",
		})
	}
	if latest == nil {
		issues = append(issues, ExtensionWidgetIssue{
			Stage:      "evidence_capture",
			Severity:   "info",
			Message:    "Для этой сущности пока нет сохраненных данных из кабинета WB.",
			ActionPath: "refresh",
		})
	}

	return &ExtensionEvidenceDebug{
		WorkspaceID:       workspaceID,
		Scope:             scope,
		CampaignID:        campaignID,
		ProductID:         productID,
		PhraseID:          phraseID,
		Query:             query,
		GeneratedAt:       time.Now().UTC(),
		LatestCapturedAt:  latest,
		Counts:            ExtensionEvidenceDebugCounts{PageContexts: len(pageContexts), NetworkCaptures: len(networkCaptures), DOMRowSnapshots: len(domRowSnapshots), BidSnapshots: len(bidSnapshots), PositionSnapshots: len(positionSnapshots), UISignals: len(uiSignals)},
		DataStatus:        status,
		PageContexts:      pageContexts,
		NetworkCaptures:   networkCaptures,
		DOMRowSnapshots:   domRowSnapshots,
		BidSnapshots:      bidSnapshots,
		PositionSnapshots: positionSnapshots,
		UISignals:         uiSignals,
		Issues:            issues,
		NextActions:       status.NextActions,
	}, nil
}

func (s *ExtensionService) GetEvidenceSupportReport(ctx context.Context, workspaceID uuid.UUID, input ExtensionEvidenceDebugInput) (*ExtensionEvidenceSupportReport, error) {
	debug, err := s.GetEvidenceDebug(ctx, workspaceID, input)
	if err != nil {
		return nil, err
	}
	return buildExtensionEvidenceSupportReport(*debug), nil
}

func buildExtensionEvidenceSupportReport(debug ExtensionEvidenceDebug) *ExtensionEvidenceSupportReport {
	sections := []ExtensionEvidenceSupportSection{
		evidenceSupportSection("page_contexts", "Контекст страницы WB", debug.Counts.PageContexts, latestPageContextTime(debug.PageContexts)),
		evidenceSupportSection("network_captures", "Разрешенные ответы WB", debug.Counts.NetworkCaptures, latestNetworkCaptureTime(debug.NetworkCaptures)),
		evidenceSupportSection("dom_row_snapshots", "Видимые строки кабинета", debug.Counts.DOMRowSnapshots, latestDOMRowSnapshotTime(debug.DOMRowSnapshots)),
		evidenceSupportSection("bid_snapshots", "Ставки и аукцион", debug.Counts.BidSnapshots, latestBidSnapshotTime(debug.BidSnapshots)),
		evidenceSupportSection("position_snapshots", "Позиции товара/запроса", debug.Counts.PositionSnapshots, latestPositionSnapshotTime(debug.PositionSnapshots)),
		evidenceSupportSection("ui_signals", "Предупреждения и блокировки в интерфейсе", debug.Counts.UISignals, latestUISignalTime(debug.UISignals)),
	}

	capturedSignals := 0
	for _, section := range sections {
		if section.EvidenceCount > 0 {
			capturedSignals++
		}
	}
	missingSignals := len(sections) - capturedSignals
	readiness := "missing"
	if capturedSignals == len(sections) {
		readiness = "ready"
	} else if capturedSignals > 0 {
		readiness = "partial"
	}
	sourceLabel := "Нет данных из кабинета WB"
	if capturedSignals > 0 {
		sourceLabel = "Данные кабинета WB"
	}
	freshnessState := debug.DataStatus.FreshnessState
	if freshnessState == "" {
		freshnessState = extensionFreshnessState(debug.LatestCapturedAt)
	}
	coverage := debug.DataStatus.Coverage
	if coverage == "" {
		coverage = readiness
	}

	return &ExtensionEvidenceSupportReport{
		WorkspaceID:      debug.WorkspaceID,
		Scope:            debug.Scope,
		CampaignID:       debug.CampaignID,
		ProductID:        debug.ProductID,
		PhraseID:         debug.PhraseID,
		Query:            debug.Query,
		GeneratedAt:      debug.GeneratedAt,
		LatestCapturedAt: debug.LatestCapturedAt,
		Summary: ExtensionEvidenceSupportSummary{
			SourceLabel:        sourceLabel,
			Readiness:          readiness,
			CapturedSignals:    capturedSignals,
			MissingSignals:     missingSignals,
			ConfirmedInCabinet: debug.DataStatus.ConfirmedInCabinet,
			FreshnessState:     freshnessState,
			Coverage:           coverage,
		},
		Sections:    sections,
		Checklist:   evidenceSupportChecklist(debug),
		Issues:      append([]ExtensionWidgetIssue{}, debug.Issues...),
		NextActions: append([]ExtensionWidgetAction{}, debug.NextActions...),
	}
}

func evidenceSupportSection(id, title string, count int, latest *time.Time) ExtensionEvidenceSupportSection {
	status := "missing"
	detail := "Реальных данных для этого блока пока нет."
	if count > 0 {
		status = "ready"
		detail = "Есть сохраненные реальные данные кабинета для проверки."
	}
	return ExtensionEvidenceSupportSection{
		ID:               id,
		Title:            title,
		Status:           status,
		Detail:           detail,
		EvidenceCount:    count,
		LatestCapturedAt: latest,
	}
}

func evidenceSupportChecklist(debug ExtensionEvidenceDebug) []ExtensionEvidenceSupportChecklistItem {
	return []ExtensionEvidenceSupportChecklistItem{
		{
			ID:         "page_context",
			Label:      "Открыт релевантный экран WB",
			Done:       debug.Counts.PageContexts > 0,
			Detail:     evidenceChecklistDetail(debug.Counts.PageContexts > 0, "Контекст страницы сохранен.", "Откройте нужную кампанию, товар или запрос в кабинете WB."),
			ActionPath: "open_wb_cabinet",
		},
		{
			ID:         "network_capture",
			Label:      "Сохранены разрешенные ответы WB",
			Done:       debug.Counts.NetworkCaptures > 0,
			Detail:     evidenceChecklistDetail(debug.Counts.NetworkCaptures > 0, "Ответы WB сохранены.", "Обновите экран WB или примените фильтр, чтобы кабинет сделал запрос."),
			ActionPath: "refresh",
		},
		{
			ID:         "dom_rows",
			Label:      "Сохранены видимые строки таблицы",
			Done:       debug.Counts.DOMRowSnapshots > 0,
			Detail:     evidenceChecklistDetail(debug.Counts.DOMRowSnapshots > 0, "Видимые строки таблицы сохранены.", "Откройте таблицу кампаний, запросов или товаров в кабинете WB."),
			ActionPath: "refresh",
		},
		{
			ID:         "bid_evidence",
			Label:      "Есть реальные данные по ставкам",
			Done:       debug.Counts.BidSnapshots > 0,
			Detail:     evidenceChecklistDetail(debug.Counts.BidSnapshots > 0, "Ставки подтверждены реальными данными.", "Откройте блок ставок или аукциона в кабинете WB."),
			ActionPath: "open_bids",
		},
		{
			ID:         "position_evidence",
			Label:      "Есть evidence по позициям",
			Done:       debug.Counts.PositionSnapshots > 0,
			Detail:     evidenceChecklistDetail(debug.Counts.PositionSnapshots > 0, "Позиции сохранены.", "Откройте выдачу или карточку, где видна позиция товара."),
			ActionPath: "open_positions",
		},
		{
			ID:         "ui_signals",
			Label:      "Собраны предупреждения или блокировки",
			Done:       debug.Counts.UISignals > 0 || debug.DataStatus.ConfirmedInCabinet,
			Detail:     evidenceChecklistDetail(debug.Counts.UISignals > 0 || debug.DataStatus.ConfirmedInCabinet, "Предупреждения интерфейса или структурированные данные подтверждены.", "Проверьте предупреждения, заблокированные действия и сообщения кабинета WB."),
			ActionPath: "open_wb_cabinet",
		},
	}
}

func evidenceChecklistDetail(done bool, readyDetail, missingDetail string) string {
	if done {
		return readyDetail
	}
	return missingDetail
}

func (s *ExtensionService) validateCampaignWorkspace(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	row, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(campaignID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuidFromPgtype(row.WorkspaceID) != workspaceID) {
		return apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to get campaign")
	}
	return nil
}

func (s *ExtensionService) validateProductWorkspace(ctx context.Context, workspaceID, productID uuid.UUID) error {
	row, err := s.queries.GetProductByID(ctx, uuidToPgtype(productID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuidFromPgtype(row.WorkspaceID) != workspaceID) {
		return apperror.New(apperror.ErrNotFound, "product not found")
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to get product")
	}
	return nil
}

func (s *ExtensionService) getWorkspacePhrase(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.Phrase, error) {
	row, err := s.queries.GetPhraseByID(ctx, uuidToPgtype(phraseID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuidFromPgtype(row.WorkspaceID) != workspaceID) {
		return nil, apperror.New(apperror.ErrNotFound, "phrase not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get phrase")
	}
	phrase := phraseFromSqlc(row)
	return &phrase, nil
}

func (s *ExtensionService) listEvidenceDebugPageContexts(ctx context.Context, workspaceID uuid.UUID, scope string, campaignID, productID, phraseID *uuid.UUID, query string, limit int32) ([]domain.ExtensionPageContext, error) {
	rows, err := s.queries.ListExtensionPageContextsFiltered(ctx, sqlcgen.ListExtensionPageContextsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		PageTypeFilter:   textToPgtype(""),
		CampaignIDFilter: uuidToPgtypePtr(campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(phraseID),
		ProductIDFilter:  uuidToPgtypePtr(productID),
		QueryFilter:      textToPgtype(query),
		RegionFilter:     textToPgtype(""),
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ExtensionPageContext, len(rows))
	for i, row := range rows {
		result[i] = extensionPageContextFromSqlc(row)
	}
	if scope == "query" && query != "" && phraseID != nil && len(result) < int(limit) {
		// Keep phrase-linked captures primary; exact-query captures fill gaps when WB pages were captured before phrase resolution.
		extra, extraErr := s.queries.ListExtensionPageContextsFiltered(ctx, sqlcgen.ListExtensionPageContextsFilteredParams{
			WorkspaceID:      uuidToPgtype(workspaceID),
			Limit:            limit - int32(len(result)),
			Offset:           0,
			PageTypeFilter:   textToPgtype(""),
			CampaignIDFilter: uuidToPgtypePtr(nil),
			PhraseIDFilter:   uuidToPgtypePtr(nil),
			ProductIDFilter:  uuidToPgtypePtr(nil),
			QueryFilter:      textToPgtype(query),
			RegionFilter:     textToPgtype(""),
		})
		if extraErr != nil {
			return nil, extraErr
		}
		for _, row := range extra {
			result = append(result, extensionPageContextFromSqlc(row))
		}
	}
	return limitPageContexts(result, limit), nil
}

func (s *ExtensionService) listEvidenceDebugNetworkCaptures(ctx context.Context, workspaceID uuid.UUID, scope string, campaignID, productID, phraseID *uuid.UUID, query string, limit int32) ([]domain.ExtensionNetworkCapture, error) {
	fetchLimit := limit
	if scope == "query" && phraseID == nil && query != "" {
		fetchLimit = limit * 10
	}
	rows, err := s.queries.ListExtensionNetworkCapturesFiltered(ctx, sqlcgen.ListExtensionNetworkCapturesFilteredParams{
		WorkspaceID:       uuidToPgtype(workspaceID),
		Limit:             fetchLimit,
		Offset:            0,
		PageTypeFilter:    textToPgtype(""),
		EndpointKeyFilter: textToPgtype(""),
		CampaignIDFilter:  uuidToPgtypePtr(campaignID),
		PhraseIDFilter:    uuidToPgtypePtr(phraseID),
		ProductIDFilter:   uuidToPgtypePtr(productID),
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ExtensionNetworkCapture, 0, len(rows))
	for _, row := range rows {
		item := extensionNetworkCaptureFromSqlc(row)
		if scope == "query" && phraseID == nil && query != "" && !stringPtrEqual(item.Query, query) {
			continue
		}
		result = append(result, item)
	}
	return limitNetworkCaptures(result, limit), nil
}

func (s *ExtensionService) listEvidenceDebugDOMRowSnapshots(ctx context.Context, workspaceID uuid.UUID, scope string, campaignID, productID, phraseID *uuid.UUID, query string, limit int32) ([]domain.ExtensionDOMRowSnapshot, error) {
	fetchLimit := limit
	if scope == "query" && phraseID == nil && query != "" {
		fetchLimit = limit * 10
	}
	rows, err := s.queries.ListExtensionDOMRowSnapshotsFiltered(ctx, sqlcgen.ListExtensionDOMRowSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            fetchLimit,
		Offset:           0,
		PageTypeFilter:   textToPgtype(""),
		TableRoleFilter:  textToPgtype(""),
		CampaignIDFilter: uuidToPgtypePtr(campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(phraseID),
		ProductIDFilter:  uuidToPgtypePtr(productID),
		QueryFilter:      textToPgtype(query),
		RegionFilter:     textToPgtype(""),
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ExtensionDOMRowSnapshot, 0, len(rows))
	for _, row := range rows {
		item := extensionDOMRowSnapshotFromSqlc(row)
		if scope == "query" && phraseID == nil && query != "" && !stringPtrEqual(item.Query, query) {
			continue
		}
		result = append(result, item)
	}
	return limitDOMRowSnapshots(result, limit), nil
}

func (s *ExtensionService) listEvidenceDebugBidSnapshots(ctx context.Context, workspaceID uuid.UUID, campaignID, phraseID *uuid.UUID, query string, limit int32) ([]domain.ExtensionBidSnapshot, error) {
	rows, err := s.queries.ListExtensionBidSnapshotsFiltered(ctx, sqlcgen.ListExtensionBidSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(phraseID),
		QueryFilter:      textToPgtype(query),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ExtensionBidSnapshot, len(rows))
	for i, row := range rows {
		result[i] = extensionBidSnapshotFromSqlc(row)
	}
	return result, nil
}

func (s *ExtensionService) listEvidenceDebugPositionSnapshots(ctx context.Context, workspaceID uuid.UUID, campaignID, productID, phraseID *uuid.UUID, query string, limit int32) ([]domain.ExtensionPositionSnapshot, error) {
	rows, err := s.queries.ListExtensionPositionSnapshotsFiltered(ctx, sqlcgen.ListExtensionPositionSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(phraseID),
		ProductIDFilter:  uuidToPgtypePtr(productID),
		QueryFilter:      textToPgtype(query),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ExtensionPositionSnapshot, len(rows))
	for i, row := range rows {
		result[i] = extensionPositionSnapshotFromSqlc(row)
	}
	return result, nil
}

func (s *ExtensionService) listEvidenceDebugUISignals(ctx context.Context, workspaceID uuid.UUID, campaignID, productID, phraseID *uuid.UUID, query string, limit int32) ([]domain.ExtensionUISignal, error) {
	rows, err := s.queries.ListExtensionUISignalsFiltered(ctx, sqlcgen.ListExtensionUISignalsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(phraseID),
		ProductIDFilter:  uuidToPgtypePtr(productID),
		QueryFilter:      textToPgtype(query),
		RegionFilter:     textToPgtype(""),
		SignalTypeFilter: textToPgtype(""),
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ExtensionUISignal, len(rows))
	for i, row := range rows {
		result[i] = extensionUISignalFromSqlc(row)
	}
	return result, nil
}

func latestEvidenceDebugTime(
	pageContexts []domain.ExtensionPageContext,
	networkCaptures []domain.ExtensionNetworkCapture,
	domRows []domain.ExtensionDOMRowSnapshot,
	bids []domain.ExtensionBidSnapshot,
	positions []domain.ExtensionPositionSnapshot,
	signals []domain.ExtensionUISignal,
) *time.Time {
	var latest *time.Time
	for _, item := range pageContexts {
		latest = latestTime(latest, item.CapturedAt)
	}
	for _, item := range networkCaptures {
		latest = latestTime(latest, item.CapturedAt)
	}
	for _, item := range domRows {
		latest = latestTime(latest, item.CapturedAt)
	}
	for _, item := range bids {
		latest = latestTime(latest, item.CapturedAt)
	}
	for _, item := range positions {
		latest = latestTime(latest, item.CapturedAt)
	}
	for _, item := range signals {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func latestPageContextTime(items []domain.ExtensionPageContext) *time.Time {
	var latest *time.Time
	for _, item := range items {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func latestNetworkCaptureTime(items []domain.ExtensionNetworkCapture) *time.Time {
	var latest *time.Time
	for _, item := range items {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func latestDOMRowSnapshotTime(items []domain.ExtensionDOMRowSnapshot) *time.Time {
	var latest *time.Time
	for _, item := range items {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func latestBidSnapshotTime(items []domain.ExtensionBidSnapshot) *time.Time {
	var latest *time.Time
	for _, item := range items {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func latestPositionSnapshotTime(items []domain.ExtensionPositionSnapshot) *time.Time {
	var latest *time.Time
	for _, item := range items {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func latestUISignalTime(items []domain.ExtensionUISignal) *time.Time {
	var latest *time.Time
	for _, item := range items {
		latest = latestTime(latest, item.CapturedAt)
	}
	return latest
}

func stringPtrEqual(value *string, expected string) bool {
	if value == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(*value), strings.TrimSpace(expected))
}

func limitPageContexts(items []domain.ExtensionPageContext, limit int32) []domain.ExtensionPageContext {
	if int32(len(items)) <= limit {
		return items
	}
	return items[:limit]
}

func limitNetworkCaptures(items []domain.ExtensionNetworkCapture, limit int32) []domain.ExtensionNetworkCapture {
	if int32(len(items)) <= limit {
		return items
	}
	return items[:limit]
}

func limitDOMRowSnapshots(items []domain.ExtensionDOMRowSnapshot, limit int32) []domain.ExtensionDOMRowSnapshot {
	if int32(len(items)) <= limit {
		return items
	}
	return items[:limit]
}
