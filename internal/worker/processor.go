package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

const (
	jobResultStateOK      = "ok"
	jobResultStateEmpty   = "empty"
	jobResultStatePartial = "partial"
	jobResultStateFailed  = "failed"

	defaultTaskTimeout       = 15 * time.Minute
	workspaceSyncTaskTimeout = 60 * time.Minute
	jobRunFinalUpdateTimeout = 10 * time.Second
	phaseRetryDelayFloor     = 30 * time.Minute
	maxPhaseRetryCount       = 3
)

var workspaceSyncLocks sync.Map

type syncRunner interface {
	ListWorkspaceCabinetIDs(ctx context.Context, workspaceID uuid.UUID) ([]uuid.UUID, error)
	SyncWorkspace(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncCampaigns(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncCampaignStats(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncPhrases(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncProducts(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncSingleCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (service.SyncSummary, error)
	SyncSingleCabinetPhase(ctx context.Context, workspaceID, cabinetID uuid.UUID, phase string) (service.SyncSummary, error)
}

type recommendationGenerator interface {
	GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error)
}

type exportGenerator interface {
	Generate(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error)
}

type taskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// syncNotifier sends notifications after sync and recommendation events.
type syncNotifier interface {
	NotifySyncComplete(ctx context.Context, workspaceID uuid.UUID, status string, issues []string)
}

type reportNotifier interface {
	NotifyAgencyClientReport(ctx context.Context, workspaceID uuid.UUID, reportDate, dateFrom, dateTo time.Time, overview *domain.AdsOverview, recommendations []domain.Recommendation) service.NotificationDeliveryResult
}

type adsReportReader interface {
	Overview(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error)
}

type reportRecommendationLister interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

// integrationRefresher auto-discovers new Sellico integrations. Refresh
// dispatches to whichever discovery paths the service has been configured
// for (legacy per-user, service-account, or both).
type integrationRefresher interface {
	Refresh(ctx context.Context) error
}

// bidAutomationRunner executes bid strategies for a workspace.
type bidAutomationRunner interface {
	RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
	ReconcileStaleBidActions(ctx context.Context, now time.Time, limit int32) (service.AutomationBidReconciliationSummary, error)
}

type repricerRunner interface {
	RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
	PollUploadTasks(ctx context.Context, workspaceID uuid.UUID) (int, error)
	ExecuteDueSchedules(ctx context.Context, now time.Time) (int, error)
	SyncQuarantine(ctx context.Context, workspaceID uuid.UUID) (int, error)
	SyncPrices(ctx context.Context, workspaceID uuid.UUID) (int, error)
	SendDailyDigest(ctx context.Context, workspaceID uuid.UUID) error
}

type economicsSyncRunner interface {
	SyncWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

type semanticsCollector interface {
	CollectFromPhrases(ctx context.Context, workspaceID, sellerCabinetID uuid.UUID) (int, error)
	CollectFromSERP(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

type competitorExtractor interface {
	ExtractFromSERP(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

type deliveryCollector interface {
	CollectForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

type seoAnalyzer interface {
	AnalyzeWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

// extendedRecommendationGenerator generates extended recs (SEO, price, content).
type extendedRecommendationGenerator interface {
	GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error)
}

type Processor struct {
	syncService          syncRunner
	queries              *sqlcgen.Queries
	engine               recommendationGenerator
	extendedEngine       extendedRecommendationGenerator
	exportGenerator      exportGenerator
	client               taskEnqueuer
	notifier             syncNotifier
	reportNotifier       reportNotifier
	adsRead              adsReportReader
	recommendations      reportRecommendationLister
	integrationRefresher integrationRefresher
	bidRunner            bidAutomationRunner
	repricer             repricerRunner
	economicsSync        economicsSyncRunner
	semantics            semanticsCollector
	competitors          competitorExtractor
	delivery             deliveryCollector
	seo                  seoAnalyzer
	logger               zerolog.Logger
}

func NewProcessor(syncService syncRunner, queries *sqlcgen.Queries, engine recommendationGenerator, exportGenerator exportGenerator, client taskEnqueuer, logger zerolog.Logger) *Processor {
	return &Processor{
		syncService:     syncService,
		queries:         queries,
		engine:          engine,
		exportGenerator: exportGenerator,
		client:          client,
		logger:          logger,
	}
}

// WithNotifier sets the notification service for sync-complete alerts.
func (p *Processor) WithNotifier(n syncNotifier) *Processor {
	p.notifier = n
	if reporter, ok := n.(reportNotifier); ok {
		p.reportNotifier = reporter
	}
	return p
}

func (p *Processor) WithReportDependencies(adsRead adsReportReader, recommendations reportRecommendationLister, notifier reportNotifier) *Processor {
	p.adsRead = adsRead
	p.recommendations = recommendations
	p.reportNotifier = notifier
	return p
}

// WithIntegrationRefresher sets the integration refresher for auto-discovery.
func (p *Processor) WithIntegrationRefresher(r integrationRefresher) *Processor {
	p.integrationRefresher = r
	return p
}

// WithBidRunner sets the bid automation runner.
func (p *Processor) WithBidRunner(r bidAutomationRunner) *Processor {
	p.bidRunner = r
	return p
}

func (p *Processor) WithRepricer(r repricerRunner) *Processor {
	p.repricer = r
	return p
}

// WithEconomicsSync sets the Sellico→product_economics cost bridge runner.
func (p *Processor) WithEconomicsSync(r economicsSyncRunner) *Processor {
	p.economicsSync = r
	return p
}

func (p *Processor) WithSemanticsCollector(s semanticsCollector) *Processor {
	p.semantics = s
	return p
}

func (p *Processor) WithCompetitorExtractor(c competitorExtractor) *Processor {
	p.competitors = c
	return p
}

func (p *Processor) WithDeliveryCollector(d deliveryCollector) *Processor {
	p.delivery = d
	return p
}

func (p *Processor) WithSEOAnalyzer(s seoAnalyzer) *Processor {
	p.seo = s
	return p
}

// WithExtendedEngine sets the extended recommendation engine (SEO, price, content).
func (p *Processor) WithExtendedEngine(e extendedRecommendationGenerator) *Processor {
	p.extendedEngine = e
	return p
}

// HandleBidAutomation runs the bid engine for a single workspace.
func (p *Processor) HandleBidAutomation(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}

	if p.bidRunner == nil {
		p.logger.Debug().Msg("bid runner not configured, skipping")
		return nil
	}

	return p.runWithJobRun(ctx, TaskBidAutomation, &workspaceID, payload, func() (map[string]any, error) {
		changesApplied, runErr := p.bidRunner.RunForWorkspace(ctx, workspaceID)
		if runErr != nil {
			p.logger.Error().Err(runErr).Str("workspace_id", workspaceID.String()).Msg("bid automation failed")
			return map[string]any{"changes_applied": changesApplied}, runErr
		}

		p.logger.Info().
			Str("workspace_id", workspaceID.String()).
			Int("changes_applied", changesApplied).
			Msg("bid automation completed")
		return map[string]any{"changes_applied": changesApplied}, nil
	})
}

// HandleReconcileBidActions resolves pending/unknown WB writes from fresh
// synced bids. The runner never repeats an external bid mutation.
func (p *Processor) HandleReconcileBidActions(ctx context.Context, _ *asynq.Task) error {
	if p.bidRunner == nil {
		p.logger.Debug().Msg("bid runner not configured, skipping reconciliation")
		return nil
	}
	summary, err := p.bidRunner.ReconcileStaleBidActions(ctx, time.Now().UTC(), 500)
	if err != nil {
		p.logger.Error().Err(err).Int("examined", summary.Examined).Msg("bid action reconciliation completed with errors")
		return err
	}
	p.logger.Info().
		Int("examined", summary.Examined).
		Int("applied", summary.Applied).
		Int("not_applied", summary.NotApplied).
		Int("superseded", summary.Superseded).
		Int("deferred", summary.Deferred).
		Msg("bid action reconciliation completed")
	return nil
}

// HandleCollectDelivery collects delivery data for a workspace.
func (p *Processor) HandleCollectDelivery(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.delivery == nil {
		return fmt.Errorf("delivery collector not configured")
	}
	return p.runWithJobRun(ctx, TaskCollectDelivery, &workspaceID, payload, func() (map[string]any, error) {
		collected, runErr := p.delivery.CollectForWorkspace(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"products_collected": collected}, nil
	})
}

func (p *Processor) HandleSweepCollectDelivery(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepCollectDelivery, TaskCollectDelivery, QueueDelivery)
}

func (p *Processor) HandleSEOAnalysis(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.seo == nil {
		return fmt.Errorf("seo analyzer not configured")
	}
	return p.runWithJobRun(ctx, TaskSEOAnalysis, &workspaceID, payload, func() (map[string]any, error) {
		analyzed, runErr := p.seo.AnalyzeWorkspace(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"products_analyzed": analyzed}, nil
	})
}

func (p *Processor) HandleSweepSEOAnalysis(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSEOAnalysis, TaskSEOAnalysis, QueueSEO)
}

func (p *Processor) HandleExtendedRecommendations(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}

	if p.extendedEngine == nil {
		p.logger.Debug().Msg("extended recommendation engine not configured, skipping")
		return nil
	}

	return p.runWithJobRun(ctx, TaskExtendedRecommendations, &workspaceID, payload, func() (map[string]any, error) {
		recs, runErr := p.extendedEngine.GenerateForWorkspace(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"generated": len(recs)}, nil
	})
}

func (p *Processor) HandleSweepExtendedRecommendations(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepExtendedRecommendations, TaskExtendedRecommendations, QueueRecommendations)
}

func (p *Processor) HandleSendClientAuditReport(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.adsRead == nil || p.recommendations == nil || p.reportNotifier == nil {
		return fmt.Errorf("client audit report dependencies are not configured")
	}

	dateFrom, dateTo, sellerCabinetID, err := clientAuditReportParams(payload.Metadata, time.Now().UTC())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSendClientAuditReport, &workspaceID, payload, func() (map[string]any, error) {
		overview, runErr := p.adsRead.Overview(ctx, workspaceID, dateFrom, dateTo, service.OverviewFilter{SellerCabinetID: sellerCabinetID})
		if runErr != nil {
			return nil, runErr
		}
		recommendations, runErr := p.recommendations.List(ctx, workspaceID, service.RecommendationListFilter{Status: domain.RecommendationStatusActive}, 1000, 0)
		if runErr != nil {
			return nil, runErr
		}
		recommendations = filterReportRecommendationsBySellerCabinet(recommendations, sellerCabinetID)
		delivery := p.reportNotifier.NotifyAgencyClientReport(ctx, workspaceID, time.Now().UTC(), dateFrom, dateTo, overview, recommendations)
		result := map[string]any{
			"report_generated": 1,
			"telegram_sent":    delivery.TelegramSent,
			"email_sent":       delivery.EmailSent,
			"date_from":        dateFrom.Format("2006-01-02"),
			"date_to":          dateTo.Format("2006-01-02"),
			"recommendations":  len(recommendations),
			"attention_items":  len(overview.Attention),
			"campaigns":        overview.Totals.Campaigns,
			"products":         overview.Totals.Products,
			"queries":          overview.Totals.Queries,
		}
		if sellerCabinetID != nil {
			result["seller_cabinet_id"] = sellerCabinetID.String()
		}
		return result, nil
	})
}

func (p *Processor) HandleSweepClientAuditReports(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepClientAuditReports, TaskSendClientAuditReport, QueueRecommendations)
}

// HandleCollectKeywords collects keywords from phrases/SERP for a workspace.
func (p *Processor) HandleCollectKeywords(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.semantics == nil {
		return fmt.Errorf("semantics collector not configured")
	}
	return p.runWithJobRun(ctx, TaskCollectKeywords, &workspaceID, payload, func() (map[string]any, error) {
		// Keywords are per-store: collect each active cabinet separately so a
		// workspace running multiple stores never blends their keyword pools.
		cabinets, err := p.queries.ListActiveSellerCabinetsByWorkspace(ctx, pgtype.UUID{Bytes: workspaceID, Valid: true})
		if err != nil {
			return nil, err
		}

		fromPhrases := 0
		for _, cabinet := range cabinets {
			cabinetID := uuid.UUID(cabinet.ID.Bytes)
			count, runErr := p.semantics.CollectFromPhrases(ctx, workspaceID, cabinetID)
			if runErr != nil {
				return nil, runErr
			}
			fromPhrases += count
		}

		fromSERP, runErr := p.semantics.CollectFromSERP(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{
			"cabinets":     len(cabinets),
			"from_phrases": fromPhrases,
			"from_serp":    fromSERP,
			"imported":     fromPhrases + fromSERP,
		}, nil
	})
}

// HandleSweepCollectKeywords distributes keyword collection across workspaces.
func (p *Processor) HandleSweepCollectKeywords(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepCollectKeywords, TaskCollectKeywords, QueueSemantics)
}

// HandleExtractCompetitors extracts competitors from SERP for a workspace.
func (p *Processor) HandleExtractCompetitors(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.competitors == nil {
		return fmt.Errorf("competitor extractor not configured")
	}
	return p.runWithJobRun(ctx, TaskExtractCompetitors, &workspaceID, payload, func() (map[string]any, error) {
		extracted, runErr := p.competitors.ExtractFromSERP(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"competitors_found": extracted}, nil
	})
}

// HandleSweepExtractCompetitors distributes competitor extraction across workspaces.
func (p *Processor) HandleSweepExtractCompetitors(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepExtractCompetitors, TaskExtractCompetitors, QueueCompetitors)
}

// HandleSweepBidAutomation runs bid automation for all workspaces with active strategies.
func (p *Processor) HandleSweepBidAutomation(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepBidAutomation, TaskBidAutomation, QueueBidAutomation)
}

func (p *Processor) HandleSweepRepricer(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepRepricer, TaskRepricer, QueueRepricer)
}

