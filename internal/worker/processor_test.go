package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

type fakeSyncRunner struct {
	listWorkspaceCabinetIDsFn func(ctx context.Context, workspaceID uuid.UUID) ([]uuid.UUID, error)
	syncWorkspaceFn           func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncCampaignsFn           func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncCampaignStatsFn       func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncPhrasesFn             func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncProductsFn            func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncSingleCabinetFn       func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (service.SyncSummary, error)
	syncCabinetPhaseFn        func(ctx context.Context, workspaceID, cabinetID uuid.UUID, phase string) (service.SyncSummary, error)
}

func (f *fakeSyncRunner) ListWorkspaceCabinetIDs(ctx context.Context, workspaceID uuid.UUID) ([]uuid.UUID, error) {
	if f.listWorkspaceCabinetIDsFn == nil {
		return nil, errors.New("unexpected ListWorkspaceCabinetIDs call")
	}
	return f.listWorkspaceCabinetIDsFn(ctx, workspaceID)
}

func (f *fakeSyncRunner) SyncWorkspace(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error) {
	if f.syncWorkspaceFn == nil {
		return service.SyncSummary{}, errors.New("unexpected SyncWorkspace call")
	}
	return f.syncWorkspaceFn(ctx, workspaceID)
}

type fakeBidAutomationRunner struct {
	changes          int
	err              error
	called           bool
	reconcileSummary service.AutomationBidReconciliationSummary
	reconcileErr     error
	reconcileCalled  bool
}

func (f *fakeBidAutomationRunner) RunForWorkspace(_ context.Context, _ uuid.UUID) (int, error) {
	f.called = true
	return f.changes, f.err
}

func (f *fakeBidAutomationRunner) ReconcileStaleBidActions(_ context.Context, _ time.Time, _ int32) (service.AutomationBidReconciliationSummary, error) {
	f.reconcileCalled = true
	return f.reconcileSummary, f.reconcileErr
}

func (f *fakeSyncRunner) SyncCampaigns(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error) {
	if f.syncCampaignsFn == nil {
		return service.SyncSummary{}, errors.New("unexpected SyncCampaigns call")
	}
	return f.syncCampaignsFn(ctx, workspaceID)
}

func (f *fakeSyncRunner) SyncCampaignStats(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error) {
	if f.syncCampaignStatsFn == nil {
		return service.SyncSummary{}, errors.New("unexpected SyncCampaignStats call")
	}
	return f.syncCampaignStatsFn(ctx, workspaceID)
}

func (f *fakeSyncRunner) SyncPhrases(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error) {
	if f.syncPhrasesFn == nil {
		return service.SyncSummary{}, errors.New("unexpected SyncPhrases call")
	}
	return f.syncPhrasesFn(ctx, workspaceID)
}

func (f *fakeSyncRunner) SyncProducts(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error) {
	return f.syncProductsFn(ctx, workspaceID)
}

func (f *fakeSyncRunner) SyncSingleCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (service.SyncSummary, error) {
	if f.syncSingleCabinetFn == nil {
		return service.SyncSummary{}, errors.New("unexpected SyncSingleCabinet call")
	}
	return f.syncSingleCabinetFn(ctx, workspaceID, cabinetID)
}

func (f *fakeSyncRunner) SyncSingleCabinetPhase(ctx context.Context, workspaceID, cabinetID uuid.UUID, phase string) (service.SyncSummary, error) {
	if f.syncCabinetPhaseFn == nil {
		return service.SyncSummary{}, errors.New("unexpected SyncSingleCabinetPhase call")
	}
	return f.syncCabinetPhaseFn(ctx, workspaceID, cabinetID, phase)
}

func TestRateLimitedPhasesMapsIssuesToRetryableSingleCabinetPhases(t *testing.T) {
	summary := service.SyncSummary{
		RateLimited:       3,
		RateLimitEndpoint: "adv_normquery_stats",
		Issues: []service.SyncIssue{
			{Stage: "stats", Message: "sync stats: rate limited (429)"},
			{Stage: "phrases", Message: "sync phrases: rate limited (429)"},
			{Stage: "products", Message: "catalog timeout"},
			{Stage: "sales_funnel_products", Message: "too many requests"},
		},
	}

	assert.Equal(t, []string{
		service.SyncPhaseStats,
		service.SyncPhasePhrases,
		service.SyncPhaseSalesFunnel,
	}, rateLimitedPhases(summary))
}

