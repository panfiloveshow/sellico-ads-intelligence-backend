package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type aiExecutionResult struct {
	Proposal aiProposal
	Status   string
	Reason   string
}

// A submission is a plan: an unavailable step invalidates the plan before
// any external write. Runtime failures stop remaining steps, and the digest
// is derived from outcomes, never the model's unexecuted summary.
func (s *OzonAIManagerService) executeAISubmission(ctx context.Context, run sqlcgen.AiRun, workspaceID, cabinetID uuid.UUID, strategy domain.Strategy, submission *aiSubmission) (string, error) {
	var results []aiExecutionResult
	var data *aiCabinetData
	err := s.queries.WithOzonAIExecutionLock(ctx, uuidToPgtype(cabinetID), func(q *sqlcgen.Queries) error {
		manager := *s
		manager.queries = q
		current, err := q.GetActiveOzonAIStrategyForCabinet(ctx, uuidToPgtype(cabinetID))
		if err != nil {
			return fmt.Errorf("strategy unavailable before execution: %w", err)
		}
		if uuidFromPgtype(current.ID) != strategy.ID {
			return fmt.Errorf("strategy changed while AI was planning")
		}
		strategy = strategyFromSqlc(current)
		params := strategy.Params.Merged()
		_, data, err = manager.buildAIContext(ctx, workspaceID, cabinetID, strategy, params)
		if err != nil {
			return err
		}
		verdicts := make([]string, len(submission.Proposals))
		planReason := ""
		seen := map[string]bool{}
		for i, p := range submission.Proposals {
			key := aiProposalTargetKey(p)
			if seen[key] {
				verdicts[i] = "В одном плане повторяется или противоречит себе действие для той же цели"
			} else {
				seen[key] = true
				verdicts[i] = manager.evaluateProposal(ctx, cabinetID, params, p, data)
				if verdicts[i] == "" && p.ActionType == domain.AIActionCPOEnable && params.AutomationLevel >= 2 {
					verdicts[i] = manager.liveCPOEnableGuard(ctx, workspaceID, cabinetID, p, data, params)
				}
			}
			if verdicts[i] != "" && planReason == "" {
				planReason = verdicts[i]
			}
		}
		// Financial/lifecycle dependencies are checked as a whole before the
		// first action, including the cabinet's remaining daily capacity.
		if planReason == "" && params.AutomationLevel >= 3 && params.MaxActionsPerDay > 0 {
			n, err := q.CountAppliedAIActionsToday(ctx, sqlcgen.CountAppliedAIActionsTodayParams{SellerCabinetID: uuidToPgtype(cabinetID), DayStart: aiExecutionDayStart()})
			if err != nil {
				return err
			}
			if n+int64(len(submission.Proposals)) > int64(params.MaxActionsPerDay) {
				planReason = "Для всего плана недостаточно оставшегося дневного лимита действий"
			}
		}
		order := make([]int, len(submission.Proposals))
		for i := range order {
			order[i] = i
		}
		// Switching a campaign must not precede bid/budget/CPO changes that
		// were the reason for switching it. Preserve order within each stage.
		sort.SliceStable(order, func(i, j int) bool {
			return aiExecutionStage(submission.Proposals[order[i]]) < aiExecutionStage(submission.Proposals[order[j]])
		})
		results = make([]aiExecutionResult, len(order))
		for i, p := range submission.Proposals {
			results[i] = aiExecutionResult{Proposal: p, Status: domain.AIDecisionStatusRejectedByGuardrail, Reason: "Шаг не начат: выполнение плана прервано"}
		}
		stopped := ""
		for _, i := range order {
			p := submission.Proposals[i]
			verdict := verdicts[i]
			if verdict == "" && planReason != "" {
				verdict = "План не выполнен: " + planReason
			}
			if verdict == "" && stopped != "" {
				verdict = "Оставшиеся действия не выполнены: " + stopped
			}
			status, err := manager.processProposalWithVerdict(ctx, run, workspaceID, cabinetID, params, p, data, verdict)
			if err != nil {
				if status == domain.AIDecisionStatusApproved {
					results[i] = aiExecutionResult{Proposal: p, Status: status, Reason: "Команда могла быть выполнена; результат не удалось сохранить. Требуется сверка с Ozon"}
				}
				return err
			}
			results[i] = aiExecutionResult{Proposal: p, Status: status, Reason: verdict}
			if status == domain.AIDecisionStatusFailed || status == domain.AIDecisionStatusRejectedByGuardrail {
				if stopped == "" {
					stopped = "предыдущий шаг не прошёл проверку или Ozon не подтвердил его выполнение"
				}
			}
		}
		return nil
	})
	if err != nil && len(results) == 0 {
		return "", err
	}
	executionErr := err
	// Reload the actual persisted reasons, including failures from Ozon.
	rows, err := s.queries.ListAIDecisions(ctx, sqlcgen.ListAIDecisionsParams{WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: uuidToPgtype(cabinetID), RunID: run.ID, Limit: int32(len(results) + 1)})
	if err != nil {
		executionErr = errors.Join(executionErr, fmt.Errorf("load execution outcomes: %w", err))
	}
	for i := range results {
		for _, row := range rows {
			if row.ActionType == results[i].Proposal.ActionType && aiDecisionMatchesProposal(row, results[i].Proposal) {
				results[i].Status = row.Status
				if row.Error.Valid {
					results[i].Reason = row.Error.String
				} else if row.GuardrailVerdict != "passed" {
					results[i].Reason = row.GuardrailVerdict
				}
				break
			}
		}
	}
	digest, applied := aiExecutionDigest(results, data, strategy.Params.Merged().AutomationLevel)
	if executionErr != nil {
		digest += "\nВыполнение прервано ошибкой. Подтверждённые изменения перечислены выше; перед повтором требуется проверить неподтверждённые шаги."
	}
	if applied > 0 {
		title := "ИИ-автопилот применил изменения"
		if applied < len(results) {
			title = "ИИ-автопилот выполнил план частично"
		}
		s.notifier.NotifyWorkspaceOwners(ctx, workspaceID, "ads-ai-applied", title, digest)
	}
	return digest, executionErr
}

