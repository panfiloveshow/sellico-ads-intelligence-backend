package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type SERPListFilter struct {
	Query    string
	Region   string
	DateFrom *time.Time
	DateTo   *time.Time
}

type CreateSERPResultItemInput struct {
	Position     int
	WBProductID  int64
	Title        string
	Price        *int64
	Rating       *float64
	ReviewsCount *int
}

type CreateSERPSnapshotInput struct {
	Query        string
	Region       string
	TotalResults int
	ScannedAt    *time.Time
	Items        []CreateSERPResultItemInput
}

// SERPService handles SERP snapshot read operations.
type SERPService struct {
	queries  *sqlcgen.Queries
	db       *pgxpool.Pool
	enqueuer RecommendationJobEnqueuer
	logger   zerolog.Logger
}

func NewSERPService(queries *sqlcgen.Queries, db *pgxpool.Pool, enqueuer RecommendationJobEnqueuer, logger zerolog.Logger) *SERPService {
	return &SERPService{
		queries:  queries,
		db:       db,
		enqueuer: enqueuer,
		logger:   logger.With().Str("component", "serp_service").Logger(),
	}
}

func (s *SERPService) Create(ctx context.Context, actorID, workspaceID uuid.UUID, input CreateSERPSnapshotInput) (*domain.SERPSnapshot, []domain.SERPResultItem, error) {
	if s.db == nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "database transaction manager is not configured")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to start serp snapshot transaction")
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := s.queries.WithTx(tx)
	scannedAt := time.Now().UTC()
	if input.ScannedAt != nil {
		scannedAt = input.ScannedAt.UTC()
	}

	snapshotRow, err := qtx.CreateSERPSnapshot(ctx, sqlcgen.CreateSERPSnapshotParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		Query:        input.Query,
		Region:       input.Region,
		TotalResults: int32(input.TotalResults),
		ScannedAt:    timePtrToPgtype(&scannedAt),
	})
	if err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to create serp snapshot")
	}

	batchItems := make([]sqlcgen.BatchCreateSERPResultItemsParams, 0, len(input.Items))
	for _, item := range input.Items {
		rating, convErr := numericFromFloat64(ptrFloat64Value(item.Rating))
		if convErr != nil && item.Rating != nil {
			return nil, nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("invalid rating: %v", convErr))
		}
		if item.Rating == nil {
			rating = pgtype.Numeric{}
		}

		batchItems = append(batchItems, sqlcgen.BatchCreateSERPResultItemsParams{
			SnapshotID:   snapshotRow.ID,
			Position:     int32(item.Position),
			WbProductID:  item.WBProductID,
			Title:        item.Title,
			Price:        optionalInt64ToPgInt8(item.Price),
			Rating:       rating,
			ReviewsCount: optionalIntToPgInt4(item.ReviewsCount),
		})
	}

	if _, err := qtx.BatchCreateSERPResultItems(ctx, batchItems); err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to batch create serp snapshot items")
	}

	itemRows, err := qtx.ListSERPResultItemsBySnapshot(ctx, snapshotRow.ID)
	if err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to load created serp snapshot items")
	}

	items := make([]domain.SERPResultItem, len(itemRows))
	for i, row := range itemRows {
		items[i] = serpResultItemFromSqlc(row)
	}

	meta, _ := json.Marshal(map[string]any{
		"query":       input.Query,
		"region":      input.Region,
		"items_count": len(items),
	})
	writeAuditLog(ctx, qtx, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "ingest_serp_snapshot",
		EntityType:  "serp_snapshot",
		EntityID:    snapshotRow.ID,
		Metadata:    meta,
	})

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to commit serp snapshot transaction")
	}

	snapshot := serpSnapshotFromSqlc(snapshotRow)
	s.tryEnqueueRecommendationGeneration(workspaceID)
	return &snapshot, items, nil
}