func (p *Processor) HandleRepricer(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.repricer == nil {
		p.logger.Debug().Msg("repricer not configured, skipping")
		return nil
	}
	// Refresh price quarantine before deciding (also runs when no strategies exist).
	if _, qErr := p.repricer.SyncQuarantine(ctx, workspaceID); qErr != nil {
		p.logger.Warn().Err(qErr).Str("workspace_id", workspaceID.String()).Msg("quarantine sync failed")
	}
	changes, err := p.repricer.RunForWorkspace(ctx, workspaceID)
	if err != nil {
		p.logger.Error().Err(err).Str("workspace_id", workspaceID.String()).Msg("repricer run failed")
		return err
	}
	p.logger.Info().Str("workspace_id", workspaceID.String()).Int("changes", changes).Msg("repricer run completed")
	return nil
}

func (p *Processor) HandleSweepPollPriceTasks(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepPollPriceTasks, TaskPollPriceTasks, QueueRepricerPoll)
}

// HandleSweepSyncPrices refreshes the WB catalog (names/images) and prices for
// all workspaces, so the repricer view stays populated without a manual click.
func (p *Processor) HandleSweepSyncPrices(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncPrices, TaskSyncPrices, QueueRepricer)
}

// HandleSweepRepricerDigest sends each workspace's daily repricer summary.
func (p *Processor) HandleSweepRepricerDigest(ctx context.Context, _ *asynq.Task) error {
	if p.repricer == nil {
		return nil
	}
	workspaces, err := p.queries.ListWorkspaces(ctx, sqlcgen.ListWorkspacesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		if err := p.repricer.SendDailyDigest(ctx, uuid.UUID(w.ID.Bytes)); err != nil {
			p.logger.Warn().Err(err).Msg("repricer digest failed")
		}
	}
	return nil
}