func aiDecisionMatchesProposal(row sqlcgen.AiDecision, p aiProposal) bool {
	var target domain.AIDecisionTarget
	return json.Unmarshal(row.Target, &target) == nil && target == p.Target
}

func aiExecutionStage(p aiProposal) int {
	switch p.ActionType {
	case domain.AIActionCampaignActivate, domain.AIActionCampaignPause:
		return 2
	case domain.AIActionCPOEnable:
		return 1
	default:
		return 0
	}
}

func aiProposalTargetKey(p aiProposal) string {
	p.Target = aiCanonicalTarget(p)
	action := p.ActionType
	if action == domain.AIActionCampaignPause || action == domain.AIActionCampaignActivate {
		action = "campaign_state"
	}
	if action == domain.AIActionCPOEnable || action == domain.AIActionCPODisable {
		action = "cpo_state"
	}
	return fmt.Sprintf("%s:%d:%d", action, p.Target.OzonCampaignID, p.Target.SKU)
}

func aiCanonicalTarget(p aiProposal) domain.AIDecisionTarget {
	target := p.Target
	switch p.ActionType {
	case domain.AIActionCampaignPause, domain.AIActionCampaignActivate, domain.AIActionBudgetChange:
		target.SKU = 0
	case domain.AIActionCPOEnable, domain.AIActionCPODisable, domain.AIActionCPOBid:
		target.OzonCampaignID = 0
	}
	return target
}

