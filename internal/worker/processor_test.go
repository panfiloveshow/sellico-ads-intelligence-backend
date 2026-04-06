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

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

type fakeSyncRunner struct {
	syncCampaignsFn      func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncCampaignStatsFn  func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncPhrasesFn        func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncProductsFn       func(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	syncSingleCabinetFn  func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (service.SyncSummary, error)
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

func TestHandleSyncWorkspace_EnqueuesRecommendationAfterFullRun(t *testing.T) {
	workspaceID := uuid.New()
	db := newFakeJobRunDB()
	queries := sqlcgen.New(db)
	enqueuer := &fakeTaskEnqueuer{}
	processor := NewProcessor(&fakeSyncRunner{
		syncCampaignsFn: func(_ context.Context, actualWorkspaceID uuid.UUID) (service.SyncSummary, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return service.SyncSummary{Cabinets: 2, Campaigns: 7}, nil
		},
		syncCampaignStatsFn: func(_ context.Context, actualWorkspaceID uuid.UUID) (service.SyncSummary, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return service.SyncSummary{Cabinets: 2, CampaignStats: 9, SkippedCampaign: 1}, nil
		},
		syncPhrasesFn: func(_ context.Context, actualWorkspaceID uuid.UUID) (service.SyncSummary, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return service.SyncSummary{Cabinets: 2, Phrases: 11, PhraseStats: 12, SkippedCampaign: 2}, nil
		},
		syncProductsFn: func(_ context.Context, actualWorkspaceID uuid.UUID) (service.SyncSummary, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return service.SyncSummary{Cabinets: 2, Products: 5}, nil
		},
	}, queries, nil, nil, enqueuer, zerolog.Nop())

	task, err := NewWorkspaceSyncTask(workspaceID)
	require.NoError(t, err)

	err = processor.HandleSyncWorkspace(context.Background(), task)

	require.NoError(t, err)
	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, TaskGenerateRecommendations, enqueuer.tasks[0].taskType)
	require.Len(t, db.jobRuns, 1)
	for _, jobRun := range db.jobRuns {
		assert.Equal(t, TaskSyncWorkspace, jobRun.TaskType)
		assert.Equal(t, "completed", jobRun.Status)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(jobRun.Metadata, &metadata))
		payload, ok := metadata["payload"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, workspaceID.String(), payload["workspace_id"])
		assert.Equal(t, float64(7), metadata["campaigns"])
		assert.Equal(t, float64(9), metadata["campaign_stats"])
		assert.Equal(t, float64(11), metadata["phrases"])
		assert.Equal(t, float64(12), metadata["phrase_stats"])
		assert.Equal(t, float64(5), metadata["products"])
		assert.Equal(t, float64(3), metadata["skipped"])
		assert.Equal(t, "enqueued", metadata["recommendation_generation"])
	}
}
