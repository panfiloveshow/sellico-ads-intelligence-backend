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

type ExtensionSearchWidget struct {
	Query            string
	Phrase           *domain.Phrase
	Frequency        *int
	CompetitorsCount *int
	KnownPositions   []domain.Position
	BidEstimate      *domain.BidSnapshot
	LiveBidSnapshot  *domain.ExtensionBidSnapshot
	LivePositions    []domain.ExtensionPositionSnapshot
	UISignals        []domain.ExtensionUISignal
	PrimaryInsight   ExtensionWidgetPrimaryInsight
	DataStatus       ExtensionWidgetDataStatus
	Recommendations  []domain.Recommendation
}

type ExtensionProductWidget struct {
	Product         domain.Product
	Positions       []domain.Position
	LivePositions   []domain.ExtensionPositionSnapshot
	UISignals       []domain.ExtensionUISignal
	PrimaryInsight  ExtensionWidgetPrimaryInsight
	DataStatus      ExtensionWidgetDataStatus
	Recommendations []domain.Recommendation
}

type ExtensionCampaignWidget struct {
	Campaign        domain.Campaign
	Stats           []domain.CampaignStat
	Phrases         []domain.Phrase
	LiveBids        []domain.ExtensionBidSnapshot
	UISignals       []domain.ExtensionUISignal
	PrimaryInsight  ExtensionWidgetPrimaryInsight
	DataStatus      ExtensionWidgetDataStatus
	Recommendations []domain.Recommendation
}

type ExtensionWidgetPrimaryInsight struct {
	Title      string                 `json:"title"`
	Message    string                 `json:"message"`
	Severity   string                 `json:"severity"`
	Source     string                 `json:"source"`
	Evidence   []string               `json:"evidence,omitempty"`
	NextAction *ExtensionWidgetAction `json:"next_action,omitempty"`
}

type ExtensionWidgetDataStatus struct {
	Source             string                        `json:"source"`
	CapturedAt         *time.Time                    `json:"captured_at,omitempty"`
	FreshnessState     string                        `json:"freshness_state"`
	Confidence         float64                       `json:"confidence"`
	Coverage           string                        `json:"coverage"`
	ConfirmedInCabinet bool                          `json:"confirmed_in_cabinet"`
	EvidenceCounts     ExtensionWidgetEvidenceCounts `json:"evidence_counts"`
	Issues             []ExtensionWidgetIssue        `json:"issues,omitempty"`
	NextActions        []ExtensionWidgetAction       `json:"next_actions,omitempty"`
}

type ExtensionWidgetEvidenceCounts struct {
	BidSnapshots      int `json:"bid_snapshots"`
	PositionSnapshots int `json:"position_snapshots"`
	UISignals         int `json:"ui_signals"`
}

