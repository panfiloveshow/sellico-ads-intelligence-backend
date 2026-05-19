package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: enqueue-cabinet-sync <seller-cabinet-id>")
		os.Exit(2)
	}

	cabinetID, err := uuid.Parse(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse seller cabinet id: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	databaseURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL and REDIS_URL are required")
		os.Exit(2)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect postgres: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	var workspaceID uuid.UUID
	var cabinetName string
	if err := pool.QueryRow(ctx, `
		SELECT workspace_id, name
		FROM seller_cabinets
		WHERE id = $1 AND deleted_at IS NULL
	`, cabinetID).Scan(&workspaceID, &cabinetName); err != nil {
		fmt.Fprintf(os.Stderr, "load seller cabinet: %v\n", err)
		os.Exit(1)
	}

	metadata := map[string]any{
		"seller_cabinet_id":   cabinetID.String(),
		"seller_cabinet_name": cabinetName,
		"task_type":           worker.TaskSyncWorkspace,
		"trigger":             "operator_control_sync",
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal metadata: %v\n", err)
		os.Exit(1)
	}

	var jobRunID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_runs (workspace_id, task_type, status, started_at, metadata)
		VALUES ($1, $2, 'pending', now(), $3::jsonb)
		RETURNING id
	`, workspaceID, worker.TaskSyncWorkspace, metadataJSON).Scan(&jobRunID); err != nil {
		fmt.Fprintf(os.Stderr, "create job run: %v\n", err)
		os.Exit(1)
	}

	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse redis url: %v\n", err)
		os.Exit(1)
	}
	client := asynq.NewClient(redisOpt)
	defer client.Close()

	task, err := worker.NewWorkspaceTaskWithMetadata(worker.TaskSyncWorkspace, workspaceID, &jobRunID, metadata)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create task: %v\n", err)
		os.Exit(1)
	}
	info, err := client.Enqueue(task, asynq.Queue(worker.QueueWBSync), asynq.MaxRetry(10), asynq.Timeout(60*time.Minute))
	if err != nil {
		_, _ = pool.Exec(ctx, `
			UPDATE job_runs
			SET status = 'failed', finished_at = now(), error_message = $2
			WHERE id = $1
		`, jobRunID, err.Error())
		fmt.Fprintf(os.Stderr, "enqueue task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("enqueued job_run_id=%s task_id=%s workspace_id=%s cabinet_id=%s cabinet_name=%q queue=%s\n",
		jobRunID, info.ID, workspaceID, cabinetID, cabinetName, info.Queue)
}