// HandleSweepSellicoEconomics mirrors Sellico cost data into product_economics
// for every workspace so the margin-floor strategy has real numbers.
func (p *Processor) HandleSweepSellicoEconomics(ctx context.Context, _ *asynq.Task) error {
	if p.economicsSync == nil {
		return nil
	}
	workspaces, err := p.queries.ListWorkspaces(ctx, sqlcgen.ListWorkspacesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		if n, err := p.economicsSync.SyncWorkspace(ctx, uuid.UUID(w.ID.Bytes)); err != nil {
			p.logger.Warn().Err(err).Msg("sellico economics sync failed")
		} else if n > 0 {
			p.logger.Info().Int("imported", n).Str("workspace_id", uuid.UUID(w.ID.Bytes).String()).Msg("sellico economics synced")
		}
	}
	return nil
}

// HandleSyncPrices refreshes WB prices for a workspace (async, user-triggered).
func (p *Processor) HandleSyncPrices(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.repricer == nil {
		return nil
	}
	synced, err := p.repricer.SyncPrices(ctx, workspaceID)
	if err != nil {
		p.logger.Error().Err(err).Str("workspace_id", workspaceID.String()).Msg("price sync failed")
		return err
	}
	p.logger.Info().Str("workspace_id", workspaceID.String()).Int("synced", synced).Msg("price sync completed")
	return nil
}

// HandleExecutePriceSchedules runs all due schedule entries (global, not per-workspace).
func (p *Processor) HandleExecutePriceSchedules(ctx context.Context, _ *asynq.Task) error {
	if p.repricer == nil {
		return nil
	}
	executed, err := p.repricer.ExecuteDueSchedules(ctx, time.Now().UTC())
	if err != nil {
		p.logger.Error().Err(err).Msg("execute due price schedules failed")
		return err
	}
	if executed > 0 {
		p.logger.Info().Int("executed", executed).Msg("executed due price schedules")
	}
	return nil
}

func (p *Processor) HandlePollPriceTasks(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	if p.repricer == nil {
		return nil
	}
	terminal, err := p.repricer.PollUploadTasks(ctx, workspaceID)
	if err != nil {
		p.logger.Error().Err(err).Str("workspace_id", workspaceID.String()).Msg("price task poll failed")
		return err
	}
	if terminal > 0 {
		p.logger.Info().Str("workspace_id", workspaceID.String()).Int("terminal", terminal).Msg("price task poll completed")
	}
	return nil
}

