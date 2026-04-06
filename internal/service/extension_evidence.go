package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type workspaceExtensionEvidence struct {
	pageContexts []domain.ExtensionPageContext
	bids         []domain.ExtensionBidSnapshot
	positions    []domain.ExtensionPositionSnapshot
	signals      []domain.ExtensionUISignal

	// Pre-built indexes for O(1) entity lookup (audit fix: CRITICAL O(N²))
	pageByProduct    map[uuid.UUID][]int
	pageByPhrase     map[uuid.UUID][]int
	pageByCampaign   map[uuid.UUID][]int
	bidByPhrase      map[uuid.UUID][]int
	bidByCampaign    map[uuid.UUID][]int
	posByProduct     map[uuid.UUID][]int
	posByPhrase      map[uuid.UUID][]int
	posByCampaign    map[uuid.UUID][]int
	sigByProduct     map[uuid.UUID][]int
	sigByPhrase      map[uuid.UUID][]int
	sigByCampaign    map[uuid.UUID][]int
}

func loadWorkspaceExtensionEvidence(ctx context.Context, queries *sqlcgen.Queries, workspaceID uuid.UUID, limit int32) (*workspaceExtensionEvidence, error) {
	pageContextRows, err := queries.ListExtensionPageContextsFiltered(ctx, sqlcgen.ListExtensionPageContextsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		PageTypeFilter:   textToPgtype(""),
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
	})
	if err != nil {
		return nil, err
	}
	bidRows, err := queries.ListExtensionBidSnapshotsFiltered(ctx, sqlcgen.ListExtensionBidSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, err
	}
	positionRows, err := queries.ListExtensionPositionSnapshotsFiltered(ctx, sqlcgen.ListExtensionPositionSnapshotsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		DateFrom:         timePtrToPgtype(nil),
		DateTo:           timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, err
	}
	signalRows, err := queries.ListExtensionUISignalsFiltered(ctx, sqlcgen.ListExtensionUISignalsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           0,
		CampaignIDFilter: uuidToPgtypePtr(nil),
		PhraseIDFilter:   uuidToPgtypePtr(nil),
		ProductIDFilter:  uuidToPgtypePtr(nil),
		QueryFilter:      textToPgtype(""),
		RegionFilter:     textToPgtype(""),
		SignalTypeFilter: textToPgtype(""),
	})
	if err != nil {
		return nil, err
	}

	result := &workspaceExtensionEvidence{
		pageContexts: make([]domain.ExtensionPageContext, len(pageContextRows)),
		bids:         make([]domain.ExtensionBidSnapshot, len(bidRows)),
		positions:    make([]domain.ExtensionPositionSnapshot, len(positionRows)),
		signals:      make([]domain.ExtensionUISignal, len(signalRows)),
	}
	for i, row := range pageContextRows {
		result.pageContexts[i] = extensionPageContextFromSqlc(row)
	}
	for i, row := range bidRows {
		result.bids[i] = extensionBidSnapshotFromSqlc(row)
	}
	for i, row := range positionRows {
		result.positions[i] = extensionPositionSnapshotFromSqlc(row)
	}
	for i, row := range signalRows {
		result.signals[i] = extensionUISignalFromSqlc(row)
	}

	result.buildIndexes()
	return result, nil
}

