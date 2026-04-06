package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type KeywordRow struct {
	ID             pgtype.UUID        `json:"id"`
	WorkspaceID    pgtype.UUID        `json:"workspace_id"`
	Query          string             `json:"query"`
	Normalized     string             `json:"normalized"`
	Frequency      int32              `json:"frequency"`
	FrequencyTrend string             `json:"frequency_trend"`
	ClusterID      pgtype.UUID        `json:"cluster_id"`
	Source         string             `json:"source"`
	CreatedAt      pgtype.Timestamptz `json:"created_at"`
	UpdatedAt      pgtype.Timestamptz `json:"updated_at"`
}

type UpsertKeywordParams struct {
	WorkspaceID pgtype.UUID
	Query       string
	Normalized  string
	Frequency   int32
	Source      string
}

func (q *Queries) UpsertKeyword(ctx context.Context, arg UpsertKeywordParams) (KeywordRow, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO keywords (workspace_id, query, normalized, frequency, source)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (workspace_id, normalized) DO UPDATE SET
			frequency = EXCLUDED.frequency,
			frequency_trend = CASE
				WHEN keywords.frequency < EXCLUDED.frequency THEN 'rising'
				WHEN keywords.frequency > EXCLUDED.frequency THEN 'falling'
				ELSE 'stable'
			END,
			updated_at = now()
		RETURNING id, workspace_id, query, normalized, frequency, frequency_trend, cluster_id, source, created_at, updated_at`,
		arg.WorkspaceID, arg.Query, arg.Normalized, arg.Frequency, arg.Source)
	var i KeywordRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.Query, &i.Normalized, &i.Frequency, &i.FrequencyTrend, &i.ClusterID, &i.Source, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type ListKeywordsParams struct {
	WorkspaceID pgtype.UUID
	Search      pgtype.Text
	Limit       int32
	Offset      int32
}

func (q *Queries) ListKeywords(ctx context.Context, arg ListKeywordsParams) ([]KeywordRow, error) {
	baseQuery := `SELECT id, workspace_id, query, normalized, frequency, frequency_trend, cluster_id, source, created_at, updated_at FROM keywords WHERE workspace_id = $1`
	var args []any
	args = append(args, arg.WorkspaceID)

	if arg.Search.Valid && arg.Search.String != "" {
		baseQuery += ` AND normalized ILIKE $2 ORDER BY frequency DESC LIMIT $3 OFFSET $4`
		args = append(args, "%"+arg.Search.String+"%", arg.Limit, arg.Offset)
	} else {
		baseQuery += ` ORDER BY frequency DESC LIMIT $2 OFFSET $3`
		args = append(args, arg.Limit, arg.Offset)
	}

	rows, err := q.db.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []KeywordRow
	for rows.Next() {
		var i KeywordRow
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.Query, &i.Normalized, &i.Frequency, &i.FrequencyTrend, &i.ClusterID, &i.Source, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) CreateFrequencyHistory(ctx context.Context, keywordID pgtype.UUID, frequency int32) error {
	_, err := q.db.Exec(ctx, `INSERT INTO keyword_frequency_history (keyword_id, frequency) VALUES ($1, $2)`, keywordID, frequency)
	return err
}

type FrequencyHistoryRow struct {
	ID        pgtype.UUID        `json:"id"`
	KeywordID pgtype.UUID        `json:"keyword_id"`
	Frequency int32              `json:"frequency"`
	CheckedAt pgtype.Timestamptz `json:"checked_at"`
}

func (q *Queries) ListFrequencyHistory(ctx context.Context, keywordID pgtype.UUID, limit int32) ([]FrequencyHistoryRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, keyword_id, frequency, checked_at FROM keyword_frequency_history WHERE keyword_id = $1 ORDER BY checked_at DESC LIMIT $2`,
		keywordID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []FrequencyHistoryRow
	for rows.Next() {
		var i FrequencyHistoryRow
		if err := rows.Scan(&i.ID, &i.KeywordID, &i.Frequency, &i.CheckedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type KeywordClusterRow struct {
	ID             pgtype.UUID        `json:"id"`
	WorkspaceID    pgtype.UUID        `json:"workspace_id"`
	Name           string             `json:"name"`
	MainKeyword    string             `json:"main_keyword"`
	KeywordCount   int32              `json:"keyword_count"`
	TotalFrequency int32              `json:"total_frequency"`
	CreatedAt      pgtype.Timestamptz `json:"created_at"`
	UpdatedAt      pgtype.Timestamptz `json:"updated_at"`
}

type CreateKeywordClusterParams struct {
	WorkspaceID pgtype.UUID
	Name        string
	MainKeyword string
}

func (q *Queries) CreateKeywordCluster(ctx context.Context, arg CreateKeywordClusterParams) (KeywordClusterRow, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO keyword_clusters (workspace_id, name, main_keyword) VALUES ($1,$2,$3)
		RETURNING id, workspace_id, name, main_keyword, keyword_count, total_frequency, created_at, updated_at`,
		arg.WorkspaceID, arg.Name, arg.MainKeyword)
	var i KeywordClusterRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.Name, &i.MainKeyword, &i.KeywordCount, &i.TotalFrequency, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) ListKeywordClusters(ctx context.Context, workspaceID pgtype.UUID, limit, offset int32) ([]KeywordClusterRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, workspace_id, name, main_keyword, keyword_count, total_frequency, created_at, updated_at
		FROM keyword_clusters WHERE workspace_id = $1 ORDER BY total_frequency DESC LIMIT $2 OFFSET $3`,
		workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []KeywordClusterRow
	for rows.Next() {
		var i KeywordClusterRow
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.Name, &i.MainKeyword, &i.KeywordCount, &i.TotalFrequency, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) AssignKeywordToCluster(ctx context.Context, keywordID, clusterID pgtype.UUID) error {
	_, err := q.db.Exec(ctx, `UPDATE keywords SET cluster_id = $2, updated_at = now() WHERE id = $1`, keywordID, clusterID)
	return err
}

type SEOAnalysisRow struct {
	ID                pgtype.UUID        `json:"id"`
	WorkspaceID       pgtype.UUID        `json:"workspace_id"`
	ProductID         pgtype.UUID        `json:"product_id"`
	TitleScore        int32              `json:"title_score"`
	DescriptionScore  int32              `json:"description_score"`
	KeywordsScore     int32              `json:"keywords_score"`
	OverallScore      int32              `json:"overall_score"`
	TitleIssues       []byte             `json:"title_issues"`
	DescriptionIssues []byte             `json:"description_issues"`
	KeywordCoverage   []byte             `json:"keyword_coverage"`
	Recommendations   []byte             `json:"recommendations"`
	AnalyzedAt        pgtype.Timestamptz `json:"analyzed_at"`
}

type CreateSEOAnalysisParams struct {
	WorkspaceID       pgtype.UUID
	ProductID         pgtype.UUID
	TitleScore        int32
	DescriptionScore  int32
	KeywordsScore     int32
	OverallScore      int32
	TitleIssues       []byte
	DescriptionIssues []byte
	KeywordCoverage   []byte
	Recommendations   []byte
}

func (q *Queries) CreateSEOAnalysis(ctx context.Context, arg CreateSEOAnalysisParams) (SEOAnalysisRow, error) {
	row := q.db.QueryRow(ctx,
		`INSERT INTO seo_analyses (workspace_id, product_id, title_score, description_score, keywords_score, overall_score, title_issues, description_issues, keyword_coverage, recommendations)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, workspace_id, product_id, title_score, description_score, keywords_score, overall_score, title_issues, description_issues, keyword_coverage, recommendations, analyzed_at`,
		arg.WorkspaceID, arg.ProductID, arg.TitleScore, arg.DescriptionScore, arg.KeywordsScore, arg.OverallScore, arg.TitleIssues, arg.DescriptionIssues, arg.KeywordCoverage, arg.Recommendations)
	var i SEOAnalysisRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.TitleScore, &i.DescriptionScore, &i.KeywordsScore, &i.OverallScore, &i.TitleIssues, &i.DescriptionIssues, &i.KeywordCoverage, &i.Recommendations, &i.AnalyzedAt)
	return i, err
}

func (q *Queries) GetLatestSEOAnalysis(ctx context.Context, productID pgtype.UUID) (SEOAnalysisRow, error) {
	row := q.db.QueryRow(ctx,
		`SELECT id, workspace_id, product_id, title_score, description_score, keywords_score, overall_score, title_issues, description_issues, keyword_coverage, recommendations, analyzed_at
		FROM seo_analyses WHERE product_id = $1 ORDER BY analyzed_at DESC LIMIT 1`, productID)
	var i SEOAnalysisRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.TitleScore, &i.DescriptionScore, &i.KeywordsScore, &i.OverallScore, &i.TitleIssues, &i.DescriptionIssues, &i.KeywordCoverage, &i.Recommendations, &i.AnalyzedAt)
	return i, err
}
