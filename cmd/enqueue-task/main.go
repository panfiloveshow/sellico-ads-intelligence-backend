package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: enqueue-task refresh-integrations|sync-sweep|sync-workspace <workspace-id>|client-audit-report <workspace-id> [date_from=YYYY-MM-DD] [date_to=YYYY-MM-DD] [seller_cabinet_id=uuid]")
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
	case "client-audit-report":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "client-audit-report requires workspace id")
			os.Exit(2)
		}
		workspaceID, parseErr := uuid.Parse(os.Args[2])
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "parse workspace id: %v\n", parseErr)
			os.Exit(2)
		}
		metadata := map[string]any{}
		for _, arg := range os.Args[3:] {
			key, value, ok := strings.Cut(arg, "=")
			if !ok || value == "" {
				fmt.Fprintf(os.Stderr, "invalid metadata argument %q\n", arg)
				os.Exit(2)
			}
			switch key {
			case "date_from", "date_to", "seller_cabinet_id":
				metadata[key] = value
			default:
				fmt.Fprintf(os.Stderr, "unsupported metadata key %q\n", key)
				os.Exit(2)
			}
		}
		task, err = worker.NewWorkspaceTaskWithMetadata(worker.TaskSendClientAuditReport, workspaceID, nil, metadata)
		opts = []asynq.Option{asynq.Queue(worker.QueueRecommendations), asynq.MaxRetry(3)}
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