// buildIndexes creates per-entity lookup maps for O(1) evidence matching.
func (e *workspaceExtensionEvidence) buildIndexes() {
	e.pageByProduct = make(map[uuid.UUID][]int)
	e.pageByPhrase = make(map[uuid.UUID][]int)
	e.pageByCampaign = make(map[uuid.UUID][]int)
	for i, item := range e.pageContexts {
		if item.ProductID != nil {
			e.pageByProduct[*item.ProductID] = append(e.pageByProduct[*item.ProductID], i)
		}
		if item.PhraseID != nil {
			e.pageByPhrase[*item.PhraseID] = append(e.pageByPhrase[*item.PhraseID], i)
		}
		if item.CampaignID != nil {
			e.pageByCampaign[*item.CampaignID] = append(e.pageByCampaign[*item.CampaignID], i)
		}
	}

	e.bidByPhrase = make(map[uuid.UUID][]int)
	e.bidByCampaign = make(map[uuid.UUID][]int)
	for i, item := range e.bids {
		if item.PhraseID != nil {
			e.bidByPhrase[*item.PhraseID] = append(e.bidByPhrase[*item.PhraseID], i)
		}
		if item.CampaignID != nil {
			e.bidByCampaign[*item.CampaignID] = append(e.bidByCampaign[*item.CampaignID], i)
		}
	}

	e.posByProduct = make(map[uuid.UUID][]int)
	e.posByPhrase = make(map[uuid.UUID][]int)
	e.posByCampaign = make(map[uuid.UUID][]int)
	for i, item := range e.positions {
		if item.ProductID != nil {
			e.posByProduct[*item.ProductID] = append(e.posByProduct[*item.ProductID], i)
		}
		if item.PhraseID != nil {
			e.posByPhrase[*item.PhraseID] = append(e.posByPhrase[*item.PhraseID], i)
		}
		if item.CampaignID != nil {
			e.posByCampaign[*item.CampaignID] = append(e.posByCampaign[*item.CampaignID], i)
		}
	}

	e.sigByProduct = make(map[uuid.UUID][]int)
	e.sigByPhrase = make(map[uuid.UUID][]int)
	e.sigByCampaign = make(map[uuid.UUID][]int)
	for i, item := range e.signals {
		if item.ProductID != nil {
			e.sigByProduct[*item.ProductID] = append(e.sigByProduct[*item.ProductID], i)
		}
		if item.PhraseID != nil {
			e.sigByPhrase[*item.PhraseID] = append(e.sigByPhrase[*item.PhraseID], i)
		}
		if item.CampaignID != nil {
			e.sigByCampaign[*item.CampaignID] = append(e.sigByCampaign[*item.CampaignID], i)
		}
	}
}

// productEvidenceIndexed uses pre-built indexes for O(1) product evidence lookup.
func (e *workspaceExtensionEvidence) productEvidenceIndexed(productID uuid.UUID) *domain.SourceEvidence {
	if e == nil {
		return backendOnlyEvidence(domain.SourceAPI, 0.75)
	}
	return e.buildEvidenceFromIndexes(domain.SourceAPI, 0.75,
		e.pageByProduct[productID],
		nil, // bids don't have product index
		e.posByProduct[productID],
		e.sigByProduct[productID],
	)
}

// campaignEvidenceIndexed uses pre-built indexes for O(1) campaign evidence lookup.
func (e *workspaceExtensionEvidence) campaignEvidenceIndexed(campaignID uuid.UUID) *domain.SourceEvidence {
	if e == nil {
		return backendOnlyEvidence(domain.SourceAPI, 0.75)
	}
	return e.buildEvidenceFromIndexes(domain.SourceAPI, 0.75,
		e.pageByCampaign[campaignID],
		e.bidByCampaign[campaignID],
		e.posByCampaign[campaignID],
		e.sigByCampaign[campaignID],
	)
}

// phraseEvidenceIndexed uses pre-built indexes for O(1) phrase evidence lookup.
func (e *workspaceExtensionEvidence) phraseEvidenceIndexed(phraseID uuid.UUID) *domain.SourceEvidence {
	if e == nil {
		return backendOnlyEvidence(domain.SourceAPI, 0.75)
	}
	return e.buildEvidenceFromIndexes(domain.SourceAPI, 0.75,
		e.pageByPhrase[phraseID],
		e.bidByPhrase[phraseID],
		e.posByPhrase[phraseID],
		e.sigByPhrase[phraseID],
	)
}