// HandleSweepRefreshIntegrations auto-discovers new WB integrations from Sellico.
func (p *Processor) HandleSweepRefreshIntegrations(ctx context.Context, _ *asynq.Task) error {
	if p.integrationRefresher == nil {
		p.logger.Debug().Msg("integration refresher not configured, skipping")
		return nil
	}
	return p.integrationRefresher.Refresh(ctx)
}

func (p *Processor) HandleSyncWorkspace(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	metadata := workspaceTaskMetadata(payload)
	cabinetID, phase, err := workspaceSyncTaskScope(metadata)
	if err != nil {
		return err
	}
	lockKey := workspaceID.String() + ":fanout"
	if cabinetID != nil {
		lockKey = workspaceID.String() + ":" + cabinetID.String()
	}
	unlock, locked := tryWorkspaceSyncLock(lockKey)
	if !locked {
		p.logger.Warn().Str("workspace_id", workspaceID.String()).Msg("workspace sync already running, skipping duplicate task")
		p.markDuplicateWorkspaceSyncSkipped(ctx, payload, workspaceID)
		return nil
	}
	defer unlock()
	return p.runWithJobRun(ctx, TaskSyncWorkspace, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}

		p.logger.Info().Str("workspace_id", workspaceID.String()).Msg("sync workspace started")

		// Cabinet tasks have independent asynq deadlines and readiness state.
		if cabinetID != nil {
			cabIDStr := cabinetID.String()
			var summary service.SyncSummary
			var syncErr error
			if phase != "" {
				p.logger.Info().Str("cabinet_id", cabIDStr).Str("sync_phase", phase).Msg("starting single cabinet sync phase")
				summary, syncErr = p.syncService.SyncSingleCabinetPhase(ctx, workspaceID, *cabinetID, phase)
			} else {
				p.logger.Info().Str("cabinet_id", cabIDStr).Msg("starting single cabinet sync")
				summary, syncErr = p.syncService.SyncSingleCabinet(ctx, workspaceID, *cabinetID)
			}
			result := singleCabinetSyncResult(summary, cabIDStr, phase)
			if retryResult := p.enqueueRateLimitedSingleCabinetRetries(workspaceID, *cabinetID, metadata, summary); len(retryResult) > 0 {
				result["phase_retries_queued"] = retryResult
			}
			mergeIntoMetadata(result, syncSummaryMetadata(summary))
			if syncErr != nil {
				p.notifySyncResult(ctx, workspaceID, "partial", nil)
				return result, syncErr
			}
			p.notifySyncResult(ctx, workspaceID, "ok", nil)

			recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
			if enqueueErr == nil {
				result["recommendation_generation"] = recommendationTaskStatus
			}
			return result, nil
		}

		// Scheduled workspace sync is a fan-out only. Running every cabinet in one
		// 60-minute context let the first large cabinet starve all later cabinets.
		cabinetIDs, listErr := p.syncService.ListWorkspaceCabinetIDs(ctx, workspaceID)
		if listErr != nil {
			return nil, listErr
		}
		result, enqueueErr := p.enqueueCabinetSyncFanout(workspaceID, cabinetIDs)
		if enqueueErr != nil {
			return result, enqueueErr
		}
		return result, nil
	})
}

func tryWorkspaceSyncLock(key string) (func(), bool) {
	value, _ := workspaceSyncLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, false
	}
	return func() {
		lock.Unlock()
		workspaceSyncLocks.Delete(key)
	}, true
}

func workspaceSyncTaskScope(metadata map[string]any) (*uuid.UUID, string, error) {
	raw := stringFromMetadata(metadata, "seller_cabinet_id")
	if raw == "" {
		return nil, "", nil
	}
	cabinetID, err := uuid.Parse(raw)
	if err != nil {
		return nil, "", fmt.Errorf("parse seller_cabinet_id: %w", err)
	}
	return &cabinetID, stringFromMetadata(metadata, "sync_phase"), nil
}

func (p *Processor) enqueueCabinetSyncFanout(workspaceID uuid.UUID, cabinetIDs []uuid.UUID) (map[string]any, error) {
	result := map[string]any{
		"mode":                   "cabinet_fanout",
		"cabinets":               len(cabinetIDs),
		"cabinet_tasks_enqueued": 0,
	}
	if p.client == nil {
		return result, fmt.Errorf("task enqueuer is not configured")
	}

	enqueued := 0
	duplicates := 0
	var enqueueErrors []error
	for _, cabinetID := range cabinetIDs {
		task, err := NewWorkspaceTaskWithMetadata(TaskSyncWorkspace, workspaceID, nil, map[string]any{
			"seller_cabinet_id": cabinetID.String(),
			"trigger":           "scheduled_workspace_fanout",
		})
		if err != nil {
			enqueueErrors = append(enqueueErrors, fmt.Errorf("cabinet %s task: %w", cabinetID, err))
			continue
		}
		_, err = p.client.Enqueue(task,
			asynq.Queue(QueueWBSync),
			asynq.MaxRetry(0),
			asynq.Timeout(workspaceSyncTaskTimeout),
			asynq.Unique(55*time.Minute),
		)
		if errors.Is(err, asynq.ErrDuplicateTask) {
			duplicates++
			continue
		}
		if err != nil {
			enqueueErrors = append(enqueueErrors, fmt.Errorf("cabinet %s enqueue: %w", cabinetID, err))
			continue
		}
		enqueued++
	}
	result["cabinet_tasks_enqueued"] = enqueued
	result["cabinet_tasks_duplicate"] = duplicates
	return result, errors.Join(enqueueErrors...)
}