func (s *SERPService) List(ctx context.Context, workspaceID uuid.UUID, filter SERPListFilter, limit, offset int32) ([]domain.SERPSnapshot, error) {
	rows, err := s.queries.ListSERPSnapshotsFiltered(ctx, sqlcgen.ListSERPSnapshotsFilteredParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		Limit:        limit,
		Offset:       offset,
		QueryFilter:  textToPgtype(filter.Query),
		RegionFilter: textToPgtype(filter.Region),
		DateFrom:     timePtrToPgtype(filter.DateFrom),
		DateTo:       timePtrToPgtype(filter.DateTo),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list serp snapshots")
	}

	result := make([]domain.SERPSnapshot, len(rows))
	for i, row := range rows {
		result[i] = serpSnapshotFromSqlc(row)
	}
	return result, nil
}

func (s *SERPService) Get(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPSnapshot, error) {
	row, err := s.queries.GetSERPSnapshotByID(ctx, uuidToPgtype(snapshotID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "serp snapshot not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get serp snapshot")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "serp snapshot not found")
	}

	result := serpSnapshotFromSqlc(row)
	return &result, nil
}

func (s *SERPService) Compare(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPComparison, error) {
	snapshot, err := s.Get(ctx, workspaceID, snapshotID)
	if err != nil {
		return nil, err
	}

	snapshots, err := s.queries.ListSERPSnapshotsFiltered(ctx, sqlcgen.ListSERPSnapshotsFilteredParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		Limit:        200,
		Offset:       0,
		QueryFilter:  textToPgtype(snapshot.Query),
		RegionFilter: textToPgtype(snapshot.Region),
		DateFrom:     pgtype.Timestamptz{},
		DateTo:       pgtype.Timestamptz{},
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load serp snapshots for comparison")
	}

	var previous *sqlcgen.SerpSnapshot
	for i, row := range snapshots {
		if uuidFromPgtype(row.ID) != snapshotID {
			continue
		}
		if i+1 < len(snapshots) {
			previous = &snapshots[i+1]
		}
		break
	}
	if previous == nil {
		return &domain.SERPComparison{}, nil
	}

	currentItems, err := s.ListItems(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	previousItems, err := s.ListItems(ctx, uuidFromPgtype(previous.ID))
	if err != nil {
		return nil, err
	}

	workspaceProducts, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       5000,
		Offset:      0,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load workspace products for serp comparison")
	}

	ownProducts := make(map[int64]struct{}, len(workspaceProducts))
	for _, product := range workspaceProducts {
		ownProducts[product.WbProductID] = struct{}{}
	}

	currentByWBID := make(map[int64]domain.SERPResultItem, len(currentItems))
	previousByWBID := make(map[int64]domain.SERPResultItem, len(previousItems))
	for _, item := range currentItems {
		currentByWBID[item.WBProductID] = item
	}
	for _, item := range previousItems {
		previousByWBID[item.WBProductID] = item
	}

	compare := &domain.SERPComparison{
		PreviousSnapshotID: nullableUUID(uuidFromPgtype(previous.ID)),
		PreviousScannedAt:  pgTimestamptzToTimePtr(previous.ScannedAt),
	}

	for _, item := range currentItems {
		if _, ok := ownProducts[item.WBProductID]; ok {
			compare.CurrentOwnCount++
		}
	}
	for _, item := range previousItems {
		if _, ok := ownProducts[item.WBProductID]; ok {
			compare.PreviousOwnCount++
		}
	}

	newEntrants := make([]domain.SERPCompareItem, 0)
	droppedItems := make([]domain.SERPCompareItem, 0)
	biggestMovers := make([]domain.SERPCompareItem, 0)

	for _, item := range currentItems {
		previousItem, foundPreviously := previousByWBID[item.WBProductID]
		if !foundPreviously {
			compareItem := buildSERPCompareItem(item, nil, ownProducts)
			newEntrants = append(newEntrants, compareItem)
			if compareItem.IsOwnProduct {
				compare.OwnProductsGained++
			}
			continue
		}

		delta := previousItem.Position - item.Position
		if delta != 0 {
			compareItem := buildSERPCompareItem(item, &previousItem, ownProducts)
			biggestMovers = append(biggestMovers, compareItem)
		}
	}

	for _, item := range previousItems {
		if _, stillPresent := currentByWBID[item.WBProductID]; stillPresent {
			continue
		}
		compareItem := buildSERPCompareItem(domain.SERPResultItem{}, &item, ownProducts)
		droppedItems = append(droppedItems, compareItem)
		if compareItem.IsOwnProduct {
			compare.OwnProductsLost++
		}
	}

	sort.Slice(newEntrants, func(i, j int) bool {
		return valueOrMax(newEntrants[i].CurrentPosition) < valueOrMax(newEntrants[j].CurrentPosition)
	})
	sort.Slice(droppedItems, func(i, j int) bool {
		return valueOrMax(droppedItems[i].PreviousPosition) < valueOrMax(droppedItems[j].PreviousPosition)
	})
	sort.Slice(biggestMovers, func(i, j int) bool {
		left := absInt(ptrIntValue(biggestMovers[i].Delta))
		right := absInt(ptrIntValue(biggestMovers[j].Delta))
		if left == right {
			return valueOrMax(biggestMovers[i].CurrentPosition) < valueOrMax(biggestMovers[j].CurrentPosition)
		}
		return left > right
	})

	compare.NewEntrantsCount = len(newEntrants)
	compare.DroppedCount = len(droppedItems)
	compare.NewEntrants = limitSERPCompareItems(newEntrants, 5)
	compare.DroppedItems = limitSERPCompareItems(droppedItems, 5)
	compare.BiggestMovers = limitSERPCompareItems(biggestMovers, 8)

	return compare, nil
}