// buildEvidenceFromIndexes builds evidence from pre-indexed item positions.
func (e *workspaceExtensionEvidence) buildEvidenceFromIndexes(
	defaultSource string,
	defaultConfidence float64,
	pageIdxs, bidIdxs, posIdxs, sigIdxs []int,
) *domain.SourceEvidence {
	typePresence := 0
	latest := time.Time{}
	totalConfidence := 0.0
	confidenceSamples := 0

	if len(pageIdxs) > 0 {
		typePresence++
		for _, i := range pageIdxs {
			item := e.pageContexts[i]
			if item.CapturedAt.After(latest) {
				latest = item.CapturedAt
			}
			totalConfidence += 1
			confidenceSamples++
		}
	}

	if len(bidIdxs) > 0 {
		typePresence++
		for _, i := range bidIdxs {
			item := e.bids[i]
			if item.CapturedAt.After(latest) {
				latest = item.CapturedAt
			}
			totalConfidence += item.Confidence
			confidenceSamples++
		}
	}

	if len(posIdxs) > 0 {
		typePresence++
		for _, i := range posIdxs {
			item := e.positions[i]
			if item.CapturedAt.After(latest) {
				latest = item.CapturedAt
			}
			totalConfidence += item.Confidence
			confidenceSamples++
		}
	}

	if len(sigIdxs) > 0 {
		typePresence++
		for _, i := range sigIdxs {
			item := e.signals[i]
			if item.CapturedAt.After(latest) {
				latest = item.CapturedAt
			}
			totalConfidence += item.Confidence
			confidenceSamples++
		}
	}

	if typePresence == 0 {
		return backendOnlyEvidence(defaultSource, defaultConfidence)
	}

	capturedAt := latest
	source := domain.SourceExtension
	if defaultSource != domain.SourceExtension {
		source = "mixed"
	}
	confidence := 1.0
	if confidenceSamples > 0 {
		confidence = totalConfidence / float64(confidenceSamples)
	}

	return &domain.SourceEvidence{
		Source:             source,
		CapturedAt:         &capturedAt,
		FreshnessState:     freshnessState(capturedAt),
		Confidence:         confidence,
		Coverage:           evidenceCoverage(typePresence),
		ConfirmedInCabinet: true,
	}
}

func (e *workspaceExtensionEvidence) workspaceEvidence(defaultSource string) *domain.SourceEvidence {
	return e.buildEvidence(defaultSource, 0.75, func(any) bool { return true }, func(any) bool { return true }, func(any) bool { return true }, func(any) bool { return true })
}

func (e *workspaceExtensionEvidence) productEvidence(productID uuid.UUID, wbProductID int64) *domain.SourceEvidence {
	return e.buildEvidence(domain.SourceAPI, 0.75,
		func(item any) bool {
			return matchPageContextProduct(item.(domain.ExtensionPageContext), productID, wbProductID)
		},
		func(item any) bool {
			return matchBidProduct(item.(domain.ExtensionBidSnapshot), productID, wbProductID)
		},
		func(item any) bool {
			return matchPositionProduct(item.(domain.ExtensionPositionSnapshot), productID, wbProductID)
		},
		func(item any) bool {
			return matchSignalProduct(item.(domain.ExtensionUISignal), productID, wbProductID)
		},
	)
}

func (e *workspaceExtensionEvidence) campaignEvidence(campaignID uuid.UUID, wbCampaignID int64) *domain.SourceEvidence {
	return e.buildEvidence(domain.SourceAPI, 0.75,
		func(item any) bool {
			return matchPageContextCampaign(item.(domain.ExtensionPageContext), campaignID, wbCampaignID)
		},
		func(item any) bool {
			return matchBidCampaign(item.(domain.ExtensionBidSnapshot), campaignID, wbCampaignID)
		},
		func(item any) bool {
			return matchPositionCampaign(item.(domain.ExtensionPositionSnapshot), campaignID, wbCampaignID)
		},
		func(item any) bool {
			return matchSignalCampaign(item.(domain.ExtensionUISignal), campaignID, wbCampaignID)
		},
	)
}