type enqueuedTask struct {
	taskType string
	payload  []byte
}

type fakeTaskEnqueuer struct {
	tasks []enqueuedTask
	err   error
}

func (f *fakeTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	f.tasks = append(f.tasks, enqueuedTask{
		taskType: task.Type(),
		payload:  append([]byte(nil), task.Payload()...),
	})
	if f.err != nil {
		return nil, f.err
	}
	return &asynq.TaskInfo{}, nil
}

type fakeAdsReportReader struct {
	overviewFn func(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error)
}

func (f *fakeAdsReportReader) Overview(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error) {
	return f.overviewFn(ctx, workspaceID, dateFrom, dateTo, filter...)
}

type fakeReportRecommendationLister struct {
	listFn func(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

func (f *fakeReportRecommendationLister) List(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	return f.listFn(ctx, workspaceID, filter, limit, offset)
}

type fakeReportNotifier struct {
	called          bool
	delivery        service.NotificationDeliveryResult
	workspaceID     uuid.UUID
	dateFrom        time.Time
	dateTo          time.Time
	overview        *domain.AdsOverview
	recommendations []domain.Recommendation
}

func (f *fakeReportNotifier) NotifyAgencyClientReport(_ context.Context, workspaceID uuid.UUID, _ time.Time, dateFrom, dateTo time.Time, overview *domain.AdsOverview, recommendations []domain.Recommendation) service.NotificationDeliveryResult {
	f.called = true
	f.workspaceID = workspaceID
	f.dateFrom = dateFrom
	f.dateTo = dateTo
	f.overview = overview
	f.recommendations = append([]domain.Recommendation(nil), recommendations...)
	return f.delivery
}

type fakeJobRunRow struct {
	scanFunc func(dest ...any) error
}

func (r *fakeJobRunRow) Scan(dest ...any) error { return r.scanFunc(dest...) }

type fakeJobRunRows struct{}

func (r *fakeJobRunRows) Close()                                       {}
func (r *fakeJobRunRows) Err() error                                   { return nil }
func (r *fakeJobRunRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeJobRunRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeJobRunRows) Next() bool                                   { return false }
func (r *fakeJobRunRows) Scan(_ ...any) error                          { return nil }
func (r *fakeJobRunRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeJobRunRows) RawValues() [][]byte                          { return nil }
func (r *fakeJobRunRows) Conn() *pgx.Conn                              { return nil }

type fakeJobRunDB struct {
	jobRuns map[uuid.UUID]sqlcgen.JobRun
}

func newFakeJobRunDB() *fakeJobRunDB {
	return &fakeJobRunDB{jobRuns: make(map[uuid.UUID]sqlcgen.JobRun)}
}

func (db *fakeJobRunDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (db *fakeJobRunDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *fakeJobRunDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return &fakeJobRunRows{}, nil
}

func (db *fakeJobRunDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case containsSQL(sql, "INSERT INTO job_runs"):
		now := time.Now().UTC()
		jobRun := sqlcgen.JobRun{
			ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
			WorkspaceID: args[0].(pgtype.UUID),
			TaskType:    args[1].(string),
			Status:      args[2].(string),
			StartedAt:   args[3].(pgtype.Timestamptz),
			Metadata:    args[4].([]byte),
			CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.jobRuns[uuid.UUID(jobRun.ID.Bytes)] = jobRun
		return jobRunToRow(jobRun)
	case containsSQL(sql, "UPDATE job_runs"):
		jobRunID := uuid.UUID(args[0].(pgtype.UUID).Bytes)
		jobRun, ok := db.jobRuns[jobRunID]
		if !ok {
			return &fakeJobRunRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		jobRun.Status = args[1].(string)
		jobRun.FinishedAt = args[2].(pgtype.Timestamptz)
		jobRun.ErrorMessage = args[3].(pgtype.Text)
		jobRun.Metadata = args[4].([]byte)
		db.jobRuns[jobRunID] = jobRun
		return jobRunToRow(jobRun)
	}

	return &fakeJobRunRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

func jobRunToRow(item sqlcgen.JobRun) pgx.Row {
	return &fakeJobRunRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = item.ID
		*dest[1].(*pgtype.UUID) = item.WorkspaceID
		*dest[2].(*string) = item.TaskType
		*dest[3].(*string) = item.Status
		*dest[4].(*pgtype.Timestamptz) = item.StartedAt
		*dest[5].(*pgtype.Timestamptz) = item.FinishedAt
		*dest[6].(*pgtype.Text) = item.ErrorMessage
		*dest[7].(*[]byte) = item.Metadata
		*dest[8].(*pgtype.Timestamptz) = item.CreatedAt
		return nil
	}}
}

func containsSQL(sql, substr string) bool {
	return len(sql) >= len(substr) && indexOf(sql, substr) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestHandleSyncProducts_EnqueuesRecommendationGeneration(t *testing.T) {
	workspaceID := uuid.New()
	db := newFakeJobRunDB()
	queries := sqlcgen.New(db)
	enqueuer := &fakeTaskEnqueuer{}
	processor := NewProcessor(&fakeSyncRunner{
		syncProductsFn: func(_ context.Context, actualWorkspaceID uuid.UUID) (service.SyncSummary, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return service.SyncSummary{Cabinets: 2, Products: 5}, nil
		},
	}, queries, nil, nil, enqueuer, zerolog.Nop())

	task, err := NewWorkspaceTask(TaskSyncProducts, workspaceID)
	require.NoError(t, err)

	err = processor.HandleSyncProducts(context.Background(), task)

	require.NoError(t, err)
	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, TaskGenerateRecommendations, enqueuer.tasks[0].taskType)

	var payload WorkspaceTaskPayload
	require.NoError(t, json.Unmarshal(enqueuer.tasks[0].payload, &payload))
	assert.Equal(t, workspaceID.String(), payload.WorkspaceID)

	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, TaskSyncProducts, jobRun.TaskType)
		assert.Equal(t, "completed", jobRun.Status)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(jobRun.Metadata, &metadata))
		payload, ok := metadata["payload"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, workspaceID.String(), payload["workspace_id"])
		assert.Equal(t, float64(5), metadata["products"])
		assert.Equal(t, "enqueued", metadata["recommendation_generation"])
	}
}

