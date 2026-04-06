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
	DataStatus       ExtensionWidgetDataStatus
	Recommendations  []domain.Recommendation
}

type ExtensionProductWidget struct {
	Product         domain.Product
	Positions       []domain.Position
	LivePositions   []domain.ExtensionPositionSnapshot
	UISignals       []domain.ExtensionUISignal
	DataStatus      ExtensionWidgetDataStatus
	Recommendations []domain.Recommendation
}

type ExtensionCampaignWidget struct {
	Campaign        domain.Campaign
	Stats           []domain.CampaignStat
	Phrases         []domain.Phrase
	LiveBids        []domain.ExtensionBidSnapshot
	UISignals       []domain.ExtensionUISignal
	DataStatus      ExtensionWidgetDataStatus
	Recommendations []domain.Recommendation
}

type ExtensionWidgetDataStatus struct {
	Source             string     `json:"source"`
	CapturedAt         *time.Time `json:"captured_at,omitempty"`
	FreshnessState     string     `json:"freshness_state"`
	Confidence         float64    `json:"confidence"`
	Coverage           string     `json:"coverage"`
	ConfirmedInCabinet bool       `json:"confirmed_in_cabinet"`
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
		DataStatus:       buildExtensionWidgetDataStatus(liveBidSnapshot, livePositions, uiSignals),
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

	return &ExtensionProductWidget{
		Product:         product,
		Positions:       positions,
		LivePositions:   livePositions,
		UISignals:       uiSignals,
		DataStatus:      buildExtensionWidgetDataStatus(nil, livePositions, uiSignals),
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

	return &ExtensionCampaignWidget{
		Campaign:        campaign,
		Stats:           stats,
		Phrases:         phrases,
		LiveBids:        liveBids,
		UISignals:       uiSignals,
		DataStatus:      buildExtensionWidgetDataStatus(latestBid, nil, uiSignals),
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

func buildExtensionWidgetDataStatus(
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

	return ExtensionWidgetDataStatus{
		Source:             domain.SourceExtension,
		CapturedAt:         latest,
		FreshnessState:     extensionFreshnessState(latest),
		Confidence:         confidence,
		Coverage:           coverage,
		ConfirmedInCabinet: latest != nil,
	}
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