func (e *workspaceExtensionEvidence) phraseEvidence(phraseID uuid.UUID, keyword string, campaignID uuid.UUID, wbCampaignID int64) *domain.SourceEvidence {
	return e.buildEvidence(domain.SourceAPI, 0.75,
		func(item any) bool {
			return matchPageContextPhrase(item.(domain.ExtensionPageContext), phraseID, keyword, campaignID, wbCampaignID)
		},
		func(item any) bool {
			return matchBidPhrase(item.(domain.ExtensionBidSnapshot), phraseID, keyword, campaignID, wbCampaignID)
		},
		func(item any) bool {
			return matchPositionPhrase(item.(domain.ExtensionPositionSnapshot), phraseID, keyword, campaignID, wbCampaignID)
		},
		func(item any) bool {
			return matchSignalPhrase(item.(domain.ExtensionUISignal), phraseID, keyword, campaignID, wbCampaignID)
		},
	)
}

func (e *workspaceExtensionEvidence) recommendationEvidence(rec domain.Recommendation) *domain.SourceEvidence {
	query := recommendationQuery(rec.SourceMetrics)
	var keyword string
	if query != nil {
		keyword = *query
	}
	switch {
	case rec.PhraseID != nil && rec.CampaignID != nil:
		return e.buildEvidence(domain.SourceDerived, rec.Confidence,
			func(item any) bool {
				return matchPageContextPhrase(item.(domain.ExtensionPageContext), *rec.PhraseID, keyword, *rec.CampaignID, 0)
			},
			func(item any) bool {
				return matchBidPhrase(item.(domain.ExtensionBidSnapshot), *rec.PhraseID, keyword, *rec.CampaignID, 0)
			},
			func(item any) bool {
				return matchPositionPhrase(item.(domain.ExtensionPositionSnapshot), *rec.PhraseID, keyword, *rec.CampaignID, 0)
			},
			func(item any) bool {
				return matchSignalPhrase(item.(domain.ExtensionUISignal), *rec.PhraseID, keyword, *rec.CampaignID, 0)
			},
		)
	case rec.PhraseID != nil:
		return e.buildEvidence(domain.SourceDerived, rec.Confidence,
			func(item any) bool {
				return matchPageContextPhrase(item.(domain.ExtensionPageContext), *rec.PhraseID, keyword, uuid.Nil, 0)
			},
			func(item any) bool {
				return matchBidPhrase(item.(domain.ExtensionBidSnapshot), *rec.PhraseID, keyword, uuid.Nil, 0)
			},
			func(item any) bool {
				return matchPositionPhrase(item.(domain.ExtensionPositionSnapshot), *rec.PhraseID, keyword, uuid.Nil, 0)
			},
			func(item any) bool {
				return matchSignalPhrase(item.(domain.ExtensionUISignal), *rec.PhraseID, keyword, uuid.Nil, 0)
			},
		)
	case rec.ProductID != nil:
		wbProductID := recommendationWBProductID(rec.SourceMetrics)
		return e.productEvidence(*rec.ProductID, wbProductID)
	case rec.CampaignID != nil:
		wbCampaignID := recommendationWBCampaignID(rec.SourceMetrics)
		return e.campaignEvidence(*rec.CampaignID, wbCampaignID)
	default:
		return e.workspaceEvidence(domain.SourceDerived)
	}
}

