package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type jobRunInMemDB struct {
	jobRuns map[uuid.UUID]sqlcgen.JobRun
}

func newJobRunInMemDB() *jobRunInMemDB {
	return &jobRunInMemDB{jobRuns: make(map[uuid.UUID]sqlcgen.JobRun)}
}

func (db *jobRunInMemDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (db *jobRunInMemDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *jobRunInMemDB) Query(_ context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	switch {
	case containsSQL(sql, "FROM job_runs") && containsSQL(sql, "ORDER BY started_at DESC"):
		workspaceID := uuidFromPgtype(args[0].(pgtype.UUID))
		taskType := textValue(args[3].(pgtype.Text))
		status := textValue(args[4].(pgtype.Text))
		var items []sqlcgen.JobRun
		for _, item := range db.jobRuns {
			if !item.WorkspaceID.Valid || uuidFromPgtype(item.WorkspaceID) != workspaceID {
				continue
			}
			if taskType != "" && item.TaskType != taskType {
				continue
			}
			if status != "" && item.Status != status {
				continue
			}
			items = append(items, item)
		}
		return &jobRunRows{items: items}, nil
	}
	return &fakeRows{}, nil
}

func (db *jobRunInMemDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case containsSQL(sql, "FROM job_runs WHERE id"):
		jobRunID := uuidFromPgtype(args[0].(pgtype.UUID))
		jobRun, ok := db.jobRuns[jobRunID]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return &fakeRow{scanFunc: func(dest ...any) error {
			*dest[0].(*pgtype.UUID) = jobRun.ID
			*dest[1].(*pgtype.UUID) = jobRun.WorkspaceID
			*dest[2].(*string) = jobRun.TaskType
			*dest[3].(*string) = jobRun.Status
			*dest[4].(*pgtype.Timestamptz) = jobRun.StartedAt
			*dest[5].(*pgtype.Timestamptz) = jobRun.FinishedAt
			*dest[6].(*pgtype.Text) = jobRun.ErrorMessage
			*dest[7].(*[]byte) = jobRun.Metadata
			*dest[8].(*pgtype.Timestamptz) = jobRun.CreatedAt
			return nil
		}}
	}

	return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

type fakeJobRunRetryEnqueuer struct {
	workspaceFn func(taskType string, workspaceID uuid.UUID) (string, error)
	exportFn    func(workspaceID, exportID uuid.UUID) (string, error)
}

func (f fakeJobRunRetryEnqueuer) EnqueueWorkspaceTask(taskType string, workspaceID uuid.UUID) (string, error) {
	return f.workspaceFn(taskType, workspaceID)
}

func (f fakeJobRunRetryEnqueuer) EnqueueExportTask(workspaceID, exportID uuid.UUID) (string, error) {
	return f.exportFn(workspaceID, exportID)
}

type jobRunRows struct {
	items []sqlcgen.JobRun
	idx   int
}

func (r *jobRunRows) Close()                                       {}
func (r *jobRunRows) Err() error                                   { return nil }
func (r *jobRunRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *jobRunRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *jobRunRows) RawValues() [][]byte                          { return nil }
func (r *jobRunRows) Conn() *pgx.Conn                              { return nil }
func (r *jobRunRows) Values() ([]any, error)                       { return nil, nil }
func (r *jobRunRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}
func (r *jobRunRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
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
}

func TestJobRunServiceListFiltersAtQueryLevel(t *testing.T) {
	db := newJobRunInMemDB()
	queries := sqlcgen.New(db)
	workspaceID := uuid.New()
	otherWorkspaceID := uuid.New()
	now := time.Now().UTC()

	firstID := uuid.New()
	db.jobRuns[firstID] = sqlcgen.JobRun{
		ID:          uuidToPgtype(firstID),
		WorkspaceID: uuidToPgtype(workspaceID),
		TaskType:    "wb:sync_products",
		Status:      "completed",
		StartedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	secondID := uuid.New()
	db.jobRuns[secondID] = sqlcgen.JobRun{
		ID:          uuidToPgtype(secondID),
		WorkspaceID: uuidToPgtype(workspaceID),
		TaskType:    "wb:sync_campaigns",
		Status:      "failed",
		StartedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	thirdID := uuid.New()
	db.jobRuns[thirdID] = sqlcgen.JobRun{
		ID:          uuidToPgtype(thirdID),
		WorkspaceID: uuidToPgtype(otherWorkspaceID),
		TaskType:    "wb:sync_products",
		Status:      "completed",
		StartedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	svc := NewJobRunService(queries, nil)

	result, err := svc.List(context.Background(), workspaceID, JobRunListFilter{
		TaskType: "wb:sync_products",
		Status:   "completed",
	}, 20, 0)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, firstID, result[0].ID)
}

func TestJobRunServiceRetryWorkspaceTask(t *testing.T) {
	db := newJobRunInMemDB()
	queries := sqlcgen.New(db)
	workspaceID := uuid.New()
	jobRunID := uuid.New()
	now := time.Now().UTC()
	db.jobRuns[jobRunID] = sqlcgen.JobRun{
		ID:          uuidToPgtype(jobRunID),
		WorkspaceID: uuidToPgtype(workspaceID),
		TaskType:    "wb:sync_products",
		Status:      "failed",
		StartedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		Metadata:    []byte(`{"payload":{"workspace_id":"` + workspaceID.String() + `"}}`),
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	svc := NewJobRunService(queries, fakeJobRunRetryEnqueuer{
		workspaceFn: func(taskType string, actualWorkspaceID uuid.UUID) (string, error) {
			assert.Equal(t, "wb:sync_products", taskType)
			assert.Equal(t, workspaceID, actualWorkspaceID)
			return "enqueued", nil
		},
		exportFn: func(uuid.UUID, uuid.UUID) (string, error) {
			t.Fatal("export retry should not be called")
			return "", nil
		},
	})

	result, err := svc.Retry(context.Background(), workspaceID, jobRunID)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "enqueued", result.Status)
	assert.Equal(t, "wb:sync_products", result.TaskType)
	assert.Equal(t, workspaceID, result.WorkspaceID)
}

func TestJobRunServiceRetryExportTask(t *testing.T) {
	db := newJobRunInMemDB()
	queries := sqlcgen.New(db)
	workspaceID := uuid.New()
	jobRunID := uuid.New()
	exportID := uuid.New()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]string{"export_id": exportID.String()})
	require.NoError(t, err)
	db.jobRuns[jobRunID] = sqlcgen.JobRun{
		ID:          uuidToPgtype(jobRunID),
		WorkspaceID: uuidToPgtype(workspaceID),
		TaskType:    "export:generate",
		Status:      "failed",
		StartedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		Metadata:    metadata,
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	svc := NewJobRunService(queries, fakeJobRunRetryEnqueuer{
		workspaceFn: func(string, uuid.UUID) (string, error) {
			t.Fatal("workspace retry should not be called")
			return "", nil
		},
		exportFn: func(actualWorkspaceID, actualExportID uuid.UUID) (string, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			assert.Equal(t, exportID, actualExportID)
			return "enqueued", nil
		},
	})

	result, err := svc.Retry(context.Background(), workspaceID, jobRunID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ExportID)
	assert.Equal(t, exportID, *result.ExportID)
	assert.Equal(t, "export:generate", result.TaskType)
}
