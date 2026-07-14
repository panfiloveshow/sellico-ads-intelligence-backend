package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func checkedInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("value %d is outside int32 range", value)
	}
	// #nosec G115 -- the explicit bounds check above guarantees a lossless conversion.
	return int32(value), nil
}

func boundedInt32(value int) int32 {
	if value < math.MinInt32 {
		return math.MinInt32
	}
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	// #nosec G115 -- the explicit bounds checks above guarantee a lossless conversion.
	return int32(value)
}

// writeAuditLog creates an audit log entry and logs any errors to stderr.
// Audit log failures should never break business operations, but must be visible.
func writeAuditLog(ctx context.Context, queries *sqlcgen.Queries, params sqlcgen.CreateAuditLogParams) {
	if _, err := queries.CreateAuditLog(ctx, params); err != nil {
		log.Printf("[WARN] audit log write failed: action=%s entity_type=%s err=%v", params.Action, params.EntityType, err)
	}
}

func textToPgtype(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func int8ToPtr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}

func int4ToPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	value := int(v.Int32)
	return &value
}

func float8ToPtr(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	value := v.Float64
	return &value
}

func textToPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	value := v.String
	return &value
}

func timeToPtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	value := v.Time
	return &value
}

func uuidToPgtypePtr(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return uuidToPgtype(*id)
}

func uuidToPtr(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	value := uuid.UUID(id.Bytes)
	return &value
}

func timePtrToPgtype(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func optionalInt64ToPgInt8(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func optionalIntToPgInt4(value *int) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*value), Valid: true}
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

func numericToFloat64(n pgtype.Numeric) float64 {
	value, err := n.Float64Value()
	if err != nil || !value.Valid {
		return 0
	}
	return value.Float64
}