func TestHandleBidAutomationRecordsJobRun(t *testing.T) {
	workspaceID := uuid.New()
	db := newFakeJobRunDB()
	runner := &fakeBidAutomationRunner{changes: 2}
	processor := NewProcessor(nil, sqlcgen.New(db), nil, nil, nil, zerolog.Nop()).WithBidRunner(runner)
	task, err := NewWorkspaceTask(TaskBidAutomation, workspaceID)
	require.NoError(t, err)

	require.NoError(t, processor.HandleBidAutomation(context.Background(), task))
	require.True(t, runner.called)
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, TaskBidAutomation, jobRun.TaskType)
		assert.Equal(t, "completed", jobRun.Status)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(jobRun.Metadata, &metadata))
		assert.Equal(t, float64(2), metadata["changes_applied"])
	}
}

func TestHandleBidAutomationFailureMarksJobRun(t *testing.T) {
	workspaceID := uuid.New()
	db := newFakeJobRunDB()
	runner := &fakeBidAutomationRunner{err: errors.New("wb action failed")}
	processor := NewProcessor(nil, sqlcgen.New(db), nil, nil, nil, zerolog.Nop()).WithBidRunner(runner)
	task, err := NewWorkspaceTask(TaskBidAutomation, workspaceID)
	require.NoError(t, err)

	err = processor.HandleBidAutomation(context.Background(), task)
	require.ErrorContains(t, err, "wb action failed")
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, "failed", jobRun.Status)
		assert.Equal(t, "wb action failed", jobRun.ErrorMessage.String)
	}
}

func TestHandleReconcileBidActionsRunsEvidenceOnlyReconciler(t *testing.T) {
	runner := &fakeBidAutomationRunner{reconcileSummary: service.AutomationBidReconciliationSummary{
		Examined: 3, Applied: 1, NotApplied: 1, Deferred: 1,
	}}
	processor := NewProcessor(nil, nil, nil, nil, nil, zerolog.Nop()).WithBidRunner(runner)

	require.NoError(t, processor.HandleReconcileBidActions(context.Background(), NewSweepTask(TaskReconcileBidActions)))
	require.True(t, runner.reconcileCalled)
}