func (e *workspaceExtensionEvidence) buildEvidence(
	defaultSource string,
	defaultConfidence float64,
	pageMatch func(any) bool,
	bidMatch func(any) bool,
	positionMatch func(any) bool,
	signalMatch func(any) bool,
) *domain.SourceEvidence {
	if e == nil {
		return backendOnlyEvidence(defaultSource, defaultConfidence)
	}

	typePresence := 0
	latest := time.Time{}
	totalConfidence := 0.0
	confidenceSamples := 0

	hasPage := false
	for _, item := range e.pageContexts {
		if !pageMatch(item) {
			continue
		}
		hasPage = true
		if item.CapturedAt.After(latest) {
			latest = item.CapturedAt
		}
		totalConfidence += 1
		confidenceSamples++
	}
	if hasPage {
		typePresence++
	}

	hasBid := false
	for _, item := range e.bids {
		if !bidMatch(item) {
			continue
		}
		hasBid = true
		if item.CapturedAt.After(latest) {
			latest = item.CapturedAt
		}
		totalConfidence += item.Confidence
		confidenceSamples++
	}
	if hasBid {
		typePresence++
	}

	hasPosition := false
	for _, item := range e.positions {
		if !positionMatch(item) {
			continue
		}
		hasPosition = true
		if item.CapturedAt.After(latest) {
			latest = item.CapturedAt
		}
		totalConfidence += item.Confidence
		confidenceSamples++
	}
	if hasPosition {
		typePresence++
	}

	hasSignal := false
	for _, item := range e.signals {
		if !signalMatch(item) {
			continue
		}
		hasSignal = true
		if item.CapturedAt.After(latest) {
			latest = item.CapturedAt
		}
		totalConfidence += item.Confidence
		confidenceSamples++
	}
	if hasSignal {
		typePresence++
	}

	if typePresence == 0 {
		return backendOnlyEvidence(defaultSource, defaultConfidence)
	}

	capturedAt := latest
	source := domain.SourceExtension
	if defaultSource != domain.SourceExtension {
		source = "mixed"
	}
	confidence := 1.0
	if confidenceSamples > 0 {
		confidence = totalConfidence / float64(confidenceSamples)
	}

	return &domain.SourceEvidence{
		Source:             source,
		CapturedAt:         &capturedAt,
		FreshnessState:     freshnessState(capturedAt),
		Confidence:         confidence,
		Coverage:           evidenceCoverage(typePresence),
		ConfirmedInCabinet: true,
	}
}

func backendOnlyEvidence(source string, confidence float64) *domain.SourceEvidence {
	return &domain.SourceEvidence{
		Source:             source,
		FreshnessState:     "no_live_capture",
		Confidence:         confidence,
		Coverage:           "none",
		ConfirmedInCabinet: false,
	}
}

func evidenceCoverage(typePresence int) string {
	switch {
	case typePresence >= 4:
		return "high"
	case typePresence >= 2:
		return "medium"
	default:
		return "low"
	}
}