func (p *Processor) markDuplicateWorkspaceSyncSkipped(ctx context.Context, payload WorkspaceTaskPayload, workspaceID uuid.UUID) {
	if p.queries == nil || payload.JobRunID == "" {
		return
	}
	jobRunID, err := parseOptionalUUID(payload.JobRunID)
	if err != nil || jobRunID == nil {
		return
	}
	metadata := workspaceTaskMetadata(payload)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["result_state"] = jobResultStatePartial
	metadata["duplicate_skipped"] = true
	metadata["workspace_id"] = workspaceID.String()
	_, err = p.queries.UpdateJobRunStatus(ctx, sqlcgen.UpdateJobRunStatusParams{
		ID:           optionalUUIDToPgtype(jobRunID),
		Status:       "partial",
		FinishedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		ErrorMessage: pgtype.Text{String: "workspace sync already running", Valid: true},
		Metadata:     mustJSON(metadata),
	})
	if err != nil {
		p.logger.Warn().Err(err).Str("job_run_id", payload.JobRunID).Msg("failed to mark duplicate workspace sync skipped")
	}
}

func singleCabinetSyncResult(summary service.SyncSummary, cabinetID, phase string) map[string]any {
	mode := "single_cabinet"
	if phase != "" {
		mode = "single_cabinet_phase"
	}
	result := map[string]any{
		"cabinets":             1,
		"campaigns":            summary.Campaigns,
		"campaign_stats":       summary.CampaignStats,
		"product_stats":        summary.ProductStats,
		"campaign_budgets":     summary.CampaignBudgets,
		"ad_balances":          summary.AdBalances,
		"finance_docs":         summary.FinanceDocs,
		"sales_funnel":         summary.SalesFunnel,
		"business_orders":      summary.BusinessOrders,
		"business_sales":       summary.BusinessSales,
		"phrases":              summary.Phrases,
		"phrase_stats":         summary.PhraseStats,
		"products":             summary.Products,
		"products_with_stats":  summary.ProductStats,
		"campaigns_with_stats": summary.CampaignStats,
		"phrases_with_stats":   summary.PhraseStats,
		"skipped":              summary.SkippedCampaign,
		"sync_issues":          summary.Issues,
		"mode":                 mode,
		"cabinet_id":           cabinetID,
	}
	if phase != "" {
		result["sync_phase"] = phase
	}
	return result
}

func (p *Processor) enqueueRateLimitedSingleCabinetRetries(workspaceID, cabinetID uuid.UUID, metadata map[string]any, summary service.SyncSummary) []map[string]any {
	if p.client == nil || summary.RateLimited == 0 {
		return nil
	}
	currentRetry := intFromMetadata(metadata, "phase_retry_count")
	if currentRetry >= maxPhaseRetryCount {
		p.logger.Warn().
			Str("cabinet_id", cabinetID.String()).
			Int("phase_retry_count", currentRetry).
			Msg("single cabinet phase retry limit reached")
		return nil
	}

	phases := rateLimitedPhases(summary)
	if len(phases) == 0 {
		return nil
	}
	runAt := phaseRetryAt(summary)
	queued := make([]map[string]any, 0, len(phases))
	for _, phase := range phases {
		nextMetadata := copyMetadata(metadata)
		nextMetadata["seller_cabinet_id"] = cabinetID.String()
		nextMetadata["task_type"] = TaskSyncWorkspace
		nextMetadata["trigger"] = "rate_limit_phase_retry"
		nextMetadata["sync_phase"] = phase
		nextMetadata["phase_retry_count"] = currentRetry + 1
		nextMetadata["scheduled_after_rate_limit"] = true
		nextMetadata["scheduled_for"] = runAt.Format(time.RFC3339)
		if summary.RateLimitEndpoint != "" {
			nextMetadata["rate_limit_endpoint"] = summary.RateLimitEndpoint
		}

		task, err := NewWorkspaceTaskWithMetadata(TaskSyncWorkspace, workspaceID, nil, nextMetadata)
		if err != nil {
			p.logger.Warn().Err(err).Str("phase", phase).Msg("failed to create phase retry task")
			continue
		}
		taskID := fmt.Sprintf("wb-sync-phase:%s:%s:%d", cabinetID.String(), phase, runAt.Unix()/300)
		_, err = p.client.Enqueue(task,
			asynq.Queue(QueueWBSync),
			asynq.MaxRetry(0),
			asynq.Timeout(workspaceSyncTaskTimeout),
			asynq.ProcessAt(runAt),
			asynq.TaskID(taskID),
		)
		if err != nil {
			if errors.Is(err, asynq.ErrDuplicateTask) {
				queued = append(queued, map[string]any{
					"phase":  phase,
					"status": "already_queued",
					"run_at": runAt.Format(time.RFC3339),
				})
				continue
			}
			p.logger.Warn().Err(err).Str("phase", phase).Msg("failed to enqueue phase retry")
			continue
		}
		queued = append(queued, map[string]any{
			"phase":  phase,
			"status": "queued",
			"run_at": runAt.Format(time.RFC3339),
		})
	}
	return queued
}

func copyMetadata(metadata map[string]any) map[string]any {
	result := make(map[string]any, len(metadata)+4)
	for key, value := range metadata {
		if key == "payload" {
			continue
		}
		result[key] = value
	}
	return result
}

func phaseRetryAt(summary service.SyncSummary) time.Time {
	now := time.Now().UTC()
	runAt := now.Add(phaseRetryDelayFloor)
	if summary.NextAllowedAt != nil {
		candidate := summary.NextAllowedAt.UTC()
		if candidate.Before(now) {
			candidate = now
		}
		candidate = candidate.Add(phaseRetryDelayFloor)
		if candidate.After(runAt) {
			runAt = candidate
		}
	}
	return runAt
}

func rateLimitedPhases(summary service.SyncSummary) []string {
	seen := map[string]struct{}{}
	add := func(phase string) {
		if phase == "" {
			return
		}
		seen[phase] = struct{}{}
	}

	for _, issue := range summary.Issues {
		if !messageLooksRateLimited(issue.Message) {
			continue
		}
		add(syncPhaseForIssueStage(issue.Stage))
	}
	add(syncPhaseForEndpoint(summary.RateLimitEndpoint))

	result := make([]string, 0, len(seen))
	order := []string{
		service.SyncPhaseCampaigns,
		service.SyncPhaseStats,
		service.SyncPhasePhrases,
		service.SyncPhaseBudgets,
		service.SyncPhaseFinance,
		service.SyncPhaseSalesFunnel,
		service.SyncPhaseBusinessReports,
	}
	for _, phase := range order {
		if _, ok := seen[phase]; ok {
			result = append(result, phase)
		}
	}
	return result
}