func TestHandleReconcileBidActionsReturnsRunnerError(t *testing.T) {
	runner := &fakeBidAutomationRunner{reconcileErr: errors.New("database unavailable")}
	processor := NewProcessor(nil, nil, nil, nil, nil, zerolog.Nop()).WithBidRunner(runner)

	err := processor.HandleReconcileBidActions(context.Background(), NewSweepTask(TaskReconcileBidActions))
	require.ErrorContains(t, err, "database unavailable")
}

func TestHandleSyncProducts_DuplicateRecommendationTaskDoesNotFail(t *testing.T) {
	workspaceID := uuid.New()
	db := newFakeJobRunDB()
	queries := sqlcgen.New(db)
	enqueuer := &fakeTaskEnqueuer{err: asynq.ErrDuplicateTask}
	processor := NewProcessor(&fakeSyncRunner{
		syncProductsFn: func(_ context.Context, actualWorkspaceID uuid.UUID) (service.SyncSummary, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return service.SyncSummary{Cabinets: 1, Products: 3}, nil
		},
	}, queries, nil, nil, enqueuer, zerolog.Nop())

	task, err := NewWorkspaceTask(TaskSyncProducts, workspaceID)
	require.NoError(t, err)

	err = processor.HandleSyncProducts(context.Background(), task)

	require.NoError(t, err)
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, "completed", jobRun.Status)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(jobRun.Metadata, &metadata))
		payload, ok := metadata["payload"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, workspaceID.String(), payload["workspace_id"])
		assert.Equal(t, "already_queued", metadata["recommendation_generation"])
	}
}

func TestHandleSyncProducts_FailedSyncMarksJobRunFailedWithoutEnqueue(t *testing.T) {
	workspaceID := uuid.New()
	db := newFakeJobRunDB()
	queries := sqlcgen.New(db)
	enqueuer := &fakeTaskEnqueuer{}
	processor := NewProcessor(&fakeSyncRunner{
		syncProductsFn: func(context.Context, uuid.UUID) (service.SyncSummary, error) {
			return service.SyncSummary{}, errors.New("wb api unavailable")
		},
	}, queries, nil, nil, enqueuer, zerolog.Nop())

	task, err := NewWorkspaceTask(TaskSyncProducts, workspaceID)
	require.NoError(t, err)

	err = processor.HandleSyncProducts(context.Background(), task)

	require.Error(t, err)
	assert.Empty(t, enqueuer.tasks)
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, "failed", jobRun.Status)
		assert.Equal(t, "wb api unavailable", jobRun.ErrorMessage.String)
	}
}

func TestHandleSyncWorkspace_FansOutPerCabinet(t *testing.T) {
	workspaceID := uuid.New()
	cabinetIDs := []uuid.UUID{uuid.New(), uuid.New()}
	db := newFakeJobRunDB()
	queries := sqlcgen.New(db)
	enqueuer := &fakeTaskEnqueuer{}
	processor := NewProcessor(&fakeSyncRunner{
		listWorkspaceCabinetIDsFn: func(_ context.Context, actualWorkspaceID uuid.UUID) ([]uuid.UUID, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return cabinetIDs, nil
		},
	}, queries, nil, nil, enqueuer, zerolog.Nop())

	task, err := NewWorkspaceSyncTask(workspaceID)
	require.NoError(t, err)

	err = processor.HandleSyncWorkspace(context.Background(), task)

	require.NoError(t, err)
	require.Len(t, enqueuer.tasks, 2)
	seen := map[string]bool{}
	for _, enqueued := range enqueuer.tasks {
		assert.Equal(t, TaskSyncWorkspace, enqueued.taskType)
		var child WorkspaceTaskPayload
		require.NoError(t, json.Unmarshal(enqueued.payload, &child))
		meta, ok := child.Metadata.(map[string]any)
		require.True(t, ok)
		seen[meta["seller_cabinet_id"].(string)] = true
		assert.Equal(t, "scheduled_workspace_fanout", meta["trigger"])
	}
	for _, cabinetID := range cabinetIDs {
		assert.True(t, seen[cabinetID.String()])
	}
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, TaskSyncWorkspace, jobRun.TaskType)
		assert.Equal(t, "completed", jobRun.Status)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(jobRun.Metadata, &metadata))
		payload, ok := metadata["payload"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, workspaceID.String(), payload["workspace_id"])
		assert.Equal(t, "cabinet_fanout", metadata["mode"])
		assert.Equal(t, float64(2), metadata["cabinets"])
		assert.Equal(t, float64(2), metadata["cabinet_tasks_enqueued"])
	}
}

