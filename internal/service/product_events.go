package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ProductEventService detects and records product changes.
type ProductEventService struct {
	queries *sqlcgen.Queries
	logger  zerolog.Logger
}

func NewProductEventService(queries *sqlcgen.Queries, logger zerolog.Logger) *ProductEventService {
	return &ProductEventService{
		queries: queries,
		logger:  logger.With().Str("component", "product_events").Logger(),
	}
}

// DetectChanges compares current product state with last snapshot and creates events.
// Called after each product sync.
func (s *ProductEventService) DetectChanges(ctx context.Context, workspaceID uuid.UUID, current domain.Product, previous *domain.ProductSnapshot) []domain.ProductEvent {
	var events []domain.ProductEvent

	if previous == nil {
		events = append(events, domain.ProductEvent{
			WorkspaceID: workspaceID,
			ProductID:   current.ID,
			EventType:   domain.EventCreated,
			NewValue:    current.Title,
			Source:      "sync",
		})
		return events
	}

	if current.Title != previous.Title && current.Title != "" {
		events = append(events, domain.ProductEvent{WorkspaceID: workspaceID, ProductID: current.ID, EventType: domain.EventTitleChange, FieldName: "title", OldValue: previous.Title, NewValue: current.Title, Source: "sync"})
	}

	curPrice := ptrInt64(current.Price)
	if curPrice != previous.Price && curPrice > 0 {
		events = append(events, domain.ProductEvent{WorkspaceID: workspaceID, ProductID: current.ID, EventType: domain.EventPriceChange, FieldName: "price", OldValue: fmt.Sprintf("%d", previous.Price), NewValue: fmt.Sprintf("%d", curPrice), Metadata: map[string]any{"delta": curPrice - previous.Price, "delta_pct": priceDeltaPct(previous.Price, curPrice)}, Source: "sync"})
	}

	curBrand := ptrStr(current.Brand)
	if curBrand != previous.Brand && curBrand != "" {
		events = append(events, domain.ProductEvent{WorkspaceID: workspaceID, ProductID: current.ID, EventType: domain.EventBrandChange, FieldName: "brand", OldValue: previous.Brand, NewValue: curBrand, Source: "sync"})
	}

	curCategory := ptrStr(current.Category)
	if curCategory != previous.Category && curCategory != "" {
		events = append(events, domain.ProductEvent{WorkspaceID: workspaceID, ProductID: current.ID, EventType: domain.EventCategoryChange, FieldName: "category", OldValue: previous.Category, NewValue: curCategory, Source: "sync"})
	}

	curImage := ptrStr(current.ImageURL)
	if curImage != previous.ImageURL && curImage != "" {
		events = append(events, domain.ProductEvent{WorkspaceID: workspaceID, ProductID: current.ID, EventType: domain.EventPhotoChange, FieldName: "image_url", OldValue: previous.ImageURL, NewValue: curImage, Source: "sync"})
	}

	currentHash := contentHash(current)
	if currentHash != previous.ContentHash && previous.ContentHash != "" {
		events = append(events, domain.ProductEvent{WorkspaceID: workspaceID, ProductID: current.ID, EventType: domain.EventContentChange, FieldName: "content_hash", OldValue: previous.ContentHash, NewValue: currentHash, Source: "sync"})
	}

	return events
}

// RecordEvents saves detected events to the database.
func (s *ProductEventService) RecordEvents(ctx context.Context, events []domain.ProductEvent) {
	for _, event := range events {
		metaJSON, _ := json.Marshal(event.Metadata)
		s.queries.CreateProductEvent(ctx, sqlcgen.CreateProductEventParams{
			WorkspaceID: uuidToPgtype(event.WorkspaceID),
			ProductID:   uuidToPgtype(event.ProductID),
			EventType:   event.EventType,
			FieldName:   textToPgtype(event.FieldName),
			OldValue:    textToPgtype(event.OldValue),
			NewValue:    textToPgtype(event.NewValue),
			Metadata:    metaJSON,
			Source:      event.Source,
		})
	}

	if len(events) > 0 {
		s.logger.Info().Int("count", len(events)).Msg("product events recorded")
	}
}

// ListEvents returns events for a product.
func (s *ProductEventService) ListEvents(ctx context.Context, productID uuid.UUID, limit, offset int32) ([]domain.ProductEvent, error) {
	rows, err := s.queries.ListProductEvents(ctx, sqlcgen.ListProductEventsParams{
		ProductID: uuidToPgtype(productID),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, err
	}

	result := make([]domain.ProductEvent, len(rows))
	for i, row := range rows {
		result[i] = productEventFromSqlc(row)
	}
	return result, nil
}

// ListEventsByWorkspace returns recent events for a workspace.
func (s *ProductEventService) ListEventsByWorkspace(ctx context.Context, workspaceID uuid.UUID, eventType string, limit, offset int32) ([]domain.ProductEvent, error) {
	rows, err := s.queries.ListProductEventsByWorkspace(ctx, sqlcgen.ListProductEventsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		EventType:   textToPgtype(eventType),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, err
	}

	result := make([]domain.ProductEvent, len(rows))
	for i, row := range rows {
		result[i] = productEventFromSqlc(row)
	}
	return result, nil
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func contentHash(p domain.Product) string {
	data := strings.Join([]string{p.Title, ptrStr(p.Brand), ptrStr(p.Category), ptrStr(p.ImageURL), fmt.Sprintf("%d", ptrInt64(p.Price))}, "|")
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:16])
}

func priceDeltaPct(old, new int64) float64 {
	if old == 0 {
		return 0
	}
	return float64(new-old) / float64(old) * 100
}

func productEventFromSqlc(row sqlcgen.ProductEvent) domain.ProductEvent {
	e := domain.ProductEvent{
		ID:          uuidFromPgtype(row.ID),
		WorkspaceID: uuidFromPgtype(row.WorkspaceID),
		ProductID:   uuidFromPgtype(row.ProductID),
		EventType:   row.EventType,
		DetectedAt:  row.DetectedAt.Time,
		Source:      row.Source,
	}
	if row.FieldName.Valid {
		e.FieldName = row.FieldName.String
	}
	if row.OldValue.Valid {
		e.OldValue = row.OldValue.String
	}
	if row.NewValue.Valid {
		e.NewValue = row.NewValue.String
	}
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &e.Metadata)
	}
	return e
}