func (s *SERPService) ListItems(ctx context.Context, snapshotID uuid.UUID) ([]domain.SERPResultItem, error) {
	rows, err := s.queries.ListSERPResultItemsBySnapshot(ctx, uuidToPgtype(snapshotID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list serp result items")
	}

	result := make([]domain.SERPResultItem, len(rows))
	for i, row := range rows {
		result[i] = serpResultItemFromSqlc(row)
	}
	return result, nil
}

func ptrFloat64Value(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func buildSERPCompareItem(current domain.SERPResultItem, previous *domain.SERPResultItem, ownProducts map[int64]struct{}) domain.SERPCompareItem {
	wbProductID := current.WBProductID
	title := current.Title
	var currentPosition *int
	if current.WBProductID != 0 {
		currentPosition = intPtr(current.Position)
	}

	var previousPosition *int
	if previous != nil {
		if wbProductID == 0 {
			wbProductID = previous.WBProductID
		}
		if title == "" {
			title = previous.Title
		}
		previousPosition = intPtr(previous.Position)
	}

	var delta *int
	if currentPosition != nil && previousPosition != nil {
		value := *previousPosition - *currentPosition
		delta = &value
	}

	_, isOwnProduct := ownProducts[wbProductID]
	return domain.SERPCompareItem{
		WBProductID:      wbProductID,
		Title:            title,
		IsOwnProduct:     isOwnProduct,
		CurrentPosition:  currentPosition,
		PreviousPosition: previousPosition,
		Delta:            delta,
	}
}

func limitSERPCompareItems(items []domain.SERPCompareItem, limit int) []domain.SERPCompareItem {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func intPtr(value int) *int {
	return &value
}

func ptrIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func valueOrMax(value *int) int {
	if value == nil {
		return 1<<31 - 1
	}
	return *value
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func pgTimestamptzToTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func (s *SERPService) tryEnqueueRecommendationGeneration(workspaceID uuid.UUID) {
	if s.enqueuer == nil {
		return
	}
	status, err := s.enqueuer.EnqueueRecommendationGeneration(workspaceID)
	if err != nil {
		s.logger.Warn().Err(err).Str("workspace_id", workspaceID.String()).Msg("failed to enqueue recommendation generation after serp ingestion")
		return
	}
	s.logger.Debug().Str("workspace_id", workspaceID.String()).Str("queue_status", status).Msg("enqueued recommendation generation after serp ingestion")
}