func normalizeEvidenceQuery(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func metadataInt64(raw json.RawMessage, keys ...string) int64 {
	if len(raw) == 0 {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0
	}
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int64(typed)
		case int64:
			return typed
		case int:
			return int64(typed)
		case string:
			if typed == "" {
				continue
			}
			var parsed int64
			if _, err := fmt.Sscan(typed, &parsed); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func metadataMatchesWBProductID(raw json.RawMessage, wbProductID int64) bool {
	return wbProductID > 0 && metadataInt64(raw, "wb_product_id", "wbProductID") == wbProductID
}

func metadataMatchesWBCampaignID(raw json.RawMessage, wbCampaignID int64) bool {
	return wbCampaignID > 0 && metadataInt64(raw, "wb_campaign_id", "wbCampaignID") == wbCampaignID
}

func matchPageContextProduct(item domain.ExtensionPageContext, productID uuid.UUID, wbProductID int64) bool {
	return (item.ProductID != nil && *item.ProductID == productID) || metadataMatchesWBProductID(item.Metadata, wbProductID)
}

func matchBidProduct(item domain.ExtensionBidSnapshot, productID uuid.UUID, wbProductID int64) bool {
	return metadataMatchesWBProductID(item.Metadata, wbProductID)
}

func matchPositionProduct(item domain.ExtensionPositionSnapshot, productID uuid.UUID, wbProductID int64) bool {
	return (item.ProductID != nil && *item.ProductID == productID) || metadataMatchesWBProductID(item.Metadata, wbProductID)
}

func matchSignalProduct(item domain.ExtensionUISignal, productID uuid.UUID, wbProductID int64) bool {
	return (item.ProductID != nil && *item.ProductID == productID) || metadataMatchesWBProductID(item.Metadata, wbProductID)
}

func matchPageContextCampaign(item domain.ExtensionPageContext, campaignID uuid.UUID, wbCampaignID int64) bool {
	return (item.CampaignID != nil && *item.CampaignID == campaignID) || metadataMatchesWBCampaignID(item.Metadata, wbCampaignID)
}

func matchBidCampaign(item domain.ExtensionBidSnapshot, campaignID uuid.UUID, wbCampaignID int64) bool {
	return (item.CampaignID != nil && *item.CampaignID == campaignID) || metadataMatchesWBCampaignID(item.Metadata, wbCampaignID)
}

func matchPositionCampaign(item domain.ExtensionPositionSnapshot, campaignID uuid.UUID, wbCampaignID int64) bool {
	return (item.CampaignID != nil && *item.CampaignID == campaignID) || metadataMatchesWBCampaignID(item.Metadata, wbCampaignID)
}

func matchSignalCampaign(item domain.ExtensionUISignal, campaignID uuid.UUID, wbCampaignID int64) bool {
	return (item.CampaignID != nil && *item.CampaignID == campaignID) || metadataMatchesWBCampaignID(item.Metadata, wbCampaignID)
}

func matchPageContextPhrase(item domain.ExtensionPageContext, phraseID uuid.UUID, keyword string, campaignID uuid.UUID, wbCampaignID int64) bool {
	if item.PhraseID != nil && *item.PhraseID == phraseID {
		return true
	}
	if keyword != "" && item.Query != nil && normalizeEvidenceQuery(*item.Query) == normalizeEvidenceQuery(keyword) {
		if campaignID == uuid.Nil {
			return true
		}
		return matchPageContextCampaign(item, campaignID, wbCampaignID)
	}
	return false
}

func matchBidPhrase(item domain.ExtensionBidSnapshot, phraseID uuid.UUID, keyword string, campaignID uuid.UUID, wbCampaignID int64) bool {
	if item.PhraseID != nil && *item.PhraseID == phraseID {
		return true
	}
	if keyword != "" && item.Query != nil && normalizeEvidenceQuery(*item.Query) == normalizeEvidenceQuery(keyword) {
		if campaignID == uuid.Nil {
			return true
		}
		return matchBidCampaign(item, campaignID, wbCampaignID)
	}
	return false
}

func matchPositionPhrase(item domain.ExtensionPositionSnapshot, phraseID uuid.UUID, keyword string, campaignID uuid.UUID, wbCampaignID int64) bool {
	if item.PhraseID != nil && *item.PhraseID == phraseID {
		return true
	}
	if keyword != "" && normalizeEvidenceQuery(item.Query) == normalizeEvidenceQuery(keyword) {
		if campaignID == uuid.Nil {
			return true
		}
		return matchPositionCampaign(item, campaignID, wbCampaignID)
	}
	return false
}

func matchSignalPhrase(item domain.ExtensionUISignal, phraseID uuid.UUID, keyword string, campaignID uuid.UUID, wbCampaignID int64) bool {
	if item.PhraseID != nil && *item.PhraseID == phraseID {
		return true
	}
	if keyword != "" && item.Query != nil && normalizeEvidenceQuery(*item.Query) == normalizeEvidenceQuery(keyword) {
		if campaignID == uuid.Nil {
			return true
		}
		return matchSignalCampaign(item, campaignID, wbCampaignID)
	}
	return false
}

func recommendationQuery(raw json.RawMessage) *string {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	for _, key := range []string{"query", "keyword", "phrase"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			result := value
			return &result
		}
	}
	return nil
}

func recommendationWBCampaignID(raw json.RawMessage) int64 {
	return metadataInt64(raw, "wb_campaign_id", "wbCampaignID")
}

func recommendationWBProductID(raw json.RawMessage) int64 {
	return metadataInt64(raw, "wb_product_id", "wbProductID")
}
