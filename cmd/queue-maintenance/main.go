// Command queue-maintenance removes legacy recurring scheduler retries after a
// retry storm. It deliberately ignores pending, scheduled, active,
// tenant-scoped, and archived tasks. The command is dry-run by default; pass
// --apply to delete matches.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/hibiken/asynq"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

var recurringTaskTypes = map[string]struct{}{
	worker.TaskSweepRefreshIntegrations:     {},
	worker.TaskSweepSyncWorkspace:           {},
	worker.TaskSweepRecommendations:         {},
	worker.TaskSweepBidAutomation:           {},
	worker.TaskReconcileBidActions:          {},
	worker.TaskSweepSyncPrices:              {},
	worker.TaskSweepRepricer:                {},
	worker.TaskSweepRepricerDigest:          {},
	worker.TaskSweepSellicoEconomics:        {},
	worker.TaskSweepPollPriceTasks:          {},
	worker.TaskExecutePriceSchedule:         {},
	worker.TaskSweepCollectKeywords:         {},
	worker.TaskSweepExtractCompetitors:      {},
	worker.TaskSweepCollectDelivery:         {},
	worker.TaskSweepSEOAnalysis:             {},
	worker.TaskSweepExtendedRecommendations: {},
	worker.TaskSweepClientAuditReports:      {},
	worker.TaskOzonSweepSync:                {},
	worker.TaskOzonAnalyticsSync:            {},
	worker.TaskOzonPostingsSync:             {},
	worker.TaskOzonPhrasesSync:              {},
	worker.TaskOzonCPOOrdersSync:            {},
	worker.TaskOzonCampaignSkuSync:          {},
	worker.TaskOzonStrategySweep:            {},
	worker.TaskOzonAISweep:                  {},
	worker.TaskOzonAIImpactSweep:            {},
	worker.TaskOzonAIWeeklyReport:           {},
	worker.TaskOzonRepricerSweep:            {},
	worker.TaskOzonExecuteSchedules:         {},
}

func main() {
	apply := flag.Bool("apply", false, "delete matching tasks; without this flag the command is read-only")
	olderThan := flag.Duration("older-than", time.Hour, "only match retries whose last failure is older than this duration")
	flag.Parse()
	if *olderThan <= 0 {
		fatalf("--older-than must be positive")
	}
	failedBefore := time.Now().Add(-*olderThan)

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		fatalf("REDIS_URL is required")
	}
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		fatalf("parse REDIS_URL: %v", err)
	}
	inspector := asynq.NewInspector(redisOpt)
	defer inspector.Close()

	queues, err := inspector.Queues()
	if err != nil {
		fatalf("list queues: %v", err)
	}
	sort.Strings(queues)

	totalMatched := 0
	totalDeleted := 0
	for _, queue := range queues {
		tasks, listErr := inspector.ListRetryTasks(queue, asynq.PageSize(10000))
		if listErr != nil {
			fatalf("list retry tasks in %s: %v", queue, listErr)
		}
		byType := make(map[string]int)
		for _, task := range tasks {
			if !isLegacyRecurringRetry(task, failedBefore) {
				continue
			}
			totalMatched++
			byType[task.Type]++
			if *apply {
				if deleteErr := inspector.DeleteTask(queue, task.ID); deleteErr != nil {
					fatalf("delete retry task %s from %s: %v", task.ID, queue, deleteErr)
				}
				totalDeleted++
			}
		}
		printCounts(queue, "retry", byType)
	}

	mode := "dry-run"
	if *apply {
		mode = "apply"
	}
	fmt.Printf("mode=%s older_than=%s matched=%d deleted=%d\n", mode, olderThan.String(), totalMatched, totalDeleted)
}

func isLegacyRecurringRetry(task *asynq.TaskInfo, failedBefore time.Time) bool {
	if task == nil || len(task.Payload) != 0 || task.MaxRetry <= 0 || task.Retried <= 0 || task.LastFailedAt.IsZero() {
		return false
	}
	if !task.LastFailedAt.Before(failedBefore) {
		return false
	}
	_, ok := recurringTaskTypes[task.Type]
	return ok
}

func printCounts(queue, state string, counts map[string]int) {
	types := make([]string, 0, len(counts))
	for taskType := range counts {
		types = append(types, taskType)
	}
	sort.Strings(types)
	for _, taskType := range types {
		fmt.Printf("queue=%s state=%s type=%s count=%d\n", queue, state, taskType, counts[taskType])
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