func messageLooksRateLimited(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "429") ||
		strings.Contains(lower, "rate limited") ||
		strings.Contains(lower, "too many requests")
}

func syncPhaseForIssueStage(stage string) string {
	switch {
	case strings.HasPrefix(stage, "campaigns"):
		return service.SyncPhaseCampaigns
	case strings.HasPrefix(stage, "stats"), strings.HasPrefix(stage, "campaign_stats"), strings.HasPrefix(stage, "product_stats"):
		return service.SyncPhaseStats
	case strings.HasPrefix(stage, "phrases"), strings.HasPrefix(stage, "phrase_stats"):
		return service.SyncPhasePhrases
	case strings.HasPrefix(stage, "campaign_budgets"):
		return service.SyncPhaseBudgets
	case strings.HasPrefix(stage, "ad_finance"), strings.HasPrefix(stage, "ad_balance"):
		return service.SyncPhaseFinance
	case strings.HasPrefix(stage, "sales_funnel_products"):
		return service.SyncPhaseSalesFunnel
	case strings.HasPrefix(stage, "business_reports"), strings.HasPrefix(stage, "business_orders"), strings.HasPrefix(stage, "business_sales"):
		return service.SyncPhaseBusinessReports
	default:
		return ""
	}
}

func syncPhaseForEndpoint(endpoint string) string {
	switch endpoint {
	case "adv_adverts":
		return service.SyncPhaseCampaigns
	case "adv_fullstats":
		return service.SyncPhaseStats
	case "adv_normquery_stats":
		return service.SyncPhasePhrases
	case "adv_budget":
		return service.SyncPhaseBudgets
	case "adv_finance":
		return service.SyncPhaseFinance
	case "analytics_sales_funnel":
		return service.SyncPhaseSalesFunnel
	default:
		return ""
	}
}

func (p *Processor) HandleSweepSyncWorkspace(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncWorkspace, TaskSyncWorkspace, QueueWBSync)
}

func (p *Processor) HandleSyncCampaigns(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncCampaigns, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncCampaigns(ctx, workspaceID)
		result := map[string]any{
			"cabinets":    summary.Cabinets,
			"campaigns":   summary.Campaigns,
			"sync_issues": summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		return result, nil
	})
}

func (p *Processor) HandleSyncCampaignStats(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncCampaignStats, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncCampaignStats(ctx, workspaceID)
		result := map[string]any{
			"cabinets":       summary.Cabinets,
			"campaign_stats": summary.CampaignStats,
			"skipped":        summary.SkippedCampaign,
			"sync_issues":    summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus
		return result, nil
	})
}

func (p *Processor) HandleSyncPhrases(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncPhrases, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncPhrases(ctx, workspaceID)
		result := map[string]any{
			"cabinets":     summary.Cabinets,
			"phrases":      summary.Phrases,
			"phrase_stats": summary.PhraseStats,
			"skipped":      summary.SkippedCampaign,
			"sync_issues":  summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus
		return result, nil
	})
}

func (p *Processor) HandleSyncProducts(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncProducts, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncProducts(ctx, workspaceID)
		result := map[string]any{
			"cabinets":    summary.Cabinets,
			"products":    summary.Products,
			"sync_issues": summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus
		return result, nil
	})
}

func (p *Processor) HandleGenerateRecommendations(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskGenerateRecommendations, &workspaceID, payload, func() (map[string]any, error) {
		recommendations, runErr := p.engine.GenerateForWorkspace(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"generated": len(recommendations)}, nil
	})
}

func (p *Processor) HandleGenerateExport(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, exportID, err := parseExportPayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskGenerateExport, &workspaceID, WorkspaceTaskPayload{
		WorkspaceID: workspaceID.String(),
		Metadata: map[string]any{
			"export_id":      exportID.String(),
			"export_payload": payload,
		},
	}, func() (map[string]any, error) {
		if p.exportGenerator == nil {
			return nil, fmt.Errorf("export generator is not configured")
		}
		exportTask, runErr := p.exportGenerator.Generate(ctx, workspaceID, exportID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{
			"export_id": exportTask.ID.String(),
			"status":    exportTask.Status,
			"format":    exportTask.Format,
			"entity":    exportTask.EntityType,
		}, nil
	})
}

func (p *Processor) HandleSweepSyncCampaigns(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncCampaigns, TaskSyncCampaigns, QueueWBCampaigns)
}

func (p *Processor) HandleSweepSyncCampaignStats(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncCampaignStats, TaskSyncCampaignStats, QueueWBCampaignStats)
}

func (p *Processor) HandleSweepSyncPhrases(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncPhrases, TaskSyncPhrases, QueueWBPhrases)
}

func (p *Processor) HandleSweepSyncProducts(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncProducts, TaskSyncProducts, QueueWBProducts)
}

func (p *Processor) HandleSweepRecommendations(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepRecommendations, TaskGenerateRecommendations, QueueRecommendations)
}

func (p *Processor) runSweep(ctx context.Context, sweepTaskType, tenantTaskType, queue string) error {
	return p.runWithJobRun(ctx, sweepTaskType, nil, WorkspaceTaskPayload{}, func() (map[string]any, error) {
		workspaces, err := p.queries.ListWorkspaces(ctx, sqlcgen.ListWorkspacesParams{Limit: 1000, Offset: 0})
		if err != nil {
			return nil, err
		}

		enqueued := 0
		for _, workspace := range workspaces {
			workspaceID := uuid.UUID(workspace.ID.Bytes)
			if taskErr := p.enqueueWorkspaceTask(tenantTaskType, workspaceID, queue); taskErr != nil {
				if errors.Is(taskErr, asynq.ErrDuplicateTask) {
					continue
				}
				return nil, taskErr
			}
			enqueued++
		}

		return map[string]any{"queue": queue, "enqueued": enqueued}, nil
	})
}