type ExtensionWidgetIssue struct {
	Stage      string `json:"stage"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	ActionPath string `json:"action_path,omitempty"`
}

type ExtensionWidgetAction struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	ActionPath string `json:"action_path"`
	Tone       string `json:"tone,omitempty"`
}

type ExtensionEvidenceSummary struct {
	WorkspaceID        uuid.UUID
	GeneratedAt        time.Time
	LatestCapturedAt   *time.Time
	NetworkCaptures    int
	BidSnapshots       int
	PositionSnapshots  int
	UISignals          int
	EndpointCounts     map[string]int
	SeverityCounts     map[string]int
	FreshnessState     string
	Coverage           string
	ConfirmedInCabinet bool
	Issues             []ExtensionWidgetIssue
	NextActions        []ExtensionWidgetAction
}

func (s *ExtensionService) GetSearchWidget(ctx context.Context, workspaceID uuid.UUID, query string) (*ExtensionSearchWidget, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperror.New(apperror.ErrValidation, "query is required")
	}

	phrase, err := s.findPhraseByKeyword(ctx, workspaceID, query)
	if err != nil {
		return nil, err
	}

	positionsRows, err := s.queries.ListPositionsFiltered(ctx, sqlcgen.ListPositionsFilteredParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		QueryFilter:     textToPgtype(query),
		RegionFilter:    textToPgtype(""),
		DateFrom:        timePtrToPgtype(nil),
		DateTo:          timePtrToPgtype(nil),
		ProductIDFilter: uuidToPgtypePtr(nil),
		Limit:           10,
		Offset:          0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load known positions")
	}
	knownPositions := make([]domain.Position, len(positionsRows))
	for i, row := range positionsRows {
		knownPositions[i] = positionFromSqlc(row)
	}

	snapshots, err := s.queries.ListSERPSnapshotsFiltered(ctx, sqlcgen.ListSERPSnapshotsFilteredParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		QueryFilter:  textToPgtype(query),
		RegionFilter: textToPgtype(""),
		DateFrom:     timePtrToPgtype(nil),
		DateTo:       timePtrToPgtype(nil),
		Limit:        1,
		Offset:       0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load serp snapshots")
	}

	var competitorsCount *int
	if len(snapshots) > 0 {
		value := int(snapshots[0].TotalResults)
		competitorsCount = &value
	}

	var bidEstimate *domain.BidSnapshot
	recommendations := make([]domain.Recommendation, 0)
	livePositions := make([]domain.ExtensionPositionSnapshot, 0)
	uiSignals := make([]domain.ExtensionUISignal, 0)
	var liveBidSnapshot *domain.ExtensionBidSnapshot

	if phrase != nil {
		if estimateRow, estimateErr := s.queries.GetLatestBidSnapshot(ctx, uuidToPgtype(phrase.ID)); estimateErr == nil {
			estimate := bidSnapshotFromSqlc(estimateRow)
			bidEstimate = &estimate
		} else if !errors.Is(estimateErr, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrInternal, "failed to load bid estimate")
		}

		recommendationRows, recommendationErr := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
			WorkspaceID:      uuidToPgtype(workspaceID),
			CampaignIDFilter: uuidToPgtypePtr(nil),
			PhraseIDFilter:   uuidToPgtypePtr(&phrase.ID),
			ProductIDFilter:  uuidToPgtypePtr(nil),
			TypeFilter:       textToPgtype(""),
			SeverityFilter:   textToPgtype(""),
			StatusFilter:     textToPgtype(""),
			Limit:            5,
			Offset:           0,
		})
		if recommendationErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to load recommendations")
		}

		recommendations = make([]domain.Recommendation, len(recommendationRows))
		for i, row := range recommendationRows {
			recommendations[i] = recommendationFromSqlc(row)
		}
	}

	extensionQuery := query
	var phraseIDFilter *uuid.UUID
	if phrase != nil {
		extensionQuery = phrase.Keyword
		phraseIDFilter = &phrase.ID
	}
	extensionPositionRows, err := s.queries.ListExtensionPositionSnapshotsFiltered(ctx, sqlcgen.ListExtensionPositionSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            10,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(phraseIDFilter),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(extensionQuery),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load live positions")
	}
	livePositions = make([]domain.ExtensionPositionSnapshot, len(extensionPositionRows))
	for i, row := range extensionPositionRows {
		livePositions[i] = extensionPositionSnapshotFromSqlc(row)
	}

	extensionBidRows, err := s.queries.ListExtensionBidSnapshotsFiltered(ctx, sqlcgen.ListExtensionBidSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            1,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(phraseIDFilter),
		QueryFilter:      textToPgtype(extensionQuery),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load live bids")
	}
	if len(extensionBidRows) > 0 {
		value := extensionBidSnapshotFromSqlc(extensionBidRows[0])
		liveBidSnapshot = &value
	}

	uiSignalRows, err := s.queries.ListExtensionUISignalsFiltered(ctx, sqlcgen.ListExtensionUISignalsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            5,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(phraseIDFilter),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(extensionQuery),
		RegionFilter:     textToPgtype(""),
		SignalTypeFilter: textToPgtype(""),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load ui signals")
	}
	uiSignals = make([]domain.ExtensionUISignal, len(uiSignalRows))
	for i, row := range uiSignalRows {
		uiSignals[i] = extensionUISignalFromSqlc(row)
	}

	var frequency *int
	if phrase != nil {
		frequency = phrase.Count
	}

	dataStatus := buildExtensionWidgetDataStatus("search", liveBidSnapshot, livePositions, uiSignals)
	return &ExtensionSearchWidget{
		Query:            query,
		Phrase:           phrase,
		Frequency:        frequency,
		CompetitorsCount: competitorsCount,
		KnownPositions:   knownPositions,
		BidEstimate:      bidEstimate,
		LiveBidSnapshot:  liveBidSnapshot,
		LivePositions:    livePositions,
		UISignals:        uiSignals,
		PrimaryInsight:   buildSearchWidgetPrimaryInsight(query, phrase, knownPositions, liveBidSnapshot, livePositions, recommendations, dataStatus),
		DataStatus:       dataStatus,
		Recommendations:  recommendations,
	}, nil
}

func (s *ExtensionService) GetProductWidget(ctx context.Context, workspaceID, productID uuid.UUID) (*ExtensionProductWidget, error) {
	row, err := s.queries.GetProductByID(ctx, uuidToPgtype(productID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get product")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}

	product := productFromSqlc(row)

	positionRows, err := s.queries.ListPositionsByProduct(ctx, sqlcgen.ListPositionsByProductParams{
		ProductID: uuidToPgtype(productID),
		Limit:     10,
		Offset:    0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product positions")
	}
	positions := make([]domain.Position, len(positionRows))
	for i, positionRow := range positionRows {
		positions[i] = positionFromSqlc(positionRow)
	}

	recommendationRows, err := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(&productID),
		TypeFilter:       textToPgtype(""),
		SeverityFilter:   textToPgtype(""),
		StatusFilter:     textToPgtype(""),
		Limit:            10,
		Offset:           0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product recommendations")
	}
	recommendations := make([]domain.Recommendation, len(recommendationRows))
	for i, recommendationRow := range recommendationRows {
		recommendations[i] = recommendationFromSqlc(recommendationRow)
	}

	extensionPositionRows, err := s.queries.ListExtensionPositionSnapshotsFiltered(ctx, sqlcgen.ListExtensionPositionSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            10,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(&productID),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product live positions")
	}
	livePositions := make([]domain.ExtensionPositionSnapshot, len(extensionPositionRows))
	for i, row := range extensionPositionRows {
		livePositions[i] = extensionPositionSnapshotFromSqlc(row)
	}

	uiSignalRows, err := s.queries.ListExtensionUISignalsFiltered(ctx, sqlcgen.ListExtensionUISignalsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            5,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(&productID),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		SignalTypeFilter: textToPgtype(""),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product ui signals")
	}
	uiSignals := make([]domain.ExtensionUISignal, len(uiSignalRows))
	for i, row := range uiSignalRows {
		uiSignals[i] = extensionUISignalFromSqlc(row)
	}

	dataStatus := buildExtensionWidgetDataStatus("product", nil, livePositions, uiSignals)
	return &ExtensionProductWidget{
		Product:         product,
		Positions:       positions,
		LivePositions:   livePositions,
		UISignals:       uiSignals,
		PrimaryInsight:  buildProductWidgetPrimaryInsight(product, positions, livePositions, recommendations, dataStatus),
		DataStatus:      dataStatus,
		Recommendations: recommendations,
	}, nil
}

func (s *ExtensionService) GetProductWidgetByWBProductID(ctx context.Context, workspaceID uuid.UUID, wbProductID int64) (*ExtensionProductWidget, error) {
	row, err := s.queries.GetProductByWBProductID(ctx, sqlcgen.GetProductByWBProductIDParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		WbProductID: wbProductID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get product by wb_product_id")
	}
	return s.GetProductWidget(ctx, workspaceID, uuidFromPgtype(row.ID))
}

func (s *ExtensionService) GetCampaignWidget(ctx context.Context, workspaceID, campaignID uuid.UUID) (*ExtensionCampaignWidget, error) {
	row, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(campaignID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get campaign")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	campaign := campaignFromSqlc(row)
	dateTo := time.Now().UTC()
	dateFrom := dateTo.AddDate(0, 0, -30)

	statsRows, err := s.queries.GetCampaignStatsByDateRange(ctx, sqlcgen.GetCampaignStatsByDateRangeParams{
		CampaignID: uuidToPgtype(campaignID),
		Date:       pgDate(dateFrom),
		Date_2:     pgDate(dateTo),
		Limit:      30,
		Offset:     0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign stats")
	}
	stats := make([]domain.CampaignStat, len(statsRows))
	for i, statsRow := range statsRows {
		stats[i] = campaignStatFromSqlc(statsRow)
	}

	phraseRows, err := s.queries.ListPhrasesByCampaign(ctx, sqlcgen.ListPhrasesByCampaignParams{
		CampaignID: uuidToPgtype(campaignID),
		Limit:      10,
		Offset:     0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign phrases")
	}
	phrases := make([]domain.Phrase, len(phraseRows))
	for i, phraseRow := range phraseRows {
		phrases[i] = phraseFromSqlc(phraseRow)
	}

	recommendationRows, err := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		CampaignIDFilter: uuidToPgtypePtr(&campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		TypeFilter:       textToPgtype(""),
		SeverityFilter:   textToPgtype(""),
		StatusFilter:     textToPgtype(""),
		Limit:            10,
		Offset:           0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign recommendations")
	}
	recommendations := make([]domain.Recommendation, len(recommendationRows))
	for i, recommendationRow := range recommendationRows {
		recommendations[i] = recommendationFromSqlc(recommendationRow)
	}

	liveBidRows, err := s.queries.ListExtensionBidSnapshotsFiltered(ctx, sqlcgen.ListExtensionBidSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            10,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(&campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign live bids")
	}
	liveBids := make([]domain.ExtensionBidSnapshot, len(liveBidRows))
	for i, row := range liveBidRows {
		liveBids[i] = extensionBidSnapshotFromSqlc(row)
	}

	uiSignalRows, err := s.queries.ListExtensionUISignalsFiltered(ctx, sqlcgen.ListExtensionUISignalsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            5,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(&campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		SignalTypeFilter: textToPgtype(""),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign ui signals")
	}
	uiSignals := make([]domain.ExtensionUISignal, len(uiSignalRows))
	for i, row := range uiSignalRows {
		uiSignals[i] = extensionUISignalFromSqlc(row)
	}

	var latestBid *domain.ExtensionBidSnapshot
	if len(liveBids) > 0 {
		latestBid = &liveBids[0]
	}

	dataStatus := buildExtensionWidgetDataStatus("campaign", latestBid, nil, uiSignals)
	return &ExtensionCampaignWidget{
		Campaign:        campaign,
		Stats:           stats,
		Phrases:         phrases,
		LiveBids:        liveBids,
		UISignals:       uiSignals,
		PrimaryInsight:  buildCampaignWidgetPrimaryInsight(stats, latestBid, recommendations, dataStatus),
		DataStatus:      dataStatus,
		Recommendations: recommendations,
	}, nil
}

func (s *ExtensionService) GetCampaignWidgetByWBCampaignID(ctx context.Context, workspaceID uuid.UUID, wbCampaignID int64) (*ExtensionCampaignWidget, error) {
	row, err := s.queries.GetCampaignByWBCampaignID(ctx, sqlcgen.GetCampaignByWBCampaignIDParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		WbCampaignID: wbCampaignID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get campaign by wb_campaign_id")
	}
	return s.GetCampaignWidget(ctx, workspaceID, uuidFromPgtype(row.ID))
}

func (s *ExtensionService) GetEvidenceSummary(ctx context.Context, workspaceID uuid.UUID) (*ExtensionEvidenceSummary, error) {
	const limit int32 = 500
	networkRows, err := s.queries.ListExtensionNetworkCapturesFiltered(ctx, sqlcgen.ListExtensionNetworkCapturesFilteredParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension network captures")
	}
	bidRows, err := s.queries.ListExtensionBidSnapshotsFiltered(ctx, sqlcgen.ListExtensionBidSnapshotsFilteredParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension bid snapshots")
	}
	positionRows, err := s.queries.ListExtensionPositionSnapshotsFiltered(ctx, sqlcgen.ListExtensionPositionSnapshotsFilteredParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension position snapshots")
	}
	uiRows, err := s.queries.ListExtensionUISignalsFiltered(ctx, sqlcgen.ListExtensionUISignalsFilteredParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load extension ui signals")
	}

	endpointCounts := make(map[string]int)
	var latest *time.Time
	for _, row := range networkRows {
		endpointCounts[row.EndpointKey]++
		latest = latestTime(latest, row.CapturedAt.Time)
	}
	var latestBid *domain.ExtensionBidSnapshot
	for i, row := range bidRows {
		latest = latestTime(latest, row.CapturedAt.Time)
		if i == 0 {
			item := extensionBidSnapshotFromSqlc(row)
			latestBid = &item
		}
	}
	positions := make([]domain.ExtensionPositionSnapshot, len(positionRows))
	for i, row := range positionRows {
		latest = latestTime(latest, row.CapturedAt.Time)
		positions[i] = extensionPositionSnapshotFromSqlc(row)
	}
	uiSignals := make([]domain.ExtensionUISignal, len(uiRows))
	severityCounts := make(map[string]int)
	for i, row := range uiRows {
		latest = latestTime(latest, row.CapturedAt.Time)
		severityCounts[row.Severity]++
		uiSignals[i] = extensionUISignalFromSqlc(row)
	}

	status := buildExtensionWidgetDataStatus("workspace", latestBid, positions, uiSignals)
	issues := make([]ExtensionWidgetIssue, 0, len(status.Issues)+1)
	issues = append(issues, status.Issues...)
	if len(networkRows) > 0 && len(bidRows)+len(positionRows)+len(uiRows) == 0 {
		issues = append(issues, ExtensionWidgetIssue{
			Stage:      "normalization",
			Severity:   "warning",
			Message:    "Расширение ловит сетевые ответы WB, но typed-сигналы пока не появились. Проверьте allowlist endpoints и normalizers.",
			ActionPath: "refresh",
		})
	}

	return &ExtensionEvidenceSummary{
		WorkspaceID:        workspaceID,
		GeneratedAt:        time.Now().UTC(),
		LatestCapturedAt:   latest,
		NetworkCaptures:    len(networkRows),
		BidSnapshots:       len(bidRows),
		PositionSnapshots:  len(positionRows),
		UISignals:          len(uiRows),
		EndpointCounts:     endpointCounts,
		SeverityCounts:     severityCounts,
		FreshnessState:     extensionFreshnessState(latest),
		Coverage:           status.Coverage,
		ConfirmedInCabinet: latest != nil,
		Issues:             issues,
		NextActions:        status.NextActions,
	}, nil
}

func buildExtensionWidgetDataStatus(
	scope string,
	liveBid *domain.ExtensionBidSnapshot,
	livePositions []domain.ExtensionPositionSnapshot,
	uiSignals []domain.ExtensionUISignal,
) ExtensionWidgetDataStatus {
	var latest *time.Time
	totalConfidence := 0.0
	sources := 0

	if liveBid != nil {
		value := liveBid.CapturedAt
		latest = &value
		totalConfidence += liveBid.Confidence
		sources++
	}
	for _, item := range livePositions {
		if latest == nil || item.CapturedAt.After(*latest) {
			value := item.CapturedAt
			latest = &value
		}
		totalConfidence += item.Confidence
		sources++
	}
	for _, item := range uiSignals {
		if latest == nil || item.CapturedAt.After(*latest) {
			value := item.CapturedAt
			latest = &value
		}
		totalConfidence += item.Confidence
		sources++
	}

	coverage := "none"
	switch presentSources(liveBid, livePositions, uiSignals) {
	case 0:
		coverage = "none"
	case 1:
		coverage = "partial"
	default:
		coverage = "full"
	}

	confidence := 0.0
	if sources > 0 {
		confidence = totalConfidence / float64(sources)
	}

	freshnessState := extensionFreshnessState(latest)
	issues, nextActions := extensionWidgetIssuesAndActions(scope, liveBid, livePositions, uiSignals, freshnessState)

	return ExtensionWidgetDataStatus{
		Source:             domain.SourceExtension,
		CapturedAt:         latest,
		FreshnessState:     freshnessState,
		Confidence:         confidence,
		Coverage:           coverage,
		ConfirmedInCabinet: latest != nil,
		EvidenceCounts: ExtensionWidgetEvidenceCounts{
			BidSnapshots:      boolToInt(liveBid != nil),
			PositionSnapshots: len(livePositions),
			UISignals:         len(uiSignals),
		},
		Issues:      issues,
		NextActions: nextActions,
	}
}

func buildCampaignWidgetPrimaryInsight(
	stats []domain.CampaignStat,
	liveBid *domain.ExtensionBidSnapshot,
	recommendations []domain.Recommendation,
	status ExtensionWidgetDataStatus,
) ExtensionWidgetPrimaryInsight {
	if rec := topWidgetRecommendation(recommendations); rec != nil {
		return primaryInsightFromRecommendation(*rec)
	}

	total := aggregateCampaignWidgetStats(stats)
	if total.hasBusinessEvidence() {
		evidence := []string{"wb_api_campaign_stats_30d"}
		switch {
		case total.Spend > 0 && total.Orders == 0:
			return ExtensionWidgetPrimaryInsight{
				Title:      "Есть расход без подтвержденных заказов",
				Message:    "WB API вернул расход по кампании за период, но заказы в этих строках не подтверждены. Перед повышением ставки проверьте кластеры и карточку.",
				Severity:   domain.SeverityHigh,
				Source:     domain.SourceAPI,
				Evidence:   evidence,
				NextAction: openSellicoWidgetAction(),
			}
		case total.Revenue > 0:
			return ExtensionWidgetPrimaryInsight{
				Title:      "Кампания имеет подтвержденную статистику WB",
				Message:    "Покажите менеджеру расход, выручку, заказы и ДРР из официальной статистики; решение по ставке принимайте с учетом маржи и остатков.",
				Severity:   domain.SeverityMedium,
				Source:     domain.SourceAPI,
				Evidence:   evidence,
				NextAction: openSellicoWidgetAction(),
			}
		default:
			return ExtensionWidgetPrimaryInsight{
				Title:      "Есть рекламная активность, но бизнес-результат неполный",
				Message:    "WB API вернул показы/клики/расход, но выручка или заказы пока отсутствуют. Это состояние требует осторожного решения, а не автоповышения.",
				Severity:   domain.SeverityMedium,
				Source:     domain.SourceAPI,
				Evidence:   evidence,
				NextAction: refreshWidgetAction(),
			}
		}
	}

	if liveBid != nil {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Ставка подтверждена в кабинете WB",
			Message:    widgetBidMessage(liveBid),
			Severity:   domain.SeverityLow,
			Source:     domain.SourceExtension,
			Evidence:   []string{"extension_bid_snapshot"},
			NextAction: refreshWidgetAction(),
		}
	}

	return primaryInsightFromDataStatus("Нет подтвержденной статистики кампании", "Откройте кампанию WB, дождитесь загрузки таблицы ставок/статистики и обновите контекст Sellico.", status)
}

func buildProductWidgetPrimaryInsight(
	product domain.Product,
	positions []domain.Position,
	livePositions []domain.ExtensionPositionSnapshot,
	recommendations []domain.Recommendation,
	status ExtensionWidgetDataStatus,
) ExtensionWidgetPrimaryInsight {
	if rec := topWidgetRecommendation(recommendations); rec != nil {
		return primaryInsightFromRecommendation(*rec)
	}

	if len(livePositions) > 0 {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Позиция товара подтверждена кабинетом WB",
			Message:    "Расширение видело товар на реальной странице WB. Используйте этот live-снимок как evidence, а не как замену официальной статистики.",
			Severity:   domain.SeverityLow,
			Source:     domain.SourceExtension,
			Evidence:   []string{"extension_position_snapshot"},
			NextAction: refreshWidgetAction(),
		}
	}

	if len(positions) > 0 {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Есть история позиций товара",
			Message:    "Sellico нашел сохраненные позиции товара по запросам. Для live-подтверждения откройте нужную выдачу WB и сохраните текущую позицию.",
			Severity:   domain.SeverityLow,
			Source:     positions[0].Source,
			Evidence:   []string{"position_history", product.ID.String()},
			NextAction: refreshWidgetAction(),
		}
	}

	return primaryInsightFromDataStatus("Нет live-сигналов по товару", "Товар синхронизирован, но расширение пока не видело его позицию или предупреждения в кабинете WB.", status)
}

func buildSearchWidgetPrimaryInsight(
	query string,
	phrase *domain.Phrase,
	knownPositions []domain.Position,
	liveBid *domain.ExtensionBidSnapshot,
	livePositions []domain.ExtensionPositionSnapshot,
	recommendations []domain.Recommendation,
	status ExtensionWidgetDataStatus,
) ExtensionWidgetPrimaryInsight {
	if rec := topWidgetRecommendation(recommendations); rec != nil {
		return primaryInsightFromRecommendation(*rec)
	}

	if liveBid != nil {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Ставка по запросу подтверждена кабинетом WB",
			Message:    widgetBidMessage(liveBid),
			Severity:   domain.SeverityLow,
			Source:     domain.SourceExtension,
			Evidence:   []string{"extension_bid_snapshot", strings.TrimSpace(query)},
			NextAction: refreshWidgetAction(),
		}
	}

	if len(livePositions) > 0 {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Позиция по запросу подтверждена кабинетом WB",
			Message:    "Live-снимок показывает видимую позицию товара по запросу. Используйте его как evidence рядом с API-статистикой, не как расчет продаж.",
			Severity:   domain.SeverityLow,
			Source:     domain.SourceExtension,
			Evidence:   []string{"extension_position_snapshot", strings.TrimSpace(query)},
			NextAction: refreshWidgetAction(),
		}
	}

	if phrase == nil {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Запрос еще не связан с кластером Sellico",
			Message:    "Расширение видит страницу WB, но в backend нет синхронизированного кластера для этого запроса. Нужна синхронизация рекламы или сохранение real evidence.",
			Severity:   domain.SeverityMedium,
			Source:     domain.SourceExtension,
			Evidence:   []string{strings.TrimSpace(query)},
			NextAction: refreshWidgetAction(),
		}
	}

	if len(knownPositions) > 0 {
		return ExtensionWidgetPrimaryInsight{
			Title:      "Есть сохраненная история позиций",
			Message:    "Sellico нашел позиции по этому запросу. Для решения по ставке добавьте live-снимок ставки или обновите данные WB API.",
			Severity:   domain.SeverityLow,
			Source:     knownPositions[0].Source,
			Evidence:   []string{"position_history", strings.TrimSpace(query)},
			NextAction: refreshWidgetAction(),
		}
	}

	return primaryInsightFromDataStatus("Нет подтвержденных live-сигналов по запросу", "Откройте таблицу ставок или выдачу WB по этому запросу и обновите контекст Sellico.", status)
}

type campaignWidgetStatsTotal struct {
	Impressions int64
	Clicks      int64
	Spend       int64
	Orders      int64
	Revenue     int64
}

func aggregateCampaignWidgetStats(stats []domain.CampaignStat) campaignWidgetStatsTotal {
	var total campaignWidgetStatsTotal
	for _, stat := range stats {
		total.Impressions += stat.Impressions
		total.Clicks += stat.Clicks
		total.Spend += stat.Spend
		if stat.Orders != nil {
			total.Orders += *stat.Orders
		}
		if stat.Revenue != nil {
			total.Revenue += *stat.Revenue
		}
	}
	return total
}

func (total campaignWidgetStatsTotal) hasBusinessEvidence() bool {
	return total.Impressions > 0 || total.Clicks > 0 || total.Spend > 0 || total.Orders > 0 || total.Revenue > 0
}

func topWidgetRecommendation(items []domain.Recommendation) *domain.Recommendation {
	var selected *domain.Recommendation
	selectedRank := -1
	for i := range items {
		rank := severityRank(items[i].Severity)
		if selected == nil || rank > selectedRank || (rank == selectedRank && items[i].CreatedAt.After(selected.CreatedAt)) {
			selected = &items[i]
			selectedRank = rank
		}
	}
	return selected
}

func primaryInsightFromRecommendation(item domain.Recommendation) ExtensionWidgetPrimaryInsight {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = strings.TrimSpace(item.Type)
	}
	if title == "" {
		title = "Активная рекомендация Sellico"
	}
	message := strings.TrimSpace(item.Description)
	if message == "" && item.NextAction != nil {
		message = strings.TrimSpace(*item.NextAction)
	}
	if message == "" {
		message = "Есть активная рекомендация Sellico по реальным данным workspace."
	}
	return ExtensionWidgetPrimaryInsight{
		Title:      title,
		Message:    message,
		Severity:   normalizedWidgetSeverity(item.Severity),
		Source:     domain.SourceDerived,
		Evidence:   []string{"active_recommendation", item.ID.String()},
		NextAction: openSellicoWidgetAction(),
	}
}

func primaryInsightFromDataStatus(title, fallbackMessage string, status ExtensionWidgetDataStatus) ExtensionWidgetPrimaryInsight {
	message := strings.TrimSpace(fallbackMessage)
	severity := domain.SeverityLow
	if len(status.Issues) > 0 {
		message = strings.TrimSpace(status.Issues[0].Message)
		severity = normalizedWidgetSeverity(status.Issues[0].Severity)
	}
	if message == "" {
		message = fallbackMessage
	}
	return ExtensionWidgetPrimaryInsight{
		Title:      title,
		Message:    message,
		Severity:   severity,
		Source:     status.Source,
		Evidence:   []string{"data_status:" + status.FreshnessState},
		NextAction: firstWidgetAction(status.NextActions),
	}
}

func widgetBidMessage(item *domain.ExtensionBidSnapshot) string {
	if item == nil {
		return "Live-снимок ставки отсутствует."
	}
	parts := make([]string, 0, 4)
	if item.VisibleBid != nil {
		parts = append(parts, "текущая ставка подтверждена")
	}
	if item.RecommendedBid != nil {
		parts = append(parts, "есть рекомендация WB")
	}
	if item.CompetitiveBid != nil {
		parts = append(parts, "есть конкурентная ставка")
	}
	if item.CPMMin != nil {
		parts = append(parts, "есть минимальная ставка")
	}
	if len(parts) == 0 {
		return "Расширение получило live-снимок ставки без числовых значений; обновите контекст перед решением."
	}
	return strings.Join(parts, ", ") + ". Перед автодействием сверяем это с API, лимитами, маржей и остатками."
}

func normalizedWidgetSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case domain.SeverityCritical:
		return domain.SeverityCritical
	case domain.SeverityHigh:
		return domain.SeverityHigh
	case domain.SeverityMedium:
		return domain.SeverityMedium
	default:
		return domain.SeverityLow
	}
}

func firstWidgetAction(actions []ExtensionWidgetAction) *ExtensionWidgetAction {
	if len(actions) == 0 {
		return refreshWidgetAction()
	}
	action := actions[0]
	return &action
}

func refreshWidgetAction() *ExtensionWidgetAction {
	return &ExtensionWidgetAction{ID: "refresh", Label: "Обновить контекст", ActionPath: "refresh", Tone: "primary"}
}

func openSellicoWidgetAction() *ExtensionWidgetAction {
	return &ExtensionWidgetAction{ID: "open-sellico-ads", Label: "Открыть Ads Intelligence", ActionPath: "open-sellico-ads", Tone: "primary"}
}

func latestTime(current *time.Time, candidate time.Time) *time.Time {
	if current == nil || candidate.After(*current) {
		value := candidate
		return &value
	}
	return current
}

func extensionWidgetIssuesAndActions(
	scope string,
	liveBid *domain.ExtensionBidSnapshot,
	livePositions []domain.ExtensionPositionSnapshot,
	uiSignals []domain.ExtensionUISignal,
	freshnessState string,
) ([]ExtensionWidgetIssue, []ExtensionWidgetAction) {
	issues := make([]ExtensionWidgetIssue, 0, 4)
	actionsByID := make(map[string]ExtensionWidgetAction)
	addAction := func(action ExtensionWidgetAction) {
		if action.ID == "" {
			return
		}
		if _, exists := actionsByID[action.ID]; !exists {
			actionsByID[action.ID] = action
		}
	}
	addIssue := func(issue ExtensionWidgetIssue) {
		if issue.Message == "" {
			return
		}
		issues = append(issues, issue)
		switch issue.ActionPath {
		case "refresh":
			addAction(ExtensionWidgetAction{ID: "refresh", Label: "Обновить контекст", ActionPath: "refresh", Tone: "primary"})
		case "open-wb-promotion":
			addAction(ExtensionWidgetAction{ID: "open-wb-promotion", Label: "Открыть продвижение WB", ActionPath: "open-wb-promotion"})
		case "open-sellico-ads":
			addAction(ExtensionWidgetAction{ID: "open-sellico-ads", Label: "Открыть Ads Intelligence", ActionPath: "open-sellico-ads"})
		}
	}

	switch freshnessState {
	case "empty":
		addIssue(ExtensionWidgetIssue{
			Stage:      "extension_capture",
			Severity:   "info",
			Message:    "Sellico пока не получил подтвержденные live-сигналы с этой страницы WB.",
			ActionPath: "refresh",
		})
	case "aging":
		addIssue(ExtensionWidgetIssue{
			Stage:      "extension_capture",
			Severity:   "info",
			Message:    "Live-сигналы с этой страницы уже устаревают. Обновите контекст перед решением по ставкам.",
			ActionPath: "refresh",
		})
	case "stale":
		addIssue(ExtensionWidgetIssue{
			Stage:      "extension_capture",
			Severity:   "warning",
			Message:    "Live-сигналы старше суток. Обновите страницу WB и контекст Sellico.",
			ActionPath: "refresh",
		})
	}

	if latestSignal := mostImportantUISignal(uiSignals); latestSignal != nil {
		message := latestSignal.Title
		if latestSignal.Message != nil && strings.TrimSpace(*latestSignal.Message) != "" {
			message = message + ": " + truncateWidgetMessage(*latestSignal.Message, 180)
		}
		addIssue(ExtensionWidgetIssue{
			Stage:      "wb_page_signal",
			Severity:   latestSignal.Severity,
			Message:    message,
			ActionPath: "refresh",
		})
	}

	switch scope {
	case "campaign":
		if liveBid == nil {
			addIssue(ExtensionWidgetIssue{
				Stage:      "bid_visibility",
				Severity:   "info",
				Message:    "Текущие ставки кампании пока не подтверждены live-сигналами WB. Откройте таблицу ставок или кластеров и обновите контекст.",
				ActionPath: "refresh",
			})
		}
	case "search":
		if liveBid == nil {
			addIssue(ExtensionWidgetIssue{
				Stage:      "bid_visibility",
				Severity:   "info",
				Message:    "Sellico пока не видит ставку по этому запросу. Откройте таблицу ставок WB и обновите контекст.",
				ActionPath: "refresh",
			})
		}
		if len(livePositions) == 0 {
			addIssue(ExtensionWidgetIssue{
				Stage:      "position_visibility",
				Severity:   "info",
				Message:    "Позиция товара по запросу пока не подтверждена live-сигналом.",
				ActionPath: "refresh",
			})
		}
	case "product":
		if len(livePositions) == 0 {
			addIssue(ExtensionWidgetIssue{
				Stage:      "position_visibility",
				Severity:   "info",
				Message:    "Sellico пока не видел реальную позицию этого товара на странице WB.",
				ActionPath: "refresh",
			})
		}
	}

	if len(issues) == 0 {
		addAction(ExtensionWidgetAction{ID: "refresh", Label: "Обновить контекст", ActionPath: "refresh", Tone: "primary"})
	}

	actions := make([]ExtensionWidgetAction, 0, len(actionsByID))
	if action, ok := actionsByID["refresh"]; ok {
		actions = append(actions, action)
		delete(actionsByID, "refresh")
	}
	for _, action := range actionsByID {
		actions = append(actions, action)
	}
	return issues, actions
}

func mostImportantUISignal(items []domain.ExtensionUISignal) *domain.ExtensionUISignal {
	var selected *domain.ExtensionUISignal
	selectedRank := -1
	for i := range items {
		rank := severityRank(items[i].Severity)
		if selected == nil || rank > selectedRank || (rank == selectedRank && items[i].CapturedAt.After(selected.CapturedAt)) {
			selected = &items[i]
			selectedRank = rank
		}
	}
	return selected
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func truncateWidgetMessage(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func presentSources(
	liveBid *domain.ExtensionBidSnapshot,
	livePositions []domain.ExtensionPositionSnapshot,
	uiSignals []domain.ExtensionUISignal,
) int {
	count := 0
	if liveBid != nil {
		count++
	}
	if len(livePositions) > 0 {
		count++
	}
	if len(uiSignals) > 0 {
		count++
	}
	return count
}

func extensionFreshnessState(latest *time.Time) string {
	if latest == nil {
		return "empty"
	}
	age := time.Since(*latest)
	switch {
	case age <= 6*time.Hour:
		return "fresh"
	case age <= 24*time.Hour:
		return "aging"
	default:
		return "stale"
	}
}

func (s *ExtensionService) findPhraseByKeyword(ctx context.Context, workspaceID uuid.UUID, keyword string) (*domain.Phrase, error) {
	rows, err := s.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		CampaignIDFilter: uuidToPgtypePtr(nil),
		Limit:            500,
		Offset:           0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list phrases")
	}

	needle := strings.ToLower(strings.TrimSpace(keyword))
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Keyword), needle) {
			phrase := phraseFromSqlc(row)
			return &phrase, nil
		}
	}
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.Keyword), needle) {
			phrase := phraseFromSqlc(row)
			return &phrase, nil
		}
	}
	return nil, nil
}
