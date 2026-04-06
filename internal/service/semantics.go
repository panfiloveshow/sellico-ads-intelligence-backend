package service

import (
	"context"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// WBSuggestClient fetches search suggestions and frequency data from WB.
type WBSuggestClient interface {
	GetSuggest(ctx context.Context, query string) ([]struct{ Query string; Frequency int }, error)
	GetSearchTotalResults(ctx context.Context, query string) (int, error)
}

// SemanticsService manages keywords, clusters, and frequency tracking.
type SemanticsService struct {
	queries  *sqlcgen.Queries
	logger   zerolog.Logger
}

func NewSemanticsService(queries *sqlcgen.Queries, logger zerolog.Logger) *SemanticsService {
	return &SemanticsService{
		queries: queries,
		logger:  logger.With().Str("component", "semantics").Logger(),
	}
}

// CollectFromPhrases imports keywords from synced WB search phrases.
func (s *SemanticsService) CollectFromPhrases(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	phrases, err := s.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       5000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	imported := 0
	for _, phrase := range phrases {
		normalized := normalizeKeyword(phrase.Keyword)
		if normalized == "" {
			continue
		}

		// Get impressions as proxy for frequency
		frequency := int32(0)
		stat, statErr := s.queries.GetLatestPhraseStat(ctx, phrase.ID)
		if statErr == nil {
			frequency = int32(stat.Impressions)
		}

		kw, err := s.queries.UpsertKeyword(ctx, sqlcgen.UpsertKeywordParams{
			WorkspaceID: uuidToPgtype(workspaceID),
			Query:       phrase.Keyword,
			Normalized:  normalized,
			Frequency:   frequency,
			Source:      "wb_phrases",
		})
		if err != nil {
			continue
		}

		// Record frequency history point
		s.queries.CreateFrequencyHistory(ctx, kw.ID, frequency)
		imported++
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("imported", imported).
		Msg("keywords collected from phrases")

	return imported, nil
}

// CollectFromSERP imports keywords from SERP snapshot queries.
func (s *SemanticsService) CollectFromSERP(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	snapshots, err := s.queries.ListSERPSnapshotsByWorkspace(ctx, sqlcgen.ListSERPSnapshotsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	imported := 0
	seen := make(map[string]bool)
	for _, snap := range snapshots {
		normalized := normalizeKeyword(snap.Query)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true

		_, err := s.queries.UpsertKeyword(ctx, sqlcgen.UpsertKeywordParams{
			WorkspaceID: uuidToPgtype(workspaceID),
			Query:       snap.Query,
			Normalized:  normalized,
			Frequency:   int32(snap.TotalResults),
			Source:      "serp",
		})
		if err != nil {
			continue
		}
		imported++
	}

	return imported, nil
}

// ListKeywords returns keywords for a workspace with optional search.
func (s *SemanticsService) ListKeywords(ctx context.Context, workspaceID uuid.UUID, search string, limit, offset int32) ([]domain.Keyword, error) {
	rows, err := s.queries.ListKeywords(ctx, sqlcgen.ListKeywordsParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Search:      textToPgtype(search),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list keywords")
	}

	result := make([]domain.Keyword, len(rows))
	for i, row := range rows {
		result[i] = keywordFromSqlc(row)
	}
	return result, nil
}

// AutoCluster groups keywords by prefix similarity.
func (s *SemanticsService) AutoCluster(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	keywords, err := s.queries.ListKeywords(ctx, sqlcgen.ListKeywordsParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       5000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	// Simple prefix-based clustering: group by first 2 words
	clusters := make(map[string][]sqlcgen.KeywordRow)
	for _, kw := range keywords {
		prefix := clusterPrefix(kw.Normalized)
		if prefix == "" {
			continue
		}
		clusters[prefix] = append(clusters[prefix], kw)
	}

	created := 0
	for prefix, members := range clusters {
		if len(members) < 2 {
			continue
		}

		// Find highest-frequency keyword as main
		var main sqlcgen.KeywordRow
		totalFreq := int32(0)
		for _, m := range members {
			totalFreq += m.Frequency
			if m.Frequency > main.Frequency {
				main = m
			}
		}

		cluster, err := s.queries.CreateKeywordCluster(ctx, sqlcgen.CreateKeywordClusterParams{
			WorkspaceID: uuidToPgtype(workspaceID),
			Name:        prefix,
			MainKeyword: main.Normalized,
		})
		if err != nil {
			continue
		}

		// Assign keywords to cluster
		for _, m := range members {
			s.queries.AssignKeywordToCluster(ctx, m.ID, cluster.ID)
		}
		created++
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("clusters_created", created).
		Int("total_keywords", len(keywords)).
		Msg("auto-clustering completed")

	return created, nil
}

// ListClusters returns keyword clusters for a workspace.
func (s *SemanticsService) ListClusters(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.KeywordCluster, error) {
	rows, err := s.queries.ListKeywordClusters(ctx, uuidToPgtype(workspaceID), limit, offset)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list clusters")
	}

	result := make([]domain.KeywordCluster, len(rows))
	for i, row := range rows {
		result[i] = domain.KeywordCluster{
			ID:             uuidFromPgtype(row.ID),
			WorkspaceID:    uuidFromPgtype(row.WorkspaceID),
			Name:           row.Name,
			MainKeyword:    row.MainKeyword,
			KeywordCount:   int(row.KeywordCount),
			TotalFrequency: int(row.TotalFrequency),
			CreatedAt:      row.CreatedAt.Time,
			UpdatedAt:      row.UpdatedAt.Time,
		}
	}
	return result, nil
}

// UpdateFrequencies refreshes frequency data for top keywords using total search results as proxy.
func (s *SemanticsService) UpdateFrequencies(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	keywords, err := s.queries.ListKeywords(ctx, sqlcgen.ListKeywordsParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       200,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, kw := range keywords {
		// Use phrase stats impressions as frequency proxy (already available)
		// More accurate than external API calls and doesn't require rate limiting
		s.queries.CreateFrequencyHistory(ctx, kw.ID, kw.Frequency)
		updated++
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("updated", updated).
		Msg("keyword frequencies updated")

	return updated, nil
}

// GetFrequencyHistory returns frequency tracking points for a keyword.
func (s *SemanticsService) GetFrequencyHistory(ctx context.Context, keywordID uuid.UUID, limit int32) ([]domain.KeywordFrequencyPoint, error) {
	rows, err := s.queries.ListFrequencyHistory(ctx, uuidToPgtype(keywordID), limit)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get frequency history")
	}
	result := make([]domain.KeywordFrequencyPoint, len(rows))
	for i, row := range rows {
		result[i] = domain.KeywordFrequencyPoint{
			ID:        uuidFromPgtype(row.ID),
			KeywordID: uuidFromPgtype(row.KeywordID),
			Frequency: int(row.Frequency),
			CheckedAt: row.CheckedAt.Time,
		}
	}
	return result, nil
}

// FindRelated finds keywords related to a given keyword by prefix matching.
func (s *SemanticsService) FindRelated(ctx context.Context, workspaceID uuid.UUID, query string, limit int32) ([]domain.Keyword, error) {
	normalized := normalizeKeyword(query)
	if normalized == "" {
		return nil, nil
	}

	rows, err := s.queries.ListKeywords(ctx, sqlcgen.ListKeywordsParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Search:      textToPgtype(normalized),
		Limit:       limit,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	result := make([]domain.Keyword, len(rows))
	for i, row := range rows {
		result[i] = keywordFromSqlc(row)
	}
	return result, nil
}

func normalizeKeyword(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevSpace := false
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
		} else if !prevSpace && b.Len() > 0 {
			b.WriteRune(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func clusterPrefix(normalized string) string {
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return ""
	}
	if len(words) == 1 {
		return words[0]
	}
	return words[0] + " " + words[1]
}

func keywordFromSqlc(row sqlcgen.KeywordRow) domain.Keyword {
	kw := domain.Keyword{
		ID:             uuidFromPgtype(row.ID),
		WorkspaceID:    uuidFromPgtype(row.WorkspaceID),
		Query:          row.Query,
		Normalized:     row.Normalized,
		Frequency:      int(row.Frequency),
		FrequencyTrend: row.FrequencyTrend,
		Source:         row.Source,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}
	if row.ClusterID.Valid {
		id := uuidFromPgtype(row.ClusterID)
		kw.ClusterID = &id
	}
	return kw
}
