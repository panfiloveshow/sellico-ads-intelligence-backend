package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"time"
)

type ManagerIntegrationVerifier interface {
	VerifyManagerIntegration(context.Context, string, string, string) (*domain.ManagerIntegration, error)
}
type ManagerAdsRepository interface {
	ManagerAdsCabinetByIntegration(context.Context, uuid.UUID, string) (*domain.ManagerAdsCabinet, error)
	ManagerAdsCampaigns(context.Context, domain.ManagerAdsCabinet, time.Time, time.Time, int, int) ([]domain.ManagerAdsCampaign, int64, error)
}
type ManagerAdsService struct {
	repo ManagerAdsRepository
	crm  ManagerIntegrationVerifier
}

var errManagerAdsUnavailable = &apperror.AppError{Code: "SERVICE_UNAVAILABLE", Message: "manager ads source unavailable", Status: 503}

func NewManagerAdsService(repo ManagerAdsRepository, crm ManagerIntegrationVerifier) *ManagerAdsService {
	return &ManagerAdsService{repo: repo, crm: crm}
}

func (s *ManagerAdsService) Campaigns(ctx context.Context, identity domain.ManagerAccess, workspaceID uuid.UUID, token, integrationID string, from, to time.Time, page, limit int, compare bool) (*domain.ManagerAdsReport, error) {
	if s == nil || s.repo == nil || s.crm == nil {
		return nil, apperror.New(errManagerAdsUnavailable, "manager ads source is not configured")
	}
	if !identity.Allows("marketing.view") || !identity.Allows("integrations.view") || workspaceID == uuid.Nil || identity.ExternalWorkspaceID == "" || identity.ExternalUserID == "" {
		return nil, apperror.New(apperror.ErrForbidden, "manager ads access denied")
	}
	days := int(to.Sub(from).Hours()/24) + 1
	if days < 1 || days > 90 || page < 1 || page > 10000 || limit < 1 || limit > 100 {
		return nil, apperror.New(apperror.ErrValidation, "invalid manager ads period or page")
	}
	integration, cabinet, err := s.resolveCabinet(ctx, identity, workspaceID, token, integrationID)
	if err != nil {
		return nil, err
	}
	rows, total, err := s.repo.ManagerAdsCampaigns(ctx, *cabinet, from, to, limit, (page-1)*limit)
	if err != nil {
		return nil, apperror.New(errManagerAdsUnavailable, "manager campaign statistics are unavailable")
	}
	report := &domain.ManagerAdsReport{WorkspaceID: identity.ExternalWorkspaceID, IntegrationID: integrationID, IntegrationName: integration.Name, Marketplace: integration.Marketplace, Cabinet: *cabinet,
		Period: domain.ManagerAdsPeriod{DateFrom: from.Format(time.DateOnly), DateTo: to.Format(time.DateOnly), Timezone: "Europe/Moscow"}, Items: rows, Page: page, Limit: limit, Total: total, HasMore: int64(page*limit) < total,
		Currency: "RUB", Source: "Sellico Ads: дневные записи площадки", MonetaryPrecision: "0.01 RUB", RetrievedAt: time.Now().UTC(), Partial: true,
		Warnings: []string{"Метрики суммируют только полученные дневные записи. Отсутствующая запись не равна нулю.", "Заказы и выручка имеют атрибуцию рекламного API; это не подтверждение выкупа или чистая прибыль."}}
	if cabinet.Marketplace == "wb" {
		report.MonetaryPrecision = "1 RUB (legacy stored precision)"
		report.Warnings = append(report.Warnings, "Существующая статистика WB хранит денежные суммы в целых рублях.")
	} else {
		report.Warnings = append(report.Warnings, "У существующих дневных записей Ozon нет времени обновления. Свежесть статистики не подтверждена временем получения отчёта.")
	}
	if compare {
		previousTo := from.AddDate(0, 0, -1)
		previousFrom := from.AddDate(0, 0, -days)
		previous, _, err := s.repo.ManagerAdsCampaigns(ctx, *cabinet, previousFrom, previousTo, limit, (page-1)*limit)
		if err != nil {
			return nil, apperror.New(errManagerAdsUnavailable, "comparison statistics are unavailable")
		}
		byID := make(map[uuid.UUID]domain.ManagerAdsCampaign, len(previous))
		for _, row := range previous {
			byID[row.ID] = row
		}
		for i := range report.Items {
			if row, ok := byID[report.Items[i].ID]; ok {
				report.Items[i].PreviousMetrics = row.Metrics
				days := row.ObservedDays
				report.Items[i].PreviousObservedDays = &days
			}
		}
		report.Comparison = &domain.ManagerAdsPeriod{DateFrom: previousFrom.Format(time.DateOnly), DateTo: previousTo.Format(time.DateOnly), Timezone: "Europe/Moscow"}
	}
	return report, nil
}

// resolveCabinet is shared by stored reports and native context reads. Exact
// CRM integration and workspace mapping must be checked before credentials.
func (s *ManagerAdsService) resolveCabinet(ctx context.Context, identity domain.ManagerAccess, workspaceID uuid.UUID, token, integrationID string) (*domain.ManagerIntegration, *domain.ManagerAdsCabinet, error) {
	if s == nil || s.repo == nil || s.crm == nil {
		return nil, nil, apperror.New(errManagerAdsUnavailable, "manager ads source is not configured")
	}
	if !identity.Allows("marketing.view") || !identity.Allows("integrations.view") || workspaceID == uuid.Nil || identity.ExternalWorkspaceID == "" || identity.ExternalUserID == "" {
		return nil, nil, apperror.New(apperror.ErrForbidden, "manager ads access denied")
	}
	integration, err := s.crm.VerifyManagerIntegration(ctx, token, identity.ExternalWorkspaceID, integrationID)
	if err != nil {
		if errors.Is(err, sellico.ErrUnauthorized) || errors.Is(err, sellico.ErrManagerScopeDenied) {
			return nil, nil, err
		}
		return nil, nil, apperror.New(errManagerAdsUnavailable, "manager integration could not be verified")
	}
	if integration == nil || integration.ID != integrationID || integration.WorkspaceID != identity.ExternalWorkspaceID {
		return nil, nil, apperror.New(apperror.ErrForbidden, "manager integration scope mismatch")
	}
	cabinet, err := s.repo.ManagerAdsCabinetByIntegration(ctx, workspaceID, integrationID)
	if err != nil {
		return nil, nil, apperror.New(errManagerAdsUnavailable, "manager cabinet mapping could not be verified")
	}
	if cabinet == nil {
		return nil, nil, apperror.New(apperror.ErrNotFound, "cabinet mapping requires synchronization")
	}
	if cabinet.ID == uuid.Nil || cabinet.WorkspaceID != workspaceID || cabinet.IntegrationID != integrationID || cabinet.Marketplace != integration.Marketplace {
		return nil, nil, apperror.New(apperror.ErrForbidden, "manager cabinet scope mismatch")
	}
	return integration, cabinet, nil
}