func aiExecutionDigest(results []aiExecutionResult, data *aiCabinetData, level int) (string, int) {
	applied, proposed, blocked, failed := 0, 0, 0, 0
	var lines []string
	for _, r := range results {
		label := aiActionLabel(r.Proposal, data)
		switch r.Status {
		case domain.AIDecisionStatusAutoApplied, domain.AIDecisionStatusApplied:
			applied++
			lines = append(lines, "Выполнено: "+label+".")
		case domain.AIDecisionStatusProposed:
			proposed++
			lines = append(lines, "Ожидает подтверждения: "+label+".")
		case domain.AIDecisionStatusShadow:
			if r.Reason != "" {
				blocked++
				lines = append(lines, "Недоступно: "+label+" — "+truncateError(r.Reason))
			} else {
				proposed++
				lines = append(lines, "Наблюдение, предложение: "+label+".")
			}
		case domain.AIDecisionStatusFailed, domain.AIDecisionStatusApproved:
			failed++
			lines = append(lines, "Выполнение не подтверждено: "+label+" — "+truncateError(r.Reason))
		default:
			blocked++
			lines = append(lines, "Не выполнено: "+label+" — "+truncateError(r.Reason))
		}
	}
	if len(results) == 0 {
		return "Изменений нет: ИИ не предложил действий.", 0
	}
	header := fmt.Sprintf("Выполнено: %d. Ожидает решения: %d. Отклонено: %d. Ошибок: %d.", applied, proposed, blocked, failed)
	if level <= 1 {
		header = fmt.Sprintf("Режим наблюдения: команды в Ozon не отправлялись. Предложений: %d, недоступных: %d.", proposed, blocked)
	}
	return header + "\n" + strings.Join(lines, "\n"), applied
}

func aiActionLabel(p aiProposal, data *aiCabinetData) string {
	target := fmt.Sprintf("кампания %d", p.Target.OzonCampaignID)
	if data != nil {
		if c, ok := data.campaignsByOzonID[p.Target.OzonCampaignID]; ok && c.Title.Valid {
			target = "кампания «" + c.Title.String + "»"
			for id, other := range data.campaignsByOzonID {
				if id != p.Target.OzonCampaignID && other.Title.Valid && other.Title.String == c.Title.String {
					target += fmt.Sprintf(" (№%d)", p.Target.OzonCampaignID)
					break
				}
			}
		}
	}
	value := 0.0
	if p.NewValue != nil {
		value = *p.NewValue
	}
	switch p.ActionType {
	case domain.AIActionCampaignPause:
		return "остановка " + strings.Replace(target, "кампания", "кампании", 1)
	case domain.AIActionCampaignActivate:
		return "возобновление " + strings.Replace(target, "кампания", "кампании", 1) + " с прежним способом оплаты"
	case domain.AIActionBudgetChange:
		return fmt.Sprintf("%s, бюджет %.0f ₽", target, value)
	case domain.AIActionBidChange:
		return fmt.Sprintf("%s, товар %d: ставка %.2f ₽", target, p.Target.SKU, value)
	case domain.AIActionCPOEnable:
		return fmt.Sprintf("включение оплаты за заказ для товара %d", p.Target.SKU)
	case domain.AIActionCPODisable:
		return fmt.Sprintf("отключение оплаты за заказ для товара %d", p.Target.SKU)
	case domain.AIActionCPOBid:
		return fmt.Sprintf("товар %d: ставка за заказ %.2f ₽", p.Target.SKU, value)
	default:
		return p.ActionType
	}
}

func aiToolsForContext(data *aiCabinetData) []llm.Tool {
	tools := aiTools()
	{
		var schema map[string]any
		if json.Unmarshal(tools[0].Function.Parameters, &schema) == nil {
			properties := schema["properties"].(map[string]any)
			items := properties["proposals"].(map[string]any)["items"].(map[string]any)
			actions := []string{domain.AIActionBidChange, domain.AIActionBudgetChange, domain.AIActionCampaignPause, domain.AIActionCampaignActivate}
			if len(data.cpoBySKU) > 0 {
				actions = append(actions, domain.AIActionCPOEnable, domain.AIActionCPODisable)
			}
			items["properties"].(map[string]any)["action_type"].(map[string]any)["enum"] = actions
			tools[0].Function.Parameters, _ = json.Marshal(schema)
		}
	}
	return tools
}

func aiExecutionDayStart() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: ozonStrategyDayStart(time.Now().UTC()), Valid: true}
}
func uuidToNullablePgtype(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return uuidToPgtype(id)
}