func TestHandleSendClientAuditReportUsesRealOverviewAndFiltersCabinetRecommendations(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	otherCabinetID := uuid.New()
	db := newFakeJobRunDB()
	queries := sqlcgen.New(db)
	overview := &domain.AdsOverview{
		Attention: []domain.AttentionItem{{Title: "Бюджет скоро закончится"}},
		Totals: domain.AdsOverviewTotals{
			Campaigns: 3,
			Products:  8,
			Queries:   21,
		},
	}
	notifier := &fakeReportNotifier{delivery: service.NotificationDeliveryResult{EmailSent: true}}
	processor := NewProcessor(nil, queries, nil, nil, nil, zerolog.Nop()).
		WithReportDependencies(
			&fakeAdsReportReader{overviewFn: func(_ context.Context, actualWorkspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error) {
				assert.Equal(t, workspaceID, actualWorkspaceID)
				assert.Equal(t, "2026-04-29", dateFrom.Format("2006-01-02"))
				assert.Equal(t, "2026-05-28", dateTo.Format("2006-01-02"))
				require.Len(t, filter, 1)
				require.NotNil(t, filter[0].SellerCabinetID)
				assert.Equal(t, cabinetID, *filter[0].SellerCabinetID)
				return overview, nil
			}},
			&fakeReportRecommendationLister{listFn: func(_ context.Context, actualWorkspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
				assert.Equal(t, workspaceID, actualWorkspaceID)
				assert.Equal(t, domain.RecommendationStatusActive, filter.Status)
				assert.Equal(t, int32(1000), limit)
				assert.Equal(t, int32(0), offset)
				return []domain.Recommendation{
					{Title: "Client task", SellerCabinetID: &cabinetID},
					{Title: "Other client task", SellerCabinetID: &otherCabinetID},
					{Title: "Workspace task"},
				}, nil
			}},
			notifier,
		)

	task, err := NewWorkspaceTaskWithMetadata(TaskSendClientAuditReport, workspaceID, nil, map[string]any{
		"date_from":         "2026-04-29",
		"date_to":           "2026-05-28",
		"seller_cabinet_id": cabinetID.String(),
	})
	require.NoError(t, err)

	err = processor.HandleSendClientAuditReport(context.Background(), task)

	require.NoError(t, err)
	require.True(t, notifier.called)
	assert.Equal(t, workspaceID, notifier.workspaceID)
	assert.Equal(t, "2026-04-29", notifier.dateFrom.Format("2006-01-02"))
	assert.Equal(t, "2026-05-28", notifier.dateTo.Format("2006-01-02"))
	require.Len(t, notifier.recommendations, 1)
	assert.Equal(t, "Client task", notifier.recommendations[0].Title)
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, TaskSendClientAuditReport, jobRun.TaskType)
		assert.Equal(t, "completed", jobRun.Status)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(jobRun.Metadata, &metadata))
		assert.Equal(t, "2026-04-29", metadata["date_from"])
		assert.Equal(t, "2026-05-28", metadata["date_to"])
		assert.Equal(t, cabinetID.String(), metadata["seller_cabinet_id"])
		assert.Equal(t, float64(1), metadata["report_generated"])
		assert.Equal(t, false, metadata["telegram_sent"])
		assert.Equal(t, true, metadata["email_sent"])
		assert.Equal(t, float64(1), metadata["recommendations"])
		assert.Equal(t, float64(1), metadata["attention_items"])
	}
}

func TestClientAuditReportParamsDefaultToLast30Days(t *testing.T) {
	dateFrom, dateTo, cabinetID, err := clientAuditReportParams(nil, time.Date(2026, 5, 28, 17, 15, 0, 0, time.UTC))

	require.NoError(t, err)
	assert.Equal(t, "2026-04-29", dateFrom.Format("2006-01-02"))
	assert.Equal(t, "2026-05-28", dateTo.Format("2006-01-02"))
	assert.Nil(t, cabinetID)
}
