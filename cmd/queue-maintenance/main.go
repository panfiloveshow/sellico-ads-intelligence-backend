// Command queue-maintenance removes stale recurring scheduler triggers after a
// retry storm. It deliberately ignores tenant-scoped tasks and archived
// history. The command is dry-run by default; pass --apply to delete matches.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

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

type taskLister func(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)

func main() {
	apply := flag.Bool("apply", false, "delete matching tasks; without this flag the command is read-only")
	flag.Parse()

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
	states := []struct {
		name string
		list taskLister
	}{
		{name: "pending", list: inspector.ListPendingTasks},
		{name: "retry", list: inspector.ListRetryTasks},
		{name: "scheduled", list: inspector.ListScheduledTasks},
	}

	for _, queue := range queues {
		for _, state := range states {
			tasks, listErr := state.list(queue, asynq.PageSize(10000))
			if listErr != nil {
				fatalf("list %s tasks in %s: %v", state.name, queue, listErr)
			}
			byType := make(map[string]int)
			for _, task := range tasks {
				if !isStaleRecurringSweep(task) {
					continue
				}
				totalMatched++
				byType[task.Type]++
				if *apply {
					if deleteErr := inspector.DeleteTask(queue, task.ID); deleteErr != nil {
						fatalf("delete %s task %s from %s: %v", state.name, task.ID, queue, deleteErr)
					}
					totalDeleted++
				}
			}
			printCounts(queue, state.name, byType)
		}
	}

	mode := "dry-run"
	if *apply {
		mode = "apply"
	}
	fmt.Printf("mode=%s matched=%d deleted=%d\n", mode, totalMatched, totalDeleted)
}

func isStaleRecurringSweep(task *asynq.TaskInfo) bool {
	if task == nil || len(task.Payload) != 0 {
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