func (p *Processor) runWithJobRun(ctx context.Context, taskType string, workspaceID *uuid.UUID, payload WorkspaceTaskPayload, fn func() (map[string]any, error)) error {
	metadata := workspaceTaskMetadata(payload)
	jobRunID, err := parseOptionalUUID(payload.JobRunID)
	if err != nil {
		return fmt.Errorf("parse job run id: %w", err)
	}

	var jobRun sqlcgen.JobRun
	if jobRunID != nil {
		jobRun, err = p.queries.UpdateJobRunStatus(ctx, sqlcgen.UpdateJobRunStatusParams{
			ID:           optionalUUIDToPgtype(jobRunID),
			Status:       "running",
			FinishedAt:   pgtype.Timestamptz{},
			ErrorMessage: pgtype.Text{},
			Metadata:     mustJSON(metadata),
		})
		if err != nil {
			return err
		}
	} else {
		jobRun, err = p.queries.CreateJobRun(ctx, sqlcgen.CreateJobRunParams{
			WorkspaceID: optionalUUIDToPgtype(workspaceID),
			TaskType:    taskType,
			Status:      "running",
			StartedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			Metadata:    mustJSON(metadata),
		})
		if err != nil {
			return err
		}
	}

	finalMeta, runErr := fn()
	status := "completed"
	errorMessage := pgtype.Text{}
	if runErr != nil {
		status = "failed"
		errorMessage = pgtype.Text{String: runErr.Error(), Valid: true}
	}
	metadata = mergeMetadata(metadata, finalMeta)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["result_state"] = deriveResultState(taskType, metadata, runErr)
	shouldRetry := runErr != nil
	finalUpdateCtx, cancel := context.WithTimeout(context.Background(), jobRunFinalUpdateTimeout)
	defer cancel()
	if runErr != nil && metadata["result_state"] == jobResultStatePartial {
		status = "partial"
		shouldRetry = false
	}
	if _, err := p.queries.UpdateJobRunStatus(finalUpdateCtx, sqlcgen.UpdateJobRunStatusParams{
		ID:           jobRun.ID,
		Status:       status,
		FinishedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		ErrorMessage: errorMessage,
		Metadata:     mustJSON(metadata),
	}); err != nil {
		p.logger.Error().Err(err).Str("task_type", taskType).Msg("failed to update job run status")
	}

	if shouldRetry {
		return runErr
	}
	return nil
}

func deriveResultState(taskType string, metadata map[string]any, runErr error) string {
	if runErr != nil {
		if hasMeaningfulProgress(metadata) {
			return jobResultStatePartial
		}
		return jobResultStateFailed
	}

	if strings.HasPrefix(taskType, "wb:sweep_") {
		if intFromMetadata(metadata, "enqueued") > 0 {
			return jobResultStateOK
		}
		return jobResultStateEmpty
	}

	if issueCountFromMetadata(metadata) > 0 {
		if hasMeaningfulProgress(metadata) {
			return jobResultStatePartial
		}
		return jobResultStateFailed
	}

	if hasMeaningfulProgress(metadata) {
		return jobResultStateOK
	}

	return jobResultStateEmpty
}

func hasMeaningfulProgress(metadata map[string]any) bool {
	keys := []string{
		"cabinets",
		"campaigns",
		"campaign_stats",
		"phrases",
		"phrase_stats",
		"products",
		"product_stats",
		"campaign_budgets",
		"business_orders",
		"business_sales",
		"generated",
		"enqueued",
		"report_generated",
	}
	for _, key := range keys {
		if intFromMetadata(metadata, key) > 0 {
			return true
		}
	}
	return false
}

func issueCountFromMetadata(metadata map[string]any) int {
	issues, ok := metadata["sync_issues"]
	if !ok {
		return 0
	}
	list, ok := issues.([]service.SyncIssue)
	if ok {
		return len(list)
	}
	values, ok := issues.([]any)
	if ok {
		return len(values)
	}
	return 0
}

func intFromMetadata(metadata map[string]any, key string) int {
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func stringFromMetadata(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func clientAuditReportParams(metadata any, now time.Time) (time.Time, time.Time, *uuid.UUID, error) {
	values, _ := metadata.(map[string]any)
	dateTo := truncateUTCDate(now)
	dateFrom := dateTo.AddDate(0, 0, -29)
	if raw := stringFromAnyMap(values, "date_to"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}, time.Time{}, nil, fmt.Errorf("parse date_to: %w", err)
		}
		dateTo = parsed
	}
	if raw := stringFromAnyMap(values, "date_from"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}, time.Time{}, nil, fmt.Errorf("parse date_from: %w", err)
		}
		dateFrom = parsed
	}
	if dateFrom.After(dateTo) {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("date_from must be before or equal to date_to")
	}

	var sellerCabinetID *uuid.UUID
	if raw := stringFromAnyMap(values, "seller_cabinet_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return time.Time{}, time.Time{}, nil, fmt.Errorf("parse seller_cabinet_id: %w", err)
		}
		sellerCabinetID = &parsed
	}
	return dateFrom, dateTo, sellerCabinetID, nil
}

