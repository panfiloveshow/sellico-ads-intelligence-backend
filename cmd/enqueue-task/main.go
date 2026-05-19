package main

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: enqueue-task refresh-integrations|sync-sweep|sync-workspace <workspace-id>")
		os.Exit(2)
	}

	cfg := config.Load()
	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse redis url: %v\n", err)
		os.Exit(1)
	}
	client := asynq.NewClient(redisOpt)
	defer client.Close()

	var task *asynq.Task
	timeout := 30 * time.Minute
	opts := []asynq.Option{asynq.Queue(worker.QueueWBSync), asynq.MaxRetry(10)}

	switch os.Args[1] {
	case "refresh-integrations":
		task = worker.NewSweepTask(worker.TaskSweepRefreshIntegrations)
	case "sync-sweep":
		task = worker.NewSweepTask(worker.TaskSweepSyncWorkspace)
	case "sync-workspace":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "sync-workspace requires workspace id")
			os.Exit(2)
		}
		workspaceID, parseErr := uuid.Parse(os.Args[2])
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "parse workspace id: %v\n", parseErr)
			os.Exit(2)
		}
		task, err = worker.NewWorkspaceTask(worker.TaskSyncWorkspace, workspaceID)
		timeout = 60 * time.Minute
		opts = append(opts, asynq.Unique(55*time.Minute))
	default:
		fmt.Fprintf(os.Stderr, "unknown task %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "create task: %v\n", err)
		os.Exit(1)
	}
	opts = append(opts, asynq.Timeout(timeout))

	info, err := client.Enqueue(task, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enqueue task: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("enqueued %s type=%s queue=%s\n", info.ID, task.Type(), info.Queue)
}