func numericToFloat64Ptr(n pgtype.Numeric) *float64 {
	value, err := n.Float64Value()
	if err != nil || !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func numericFromFloat64(v float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.ScanScientific(fmt.Sprintf("%.2f", v)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// CampaignBidSnapshot is a campaign-level view of real WB product bids.
// A snapshot is returned only when all linked products with a bid agree;
// mixed product bids are deliberately treated as ambiguous for campaign-wide
// automation.
type CampaignBidSnapshot struct {
	Bid          int
	Placement    string
	ProductCount int
	CapturedAt   time.Time
}

func campaignBidSnapshot(ctx context.Context, queries *sqlcgen.Queries, campaignID uuid.UUID, placement string) (CampaignBidSnapshot, bool, error) {
	links, err := queries.ListCampaignProductsByCampaign(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return CampaignBidSnapshot{}, false, err
	}
	if len(links) == 0 {
		return CampaignBidSnapshot{}, false, nil
	}
	snapshot, ok := campaignBidSnapshotFromLinks(links, placement)
	return snapshot, ok, nil
}

func currentBidFromCampaignPhrases(ctx context.Context, queries *sqlcgen.Queries, campaignID uuid.UUID) (int, bool, error) {
	// Product bids are the authoritative source for campaign-level WB actions.
	// Only fall back to phrase bids for campaigns that do not yet have product
	// links; if product rows exist but disagree, campaign-wide automation must
	// fail closed.
	productLinks, err := queries.ListCampaignProductsByCampaign(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return 0, false, err
	}
	if len(productLinks) > 0 {
		snapshot, ok := campaignBidSnapshotFromLinks(productLinks, "search")
		return snapshot.Bid, ok, nil
	}

	phrases, err := queries.ListPhrasesByCampaign(ctx, sqlcgen.ListPhrasesByCampaignParams{
		CampaignID: uuidToPgtype(campaignID),
		Limit:      1000,
		Offset:     0,
	})
	if err != nil {
		return 0, false, err
	}

	var currentBid int
	for _, phrase := range phrases {
		if !phrase.CurrentBid.Valid || phrase.CurrentBid.Int64 <= 0 {
			continue
		}
		bid := int(phrase.CurrentBid.Int64)
		if currentBid == 0 {
			currentBid = bid
			continue
		}
		if currentBid != bid {
			// Campaign-wide bid updates are unsafe when real phrase bids disagree.
			return 0, false, nil
		}
	}
	if currentBid == 0 {
		return 0, false, nil
	}
	return currentBid, true, nil
}

func campaignBidSnapshotFromLinks(links []sqlcgen.CampaignProduct, placement string) (CampaignBidSnapshot, bool) {
	snapshot := CampaignBidSnapshot{Placement: placement}
	if len(links) == 0 || (placement != "search" && placement != "recommendations" && placement != "combined") {
		return CampaignBidSnapshot{}, false
	}
	for _, link := range links {
		bidValue := link.BidSearch
		if placement == "recommendations" {
			bidValue = link.BidRecommendations
		}
		if !bidValue.Valid || bidValue.Int64 <= 0 {
			// A campaign-wide action cannot safely infer the missing SKU bid
			// from the other products.
			return CampaignBidSnapshot{}, false
		}
		bid := int(bidValue.Int64)
		if snapshot.Bid != 0 && snapshot.Bid != bid {
			return CampaignBidSnapshot{}, false
		}
		snapshot.Bid = bid
		snapshot.ProductCount++
		if !link.UpdatedAt.Valid {
			return CampaignBidSnapshot{}, false
		}
		if snapshot.CapturedAt.IsZero() || link.UpdatedAt.Time.Before(snapshot.CapturedAt) {
			// Freshness is bounded by the oldest SKU in the aggregate.
			snapshot.CapturedAt = link.UpdatedAt.Time
		}
	}
	if snapshot.Bid == 0 || snapshot.ProductCount != len(links) || snapshot.CapturedAt.IsZero() {
		return CampaignBidSnapshot{}, false
	}
	return snapshot, true
}

// SellerCabinetAutomationReadinessBlockReason is the fail-closed cabinet
// freshness primitive for automatic actions. An absent row is represented by
// the zero value and is blocked like any other incomplete state.
func SellerCabinetAutomationReadinessBlockReason(state sqlcgen.SellerCabinetSyncState, now time.Time, maxAge time.Duration) string {
	if state.Status != "ready" {
		if state.Status == "" {
			return "cabinet has no completed sync readiness state"
		}
		return fmt.Sprintf("cabinet sync status is %s", state.Status)
	}
	if !state.CompletedAt.Valid {
		return "cabinet sync completion time is unavailable"
	}
	if maxAge > 0 && now.UTC().Sub(state.CompletedAt.Time.UTC()) > maxAge {
		return "cabinet sync readiness is stale"
	}
	if state.RateLimited {
		return "cabinet sync was rate limited"
	}
	if !state.DataThroughDate.Valid {
		return "cabinet advertising data date is unavailable"
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("MSK", 3*60*60)
	}
	today := time.Date(now.In(location).Year(), now.In(location).Month(), now.In(location).Day(), 0, 0, 0, 0, location)
	dataThrough := time.Date(state.DataThroughDate.Time.Year(), state.DataThroughDate.Time.Month(), state.DataThroughDate.Time.Day(), 0, 0, 0, 0, location)
	if dataThrough.Before(today) {
		return "cabinet advertising data does not include the current day"
	}
	return ""
}

func productFromSqlc(p sqlcgen.Product) domain.Product {
	return domain.Product{
		ID:              uuidFromPgtype(p.ID),
		WorkspaceID:     uuidFromPgtype(p.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(p.SellerCabinetID),
		WBProductID:     p.WbProductID,
		Title:           p.Title,
		Brand:           textToPtr(p.Brand),
		Category:        textToPtr(p.Category),
		ImageURL:        textToPtr(p.ImageUrl),
		Price:           int8ToPtr(p.Price),
		Rating:          float8ToPtr(p.Rating),
		ReviewsCount:    int4ToPtr(p.ReviewsCount),
		CreatedAt:       p.CreatedAt.Time,
		UpdatedAt:       p.UpdatedAt.Time,
	}
}

func productStatFromSqlc(s sqlcgen.ProductStat) domain.ProductStat {
	result := domain.ProductStat{
		ID:          uuidFromPgtype(s.ID),
		ProductID:   uuidFromPgtype(s.ProductID),
		CampaignID:  uuidFromPgtype(s.CampaignID),
		Date:        s.Date.Time,
		Impressions: s.Impressions,
		Clicks:      s.Clicks,
		Spend:       s.Spend,
		CreatedAt:   s.CreatedAt.Time,
		UpdatedAt:   s.UpdatedAt.Time,
	}
	if s.Orders.Valid {
		v := s.Orders.Int64
		result.Orders = &v
	}
	if s.Revenue.Valid {
		v := s.Revenue.Int64
		result.Revenue = &v
	}
	if s.Atbs.Valid {
		v := s.Atbs.Int64
		result.Atbs = &v
	}
	if s.Canceled.Valid {
		v := s.Canceled.Int64
		result.Canceled = &v
	}
	if s.Shks.Valid {
		v := s.Shks.Int64
		result.Shks = &v
	}
	return result
}

func productBusinessFromSqlc(s sqlcgen.ProductSalesDaily) domain.ProductBusinessSummary {
	return domain.ProductBusinessSummary{
		Date:            s.Date.Time,
		Orders:          s.Orders,
		CanceledOrders:  s.CanceledOrders,
		Sales:           s.Sales,
		Returns:         s.Returns,
		OrderedRevenue:  s.OrderedRevenue,
		SoldRevenue:     s.SoldRevenue,
		ReturnedRevenue: s.ReturnedRevenue,
		DataMode:        "reports",
	}
}

func positionFromSqlc(p sqlcgen.Position) domain.Position {
	return domain.Position{
		ID:          uuidFromPgtype(p.ID),
		WorkspaceID: uuidFromPgtype(p.WorkspaceID),
		ProductID:   uuidFromPgtype(p.ProductID),
		Query:       p.Query,
		Region:      p.Region,
		Position:    int(p.Position),
		Page:        int(p.Page),
		Source:      p.Source,
		CheckedAt:   p.CheckedAt.Time,
		CreatedAt:   p.CreatedAt.Time,
	}
}

func positionTrackingTargetFromSqlc(t sqlcgen.PositionTrackingTarget) domain.PositionTrackingTarget {
	return domain.PositionTrackingTarget{
		ID:                uuidFromPgtype(t.ID),
		WorkspaceID:       uuidFromPgtype(t.WorkspaceID),
		ProductID:         uuidFromPgtype(t.ProductID),
		Query:             t.Query,
		Region:            t.Region,
		IsActive:          t.IsActive,
		BaselinePosition:  int4ToPtr(t.BaselinePosition),
		BaselineCheckedAt: timeToPtr(t.BaselineCheckedAt),
		CreatedAt:         t.CreatedAt.Time,
		UpdatedAt:         t.UpdatedAt.Time,
	}
}

func serpSnapshotFromSqlc(s sqlcgen.SerpSnapshot) domain.SERPSnapshot {
	return domain.SERPSnapshot{
		ID:           uuidFromPgtype(s.ID),
		WorkspaceID:  uuidFromPgtype(s.WorkspaceID),
		Query:        s.Query,
		Region:       s.Region,
		TotalResults: int(s.TotalResults),
		ScannedAt:    s.ScannedAt.Time,
		CreatedAt:    s.CreatedAt.Time,
	}
}

func serpResultItemFromSqlc(i sqlcgen.SerpResultItem) domain.SERPResultItem {
	return domain.SERPResultItem{
		ID:           uuidFromPgtype(i.ID),
		SnapshotID:   uuidFromPgtype(i.SnapshotID),
		Position:     int(i.Position),
		WBProductID:  i.WbProductID,
		Title:        i.Title,
		Price:        int8ToPtr(i.Price),
		Rating:       numericToFloat64Ptr(i.Rating),
		ReviewsCount: int4ToPtr(i.ReviewsCount),
		CreatedAt:    i.CreatedAt.Time,
	}
}

func auditLogFromSqlc(a sqlcgen.AuditLog) domain.AuditLog {
	return domain.AuditLog{
		ID:          uuidFromPgtype(a.ID),
		WorkspaceID: uuidFromPgtype(a.WorkspaceID),
		UserID:      uuidToPtr(a.UserID),
		Action:      a.Action,
		EntityType:  a.EntityType,
		EntityID:    uuidToPtr(a.EntityID),
		Metadata:    json.RawMessage(a.Metadata),
		CreatedAt:   a.CreatedAt.Time,
	}
}

func jobRunFromSqlc(j sqlcgen.JobRun) domain.JobRun {
	return domain.JobRun{
		ID:           uuidFromPgtype(j.ID),
		WorkspaceID:  uuidToPtr(j.WorkspaceID),
		TaskType:     j.TaskType,
		Status:       j.Status,
		StartedAt:    j.StartedAt.Time,
		FinishedAt:   timeToPtr(j.FinishedAt),
		ErrorMessage: textToPtr(j.ErrorMessage),
		Metadata:     json.RawMessage(j.Metadata),
		CreatedAt:    j.CreatedAt.Time,
	}
}

func extensionSessionFromSqlc(s sqlcgen.ExtensionSession) domain.ExtensionSession {
	return domain.ExtensionSession{
		ID:               uuidFromPgtype(s.ID),
		UserID:           uuidFromPgtype(s.UserID),
		WorkspaceID:      uuidFromPgtype(s.WorkspaceID),
		ExtensionVersion: s.ExtensionVersion,
		LastActiveAt:     s.LastActiveAt.Time,
		CreatedAt:        s.CreatedAt.Time,
	}
}

func extensionContextEventFromSqlc(e sqlcgen.ExtensionContextEvent) domain.ExtensionContextEvent {
	return domain.ExtensionContextEvent{
		ID:          uuidFromPgtype(e.ID),
		SessionID:   uuidFromPgtype(e.SessionID),
		WorkspaceID: uuidFromPgtype(e.WorkspaceID),
		UserID:      uuidFromPgtype(e.UserID),
		URL:         e.Url,
		PageType:    e.PageType,
		Metadata:    json.RawMessage(e.Metadata),
		CreatedAt:   e.CreatedAt.Time,
	}
}

func extensionPageContextFromSqlc(e sqlcgen.ExtensionPageContext) domain.ExtensionPageContext {
	return domain.ExtensionPageContext{
		ID:              uuidFromPgtype(e.ID),
		SessionID:       uuidFromPgtype(e.SessionID),
		WorkspaceID:     uuidFromPgtype(e.WorkspaceID),
		UserID:          uuidFromPgtype(e.UserID),
		URL:             e.Url,
		PageType:        e.PageType,
		SellerCabinetID: uuidToPtr(e.SellerCabinetID),
		CampaignID:      uuidToPtr(e.CampaignID),
		PhraseID:        uuidToPtr(e.PhraseID),
		ProductID:       uuidToPtr(e.ProductID),
		Query:           textToPtr(e.Query),
		Region:          textToPtr(e.Region),
		ActiveFilters:   json.RawMessage(e.ActiveFilters),
		Metadata:        json.RawMessage(e.Metadata),
		CapturedAt:      e.CapturedAt.Time,
		CreatedAt:       e.CreatedAt.Time,
	}
}

func extensionBidSnapshotFromSqlc(e sqlcgen.ExtensionBidSnapshot) domain.ExtensionBidSnapshot {
	return domain.ExtensionBidSnapshot{
		ID:              uuidFromPgtype(e.ID),
		SessionID:       uuidFromPgtype(e.SessionID),
		WorkspaceID:     uuidFromPgtype(e.WorkspaceID),
		UserID:          uuidFromPgtype(e.UserID),
		SellerCabinetID: uuidToPtr(e.SellerCabinetID),
		CampaignID:      uuidToPtr(e.CampaignID),
		PhraseID:        uuidToPtr(e.PhraseID),
		Query:           textToPtr(e.Query),
		Region:          textToPtr(e.Region),
		VisibleBid:      int8ToPtr(e.VisibleBid),
		RecommendedBid:  int8ToPtr(e.RecommendedBid),
		CompetitiveBid:  int8ToPtr(e.CompetitiveBid),
		LeadershipBid:   int8ToPtr(e.LeadershipBid),
		CPMMin:          int8ToPtr(e.CpmMin),
		Source:          e.Source,
		Confidence:      numericToFloat64(e.Confidence),
		Metadata:        json.RawMessage(e.Metadata),
		CapturedAt:      e.CapturedAt.Time,
		CreatedAt:       e.CreatedAt.Time,
	}
}

func extensionPositionSnapshotFromSqlc(e sqlcgen.ExtensionPositionSnapshot) domain.ExtensionPositionSnapshot {
	return domain.ExtensionPositionSnapshot{
		ID:              uuidFromPgtype(e.ID),
		SessionID:       uuidFromPgtype(e.SessionID),
		WorkspaceID:     uuidFromPgtype(e.WorkspaceID),
		UserID:          uuidFromPgtype(e.UserID),
		SellerCabinetID: uuidToPtr(e.SellerCabinetID),
		CampaignID:      uuidToPtr(e.CampaignID),
		PhraseID:        uuidToPtr(e.PhraseID),
		ProductID:       uuidToPtr(e.ProductID),
		Query:           e.Query,
		Region:          e.Region,
		VisiblePosition: int(e.VisiblePosition),
		VisiblePage:     int4ToPtr(e.VisiblePage),
		PageSubtype:     textToPtr(e.PageSubtype),
		Source:          e.Source,
		Confidence:      numericToFloat64(e.Confidence),
		Metadata:        json.RawMessage(e.Metadata),
		CapturedAt:      e.CapturedAt.Time,
		CreatedAt:       e.CreatedAt.Time,
	}
}

func extensionUISignalFromSqlc(e sqlcgen.ExtensionUiSignal) domain.ExtensionUISignal {
	return domain.ExtensionUISignal{
		ID:              uuidFromPgtype(e.ID),
		SessionID:       uuidFromPgtype(e.SessionID),
		WorkspaceID:     uuidFromPgtype(e.WorkspaceID),
		UserID:          uuidFromPgtype(e.UserID),
		SellerCabinetID: uuidToPtr(e.SellerCabinetID),
		CampaignID:      uuidToPtr(e.CampaignID),
		PhraseID:        uuidToPtr(e.PhraseID),
		ProductID:       uuidToPtr(e.ProductID),
		Query:           textToPtr(e.Query),
		Region:          textToPtr(e.Region),
		SignalType:      e.SignalType,
		Severity:        e.Severity,
		Title:           e.Title,
		Message:         textToPtr(e.Message),
		Confidence:      numericToFloat64(e.Confidence),
		Metadata:        json.RawMessage(e.Metadata),
		CapturedAt:      e.CapturedAt.Time,
		CreatedAt:       e.CreatedAt.Time,
	}
}

func extensionNetworkCaptureFromSqlc(e sqlcgen.ExtensionNetworkCapture) domain.ExtensionNetworkCapture {
	return domain.ExtensionNetworkCapture{
		ID:              uuidFromPgtype(e.ID),
		SessionID:       uuidFromPgtype(e.SessionID),
		WorkspaceID:     uuidFromPgtype(e.WorkspaceID),
		UserID:          uuidFromPgtype(e.UserID),
		SellerCabinetID: uuidToPtr(e.SellerCabinetID),
		CampaignID:      uuidToPtr(e.CampaignID),
		PhraseID:        uuidToPtr(e.PhraseID),
		ProductID:       uuidToPtr(e.ProductID),
		PageType:        e.PageType,
		EndpointKey:     e.EndpointKey,
		Query:           textToPtr(e.Query),
		Region:          textToPtr(e.Region),
		Payload:         json.RawMessage(e.Payload),
		CapturedAt:      e.CapturedAt.Time,
		CreatedAt:       e.CreatedAt.Time,
	}
}

func extensionDOMRowSnapshotFromSqlc(e sqlcgen.ExtensionDomRowSnapshot) domain.ExtensionDOMRowSnapshot {
	return domain.ExtensionDOMRowSnapshot{
		ID:              uuidFromPgtype(e.ID),
		SessionID:       uuidFromPgtype(e.SessionID),
		WorkspaceID:     uuidFromPgtype(e.WorkspaceID),
		UserID:          uuidFromPgtype(e.UserID),
		SellerCabinetID: uuidToPtr(e.SellerCabinetID),
		CampaignID:      uuidToPtr(e.CampaignID),
		PhraseID:        uuidToPtr(e.PhraseID),
		ProductID:       uuidToPtr(e.ProductID),
		PageType:        e.PageType,
		TableRole:       e.TableRole,
		RowKey:          e.RowKey,
		Query:           textToPtr(e.Query),
		Region:          textToPtr(e.Region),
		VisibleText:     e.VisibleText,
		Cells:           json.RawMessage(e.Cells),
		Metadata:        json.RawMessage(e.Metadata),
		Source:          e.Source,
		Confidence:      numericToFloat64(e.Confidence),
		CapturedAt:      e.CapturedAt.Time,
		CreatedAt:       e.CreatedAt.Time,
	}
}

func recommendationFromSqlc(r sqlcgen.Recommendation) domain.Recommendation {
	return domain.Recommendation{
		ID:            uuidFromPgtype(r.ID),
		WorkspaceID:   uuidFromPgtype(r.WorkspaceID),
		CampaignID:    uuidToPtr(r.CampaignID),
		PhraseID:      uuidToPtr(r.PhraseID),
		ProductID:     uuidToPtr(r.ProductID),
		Title:         r.Title,
		Description:   r.Description,
		Type:          r.Type,
		Severity:      r.Severity,
		Confidence:    numericToFloat64(r.Confidence),
		SourceMetrics: json.RawMessage(r.SourceMetrics),
		NextAction:    textToPtr(r.NextAction),
		Status:        r.Status,
		CreatedAt:     r.CreatedAt.Time,
		UpdatedAt:     r.UpdatedAt.Time,
	}
}

func exportFromSqlc(e sqlcgen.Export) domain.Export {
	return domain.Export{
		ID:           uuidFromPgtype(e.ID),
		WorkspaceID:  uuidFromPgtype(e.WorkspaceID),
		UserID:       uuidFromPgtype(e.UserID),
		EntityType:   e.EntityType,
		Format:       e.Format,
		Status:       e.Status,
		FilePath:     textToPtr(e.FilePath),
		ErrorMessage: textToPtr(e.ErrorMessage),
		Filters:      json.RawMessage(e.Filters),
		CreatedAt:    e.CreatedAt.Time,
		UpdatedAt:    e.UpdatedAt.Time,
	}
}

func campaignStatFromLatestSqlc(s sqlcgen.CampaignStat) domain.CampaignStat {
	return campaignStatFromSqlc(s)
}

func phraseStatFromSqlc(s sqlcgen.PhraseStat) domain.PhraseStat {
	result := domain.PhraseStat{
		ID:          uuidFromPgtype(s.ID),
		PhraseID:    uuidFromPgtype(s.PhraseID),
		Date:        s.Date.Time,
		Impressions: s.Impressions,
		Clicks:      s.Clicks,
		Spend:       s.Spend,
		CreatedAt:   s.CreatedAt.Time,
		UpdatedAt:   s.UpdatedAt.Time,
	}
	if s.Atbs.Valid {
		v := s.Atbs.Int64
		result.Atbs = &v
	}
	if s.Orders.Valid {
		v := s.Orders.Int64
		result.Orders = &v
	}
	if s.Cpc.Valid {
		v := s.Cpc.Float64
		result.CPC = &v
	}
	if s.Cpm.Valid {
		v := s.Cpm.Float64
		result.CPM = &v
	}
	if s.AvgPos.Valid {
		v := s.AvgPos.Float64
		result.AvgPos = &v
	}
	return result
}

func bidSnapshotFromSqlc(b sqlcgen.BidSnapshot) domain.BidSnapshot {
	return domain.BidSnapshot{
		ID:             uuidFromPgtype(b.ID),
		PhraseID:       uuidFromPgtype(b.PhraseID),
		WorkspaceID:    uuidFromPgtype(b.WorkspaceID),
		CompetitiveBid: b.CompetitiveBid,
		LeadershipBid:  b.LeadershipBid,
		CPMMin:         b.CpmMin,
		CapturedAt:     b.CapturedAt.Time,
		CreatedAt:      b.CreatedAt.Time,
	}
}