func stringFromAnyMap(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func truncateUTCDate(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func filterReportRecommendationsBySellerCabinet(items []domain.Recommendation, sellerCabinetID *uuid.UUID) []domain.Recommendation {
	if sellerCabinetID == nil {
		return items
	}
	filtered := make([]domain.Recommendation, 0, len(items))
	for _, item := range items {
		if item.SellerCabinetID == nil || *item.SellerCabinetID != *sellerCabinetID {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (p *Processor) enqueueRecommendationGeneration(workspaceID uuid.UUID) (string, error) {
	if err := p.enqueueWorkspaceTask(TaskGenerateRecommendations, workspaceID, QueueRecommendations, asynq.Unique(5*time.Minute)); err != nil {
		if errors.Is(err, asynq.ErrDuplicateTask) {
			return "already_queued", nil
		}
		return "", err
	}
	return "enqueued", nil
}

func (p *Processor) enqueueWorkspaceTask(taskType string, workspaceID uuid.UUID, queue string, extraOpts ...asynq.Option) error {
	if p.client == nil {
		return fmt.Errorf("task client is not configured")
	}

	task, err := NewWorkspaceTask(taskType, workspaceID)
	if err != nil {
		return err
	}

	timeout := defaultTaskTimeout
	if taskType == TaskSyncWorkspace {
		timeout = workspaceSyncTaskTimeout
	}
	opts := []asynq.Option{
		asynq.Queue(queue),
		asynq.MaxRetry(10),
		asynq.Timeout(timeout),
	}
	if taskType == TaskSyncWorkspace {
		opts = append(opts, asynq.Unique(55*time.Minute))
	}
	if taskType == TaskBidAutomation {
		// A workspace must have at most one pending/running autobid task. The
		// database-side action claim is the final safety barrier; this queue
		// uniqueness also prevents redundant evaluations under scheduler races.
		opts = append(opts, asynq.Unique(10*time.Minute))
	}
	opts = append(opts, extraOpts...)

	_, err = p.client.Enqueue(task, opts...)
	return err
}

func parseWorkspacePayload(payload []byte) (WorkspaceTaskPayload, uuid.UUID, error) {
	var decoded WorkspaceTaskPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return WorkspaceTaskPayload{}, uuid.Nil, fmt.Errorf("decode task payload: %w", err)
	}
	workspaceID, err := uuid.Parse(decoded.WorkspaceID)
	if err != nil {
		return WorkspaceTaskPayload{}, uuid.Nil, fmt.Errorf("parse workspace id: %w", err)
	}
	return decoded, workspaceID, nil
}

func workspaceTaskMetadata(payload WorkspaceTaskPayload) map[string]any {
	result := map[string]any{
		"payload": payload,
	}
	if payload.Metadata == nil {
		return result
	}

	if metadata, ok := payload.Metadata.(map[string]any); ok {
		for key, value := range metadata {
			result[key] = value
		}
	}

	return result
}

func parseOptionalUUID(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseExportPayload(payload []byte) (ExportTaskPayload, uuid.UUID, uuid.UUID, error) {
	var decoded ExportTaskPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ExportTaskPayload{}, uuid.Nil, uuid.Nil, fmt.Errorf("decode export task payload: %w", err)
	}
	workspaceID, err := uuid.Parse(decoded.WorkspaceID)
	if err != nil {
		return ExportTaskPayload{}, uuid.Nil, uuid.Nil, fmt.Errorf("parse workspace id: %w", err)
	}
	exportID, err := uuid.Parse(decoded.ExportID)
	if err != nil {
		return ExportTaskPayload{}, uuid.Nil, uuid.Nil, fmt.Errorf("parse export id: %w", err)
	}
	return decoded, workspaceID, exportID, nil
}

func mustJSON(metadata map[string]any) []byte {
	if metadata == nil {
		return []byte("{}")
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	result := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func mergeIntoMetadata(base map[string]any, extra map[string]any) {
	for key, value := range extra {
		base[key] = value
	}
}

func optionalUUIDToPgtype(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func maxInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonNilTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func collectSyncIssues(summaries ...service.SyncSummary) []service.SyncIssue {
	total := 0
	for _, summary := range summaries {
		total += len(summary.Issues)
	}
	if total == 0 {
		return nil
	}

	result := make([]service.SyncIssue, 0, total)
	for _, summary := range summaries {
		result = append(result, summary.Issues...)
	}
	return result
}

func syncSummaryMetadata(summary service.SyncSummary) map[string]any {
	reasons := partialReasons(summary.Issues)
	result := map[string]any{
		"wb_errors":        summary.WBErrors,
		"rate_limited":     summary.RateLimited > 0,
		"rate_limit_count": summary.RateLimited,
	}
	if summary.RateLimitEndpoint != "" {
		result["rate_limit_endpoint"] = summary.RateLimitEndpoint
	}
	if summary.RetryAfterSeconds > 0 {
		result["retry_after_seconds"] = summary.RetryAfterSeconds
	}
	if summary.NextAllowedAt != nil {
		result["next_allowed_at"] = summary.NextAllowedAt.Format(time.RFC3339)
	}
	if summary.DateFrom != "" {
		result["date_from"] = summary.DateFrom
	}
	if summary.DateTo != "" {
		result["date_to"] = summary.DateTo
	}
	if len(reasons) > 0 {
		result["partial_reasons"] = reasons
	}
	return result
}

func collectSyncMetadata(summaries ...service.SyncSummary) (int, int, []string) {
	wbErrors := 0
	rateLimited := 0
	issues := make([]service.SyncIssue, 0)
	for _, summary := range summaries {
		wbErrors += summary.WBErrors
		rateLimited += summary.RateLimited
		issues = append(issues, summary.Issues...)
	}
	return wbErrors, rateLimited, partialReasons(issues)
}

func partialReasons(issues []service.SyncIssue) []string {
	if len(issues) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(issues))
	result := make([]string, 0, len(issues))
	for _, issue := range issues {
		reason := issue.Stage
		if reason == "" {
			reason = "sync"
		}
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
	}
	return result
}

func (p *Processor) notifySyncResult(ctx context.Context, workspaceID uuid.UUID, status string, issues []service.SyncIssue) {
	if p.notifier == nil {
		return
	}
	issueStrings := make([]string, len(issues))
	for i, issue := range issues {
		if issue.EntityID != "" {
			issueStrings[i] = fmt.Sprintf("[%s] %s: %s", issue.Stage, issue.EntityID, issue.Message)
		} else {
			issueStrings[i] = fmt.Sprintf("[%s] %s", issue.Stage, issue.Message)
		}
	}
	p.notifier.NotifySyncComplete(ctx, workspaceID, status, issueStrings)
}

func firstNonNilError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
