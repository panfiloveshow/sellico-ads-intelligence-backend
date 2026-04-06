package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

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
		CreatedAt:       p.CreatedAt.Time,
		UpdatedAt:       p.UpdatedAt.Time,
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
	return domain.PhraseStat{
		ID:          uuidFromPgtype(s.ID),
		PhraseID:    uuidFromPgtype(s.PhraseID),
		Date:        s.Date.Time,
		Impressions: s.Impressions,
		Clicks:      s.Clicks,
		Spend:       s.Spend,
		CreatedAt:   s.CreatedAt.Time,
		UpdatedAt:   s.UpdatedAt.Time,
	}
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
