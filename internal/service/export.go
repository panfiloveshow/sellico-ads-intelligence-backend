package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xuri/excelize/v2"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	exportBatchLimit = int32(10000)
	exportDateLayout = "2006-01-02"
)

type ExportEnqueuer interface {
	EnqueueExport(workspaceID, exportID uuid.UUID) error
}

type ExportDownload struct {
	Path        string
	FileName    string
	ContentType string
}

type ExportListFilter struct {
	UserID     *uuid.UUID
	EntityType string
	Format     string
	Status     string
}

type exportFilters struct {
	Title     string `json:"title"`
	Type      string `json:"type"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	ProductID string `json:"product_id"`
	Query     string `json:"query"`
	Region    string `json:"region"`
	DateFrom  string `json:"date_from"`
	DateTo    string `json:"date_to"`
}

type ExportService struct {
	queries     *sqlcgen.Queries
	storagePath string
	enqueuer    ExportEnqueuer
}

func NewExportService(queries *sqlcgen.Queries, storagePath string, enqueuer ExportEnqueuer) *ExportService {
	return &ExportService{
		queries:     queries,
		storagePath: storagePath,
		enqueuer:    enqueuer,
	}
}

func (s *ExportService) List(ctx context.Context, workspaceID uuid.UUID, filter ExportListFilter, limit, offset int32) ([]domain.Export, error) {
	rows, err := s.queries.ListExportsByWorkspace(ctx, sqlcgen.ListExportsByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           offset,
		UserIDFilter:     uuidToPgtypePtr(filter.UserID),
		EntityTypeFilter: textToPgtype(filter.EntityType),
		FormatFilter:     textToPgtype(filter.Format),
		StatusFilter:     textToPgtype(filter.Status),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list exports")
	}

	result := make([]domain.Export, len(rows))
	for i, row := range rows {
		result[i] = exportFromSqlc(row)
	}
	return result, nil
}

func (s *ExportService) Create(ctx context.Context, userID, workspaceID uuid.UUID, entityType, format string, filters json.RawMessage) (*domain.Export, error) {
	if _, err := parseExportFilters(filters); err != nil {
		return nil, apperror.New(apperror.ErrValidation, err.Error())
	}
	if len(filters) == 0 {
		filters = json.RawMessage("{}")
	}

	row, err := s.queries.CreateExport(ctx, sqlcgen.CreateExportParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(userID),
		EntityType:  entityType,
		Format:      format,
		Filters:     filters,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create export")
	}

	result := exportFromSqlc(row)
	if s.enqueuer == nil {
		return &result, nil
	}

	if err := s.enqueuer.EnqueueExport(workspaceID, result.ID); err != nil {
		_, _ = s.queries.UpdateExportStatus(ctx, sqlcgen.UpdateExportStatusParams{
			ID:           uuidToPgtype(result.ID),
			Status:       domain.ExportStatusFailed,
			FilePath:     pgtype.Text{},
			ErrorMessage: textToPgtype("failed to enqueue export"),
		})
		return nil, apperror.New(apperror.ErrInternal, "failed to enqueue export")
	}

	return &result, nil
}

func (s *ExportService) Get(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error) {
	row, err := s.queries.GetExportByID(ctx, uuidToPgtype(exportID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "export not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get export")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "export not found")
	}

	result := exportFromSqlc(row)
	return &result, nil
}

func (s *ExportService) PrepareDownload(ctx context.Context, workspaceID, exportID uuid.UUID) (*ExportDownload, error) {
	exportTask, err := s.Get(ctx, workspaceID, exportID)
	if err != nil {
		return nil, err
	}
	if exportTask.Status != domain.ExportStatusCompleted || exportTask.FilePath == nil || *exportTask.FilePath == "" {
		return nil, apperror.New(apperror.ErrConflict, "export file is not ready")
	}

	absStorage, err := filepath.Abs(s.storagePath)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to resolve export storage")
	}
	absFilePath, err := filepath.Abs(*exportTask.FilePath)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to resolve export file path")
	}
	storagePrefix := absStorage + string(os.PathSeparator)
	if absFilePath != absStorage && !strings.HasPrefix(absFilePath, storagePrefix) {
		return nil, apperror.New(apperror.ErrInternal, "export file path is outside storage")
	}
	if _, err := os.Stat(absFilePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, apperror.New(apperror.ErrNotFound, "export file not found")
		}
		return nil, apperror.New(apperror.ErrInternal, "failed to access export file")
	}

	return &ExportDownload{
		Path:        absFilePath,
		FileName:    fmt.Sprintf("export-%s.%s", exportTask.ID.String(), exportTask.Format),
		ContentType: exportContentType(exportTask.Format),
	}, nil
}

type ExportGenerator struct {
	queries     *sqlcgen.Queries
	storagePath string
}

func NewExportGenerator(queries *sqlcgen.Queries, storagePath string) *ExportGenerator {
	return &ExportGenerator{
		queries:     queries,
		storagePath: storagePath,
	}
}

func (g *ExportGenerator) Generate(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error) {
	exportTask, err := g.getExport(ctx, workspaceID, exportID)
	if err != nil {
		return nil, err
	}
	if exportTask.Status == domain.ExportStatusCompleted && exportTask.FilePath != nil {
		if _, statErr := os.Stat(*exportTask.FilePath); statErr == nil {
			return exportTask, nil
		}
	}

	if _, err := g.queries.UpdateExportStatus(ctx, sqlcgen.UpdateExportStatusParams{
		ID:           uuidToPgtype(exportID),
		Status:       domain.ExportStatusProcessing,
		FilePath:     pgtype.Text{},
		ErrorMessage: pgtype.Text{},
	}); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to mark export as processing")
	}

	headers, rows, err := g.buildRows(ctx, exportTask)
	if err != nil {
		g.markFailed(ctx, exportID, err)
		return nil, err
	}

	filePath, err := g.writeFile(workspaceID, exportID, exportTask.Format, headers, rows)
	if err != nil {
		g.markFailed(ctx, exportID, err)
		return nil, err
	}

	row, err := g.queries.UpdateExportStatus(ctx, sqlcgen.UpdateExportStatusParams{
		ID:           uuidToPgtype(exportID),
		Status:       domain.ExportStatusCompleted,
		FilePath:     textToPgtype(filePath),
		ErrorMessage: pgtype.Text{},
	})
	if err != nil {
		_ = os.Remove(filePath)
		return nil, apperror.New(apperror.ErrInternal, "failed to finalize export")
	}

	result := exportFromSqlc(row)
	return &result, nil
}

func (g *ExportGenerator) getExport(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error) {
	row, err := g.queries.GetExportByID(ctx, uuidToPgtype(exportID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "export not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get export")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "export not found")
	}

	result := exportFromSqlc(row)
	return &result, nil
}

func (g *ExportGenerator) markFailed(ctx context.Context, exportID uuid.UUID, cause error) {
	_, _ = g.queries.UpdateExportStatus(ctx, sqlcgen.UpdateExportStatusParams{
		ID:           uuidToPgtype(exportID),
		Status:       domain.ExportStatusFailed,
		FilePath:     pgtype.Text{},
		ErrorMessage: textToPgtype(cause.Error()),
	})
}

func (g *ExportGenerator) buildRows(ctx context.Context, exportTask *domain.Export) ([]string, [][]string, error) {
	filters, err := parseExportFilters(exportTask.Filters)
	if err != nil {
		return nil, nil, apperror.New(apperror.ErrValidation, err.Error())
	}

	switch exportTask.EntityType {
	case "campaigns":
		rows, err := g.queries.ListCampaignsByWorkspace(ctx, sqlcgen.ListCampaignsByWorkspaceParams{
			WorkspaceID: uuidToPgtype(exportTask.WorkspaceID),
			Limit:       exportBatchLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list campaigns for export")
		}
		result := make([][]string, len(rows))
		for i, row := range rows {
			campaign := campaignFromSqlc(row)
			result[i] = []string{
				campaign.ID.String(),
				campaign.WorkspaceID.String(),
				campaign.SellerCabinetID.String(),
				fmt.Sprintf("%d", campaign.WBCampaignID),
				campaign.Name,
				campaign.Status,
				fmt.Sprintf("%d", campaign.CampaignType),
				campaign.BidType,
				campaign.PaymentType,
				int64PtrToString(campaign.DailyBudget),
				campaign.CreatedAt.UTC().Format(time.RFC3339),
				campaign.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "workspace_id", "seller_cabinet_id", "wb_campaign_id", "name", "status", "campaign_type", "bid_type", "payment_type", "daily_budget", "created_at", "updated_at"}, result, nil
	case "campaign_stats":
		rows, err := g.queries.ListCampaignStatsByWorkspace(ctx, sqlcgen.ListCampaignStatsByWorkspaceParams{
			WorkspaceID: uuidToPgtype(exportTask.WorkspaceID),
			Limit:       exportBatchLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list campaign stats for export")
		}
		filtered := filterCampaignStatsByDate(rows, filters)
		result := make([][]string, len(filtered))
		for i, row := range filtered {
			stat := campaignStatFromLatestSqlc(row)
			result[i] = []string{
				stat.ID.String(),
				stat.CampaignID.String(),
				stat.Date.Format(exportDateLayout),
				fmt.Sprintf("%d", stat.Impressions),
				fmt.Sprintf("%d", stat.Clicks),
				fmt.Sprintf("%d", stat.Spend),
				int64PtrToString(stat.Orders),
				int64PtrToString(stat.Revenue),
				stat.CreatedAt.UTC().Format(time.RFC3339),
				stat.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "campaign_id", "date", "impressions", "clicks", "spend", "orders", "revenue", "created_at", "updated_at"}, result, nil
	case "phrases":
		rows, err := g.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
			WorkspaceID: uuidToPgtype(exportTask.WorkspaceID),
			Limit:       exportBatchLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list phrases for export")
		}
		result := make([][]string, len(rows))
		for i, row := range rows {
			phrase := phraseFromSqlc(row)
			result[i] = []string{
				phrase.ID.String(),
				phrase.CampaignID.String(),
				phrase.WorkspaceID.String(),
				fmt.Sprintf("%d", phrase.WBClusterID),
				phrase.Keyword,
				intPtrToString(phrase.Count),
				int64PtrToString(phrase.CurrentBid),
				phrase.CreatedAt.UTC().Format(time.RFC3339),
				phrase.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "campaign_id", "workspace_id", "wb_cluster_id", "keyword", "count", "current_bid", "created_at", "updated_at"}, result, nil
	case "phrase_stats":
		rows, err := g.queries.ListPhraseStatsByWorkspace(ctx, sqlcgen.ListPhraseStatsByWorkspaceParams{
			WorkspaceID: uuidToPgtype(exportTask.WorkspaceID),
			Limit:       exportBatchLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list phrase stats for export")
		}
		filtered := filterPhraseStatsByDate(rows, filters)
		result := make([][]string, len(filtered))
		for i, row := range filtered {
			stat := phraseStatFromSqlc(row)
			result[i] = []string{
				stat.ID.String(),
				stat.PhraseID.String(),
				stat.Date.Format(exportDateLayout),
				fmt.Sprintf("%d", stat.Impressions),
				fmt.Sprintf("%d", stat.Clicks),
				fmt.Sprintf("%d", stat.Spend),
				stat.CreatedAt.UTC().Format(time.RFC3339),
				stat.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "phrase_id", "date", "impressions", "clicks", "spend", "created_at", "updated_at"}, result, nil
	case "products":
		rows, err := g.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
			WorkspaceID: uuidToPgtype(exportTask.WorkspaceID),
			Limit:       exportBatchLimit,
			Offset:      0,
			TitleFilter: textToPgtype(filters.Title),
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list products for export")
		}
		result := make([][]string, len(rows))
		for i, row := range rows {
			product := productFromSqlc(row)
			result[i] = []string{
				product.ID.String(),
				product.WorkspaceID.String(),
				product.SellerCabinetID.String(),
				fmt.Sprintf("%d", product.WBProductID),
				product.Title,
				stringPtrToString(product.Brand),
				stringPtrToString(product.Category),
				stringPtrToString(product.ImageURL),
				int64PtrToString(product.Price),
				product.CreatedAt.UTC().Format(time.RFC3339),
				product.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "workspace_id", "seller_cabinet_id", "wb_product_id", "title", "brand", "category", "image_url", "price", "created_at", "updated_at"}, result, nil
	case "positions":
		productID, err := filters.productUUID()
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrValidation, err.Error())
		}
		dateFrom, dateTo, err := filters.dateRange()
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrValidation, err.Error())
		}
		rows, err := g.queries.ListPositionsFiltered(ctx, sqlcgen.ListPositionsFilteredParams{
			WorkspaceID:     uuidToPgtype(exportTask.WorkspaceID),
			Limit:           exportBatchLimit,
			Offset:          0,
			ProductIDFilter: uuidToPgtypePtr(productID),
			QueryFilter:     textToPgtype(filters.Query),
			RegionFilter:    textToPgtype(filters.Region),
			DateFrom:        timePtrToPgtype(dateFrom),
			DateTo:          timePtrToPgtype(dateTo),
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list positions for export")
		}
		result := make([][]string, len(rows))
		for i, row := range rows {
			position := positionFromSqlc(row)
			result[i] = []string{
				position.ID.String(),
				position.WorkspaceID.String(),
				position.ProductID.String(),
				position.Query,
				position.Region,
				fmt.Sprintf("%d", position.Position),
				fmt.Sprintf("%d", position.Page),
				position.Source,
				position.CheckedAt.UTC().Format(time.RFC3339),
				position.CreatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "workspace_id", "product_id", "query", "region", "position", "page", "source", "checked_at", "created_at"}, result, nil
	case "recommendations":
		rows, err := g.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
			WorkspaceID:    uuidToPgtype(exportTask.WorkspaceID),
			Limit:          exportBatchLimit,
			Offset:         0,
			TypeFilter:     textToPgtype(filters.Type),
			SeverityFilter: textToPgtype(filters.Severity),
			StatusFilter:   textToPgtype(filters.Status),
		})
		if err != nil {
			return nil, nil, apperror.New(apperror.ErrInternal, "failed to list recommendations for export")
		}
		result := make([][]string, len(rows))
		for i, row := range rows {
			recommendation := recommendationFromSqlc(row)
			result[i] = []string{
				recommendation.ID.String(),
				recommendation.WorkspaceID.String(),
				uuidPtrToString(recommendation.CampaignID),
				uuidPtrToString(recommendation.PhraseID),
				uuidPtrToString(recommendation.ProductID),
				recommendation.Title,
				recommendation.Description,
				recommendation.Type,
				recommendation.Severity,
				fmt.Sprintf("%.2f", recommendation.Confidence),
				string(recommendation.SourceMetrics),
				stringPtrToString(recommendation.NextAction),
				recommendation.Status,
				recommendation.CreatedAt.UTC().Format(time.RFC3339),
				recommendation.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		return []string{"id", "workspace_id", "campaign_id", "phrase_id", "product_id", "title", "description", "type", "severity", "confidence", "source_metrics", "next_action", "status", "created_at", "updated_at"}, result, nil
	default:
		return nil, nil, apperror.New(apperror.ErrValidation, "unsupported export entity type")
	}
}

func (g *ExportGenerator) writeFile(workspaceID, exportID uuid.UUID, format string, headers []string, rows [][]string) (string, error) {
	basePath, err := filepath.Abs(g.storagePath)
	if err != nil {
		return "", apperror.New(apperror.ErrInternal, "failed to resolve export storage")
	}
	exportDir := filepath.Join(basePath, workspaceID.String())
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return "", apperror.New(apperror.ErrInternal, "failed to prepare export storage")
	}

	filePath := filepath.Join(exportDir, fmt.Sprintf("%s.%s", exportID.String(), format))
	switch format {
	case domain.ExportFormatCSV:
		if err := writeCSVFile(filePath, headers, rows); err != nil {
			return "", err
		}
	case domain.ExportFormatXLSX:
		if err := writeXLSXFile(filePath, headers, rows); err != nil {
			return "", err
		}
	default:
		return "", apperror.New(apperror.ErrValidation, "unsupported export format")
	}

	return filePath, nil
}

func parseExportFilters(raw json.RawMessage) (exportFilters, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return exportFilters{}, nil
	}

	var filters exportFilters
	if err := json.Unmarshal(raw, &filters); err != nil {
		return exportFilters{}, fmt.Errorf("invalid export filters")
	}
	if _, err := filters.productUUID(); err != nil {
		return exportFilters{}, err
	}
	if _, _, err := filters.dateRange(); err != nil {
		return exportFilters{}, err
	}
	return filters, nil
}

func (f exportFilters) productUUID() (*uuid.UUID, error) {
	if f.ProductID == "" {
		return nil, nil
	}
	productID, err := uuid.Parse(f.ProductID)
	if err != nil {
		return nil, fmt.Errorf("invalid product_id filter")
	}
	return &productID, nil
}

func (f exportFilters) dateRange() (*time.Time, *time.Time, error) {
	var dateFrom *time.Time
	var dateTo *time.Time
	if f.DateFrom != "" {
		parsed, err := time.Parse(exportDateLayout, f.DateFrom)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid date_from filter")
		}
		dateFrom = &parsed
	}
	if f.DateTo != "" {
		parsed, err := time.Parse(exportDateLayout, f.DateTo)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid date_to filter")
		}
		dateTo = &parsed
	}
	return dateFrom, dateTo, nil
}

func filterCampaignStatsByDate(rows []sqlcgen.CampaignStat, filters exportFilters) []sqlcgen.CampaignStat {
	dateFrom, dateTo, err := filters.dateRange()
	if err != nil || (dateFrom == nil && dateTo == nil) {
		return rows
	}
	filtered := make([]sqlcgen.CampaignStat, 0, len(rows))
	for _, row := range rows {
		if dateFrom != nil && row.Date.Time.Before(*dateFrom) {
			continue
		}
		if dateTo != nil && row.Date.Time.After(*dateTo) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func filterPhraseStatsByDate(rows []sqlcgen.PhraseStat, filters exportFilters) []sqlcgen.PhraseStat {
	dateFrom, dateTo, err := filters.dateRange()
	if err != nil || (dateFrom == nil && dateTo == nil) {
		return rows
	}
	filtered := make([]sqlcgen.PhraseStat, 0, len(rows))
	for _, row := range rows {
		if dateFrom != nil && row.Date.Time.Before(*dateFrom) {
			continue
		}
		if dateTo != nil && row.Date.Time.After(*dateTo) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func exportContentType(format string) string {
	switch format {
	case domain.ExportFormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	default:
		return "text/csv; charset=utf-8"
	}
}

func writeCSVFile(path string, headers []string, rows [][]string) error {
	file, err := os.Create(path)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to create export file")
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write(headers); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to write export file")
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return apperror.New(apperror.ErrInternal, "failed to write export file")
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to write export file")
	}
	return nil
}

func writeXLSXFile(path string, headers []string, rows [][]string) error {
	file := excelize.NewFile()
	defer func() {
		_ = file.Close()
	}()

	const sheetName = "Export"
	file.SetSheetName(file.GetSheetName(0), sheetName)

	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		if err := file.SetCellValue(sheetName, cell, header); err != nil {
			return apperror.New(apperror.ErrInternal, "failed to prepare export workbook")
		}
	}
	for rowIdx, row := range rows {
		for colIdx, value := range row {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			if err := file.SetCellValue(sheetName, cell, value); err != nil {
				return apperror.New(apperror.ErrInternal, "failed to prepare export workbook")
			}
		}
	}

	if err := file.SaveAs(path); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to save export workbook")
	}
	return nil
}

func stringPtrToString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func intPtrToString(value *int) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%d", *value)
}

func int64PtrToString(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%d", *value)
}

func uuidPtrToString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}
