import { useMemo } from "react";
import { Link as RouterLink } from "react-router-dom";
import { Alert, Box, Button, CircularProgress, Stack, Typography } from "@mui/material";

import { KpiCard } from "@/components/dashboard/KpiCard";
import { AttentionList } from "@/components/dashboard/AttentionList";
import { TopProductsTable, TopCampaignsTable, TopQueriesTable } from "@/components/dashboard/TopEntitiesTable";
import { useAdsOverview } from "@/api/queries/ads";
import { useWorkspaces } from "@/api/queries/workspaces";
import { formatCompact, formatMoney, formatNumber } from "@/lib/format/numbers";
import { trailingDays } from "@/lib/format/dates";

// MVP date range: trailing 28 days. Settings page (Sprint 7) will let users
// pick a custom range and pin it to URL params.
const defaultDateRange = () => trailingDays(28);

/**
 * Sprint 6 — Command Center.
 *
 * Workspace selection: defaults to the first workspace the user is a member
 * of (auto-fetch via /api/v1/workspaces). When a multi-workspace switcher
 * lands in AppLayout (Sprint 6 follow-up), this page will read the active
 * workspace from a context instead.
 */
export function CommandCenterPage() {
  const { dateFrom, dateTo } = useMemo(defaultDateRange, []);
  const { data: workspaces, isLoading: workspacesLoading } = useWorkspaces();
  const workspaceId = workspaces?.[0]?.id;

  const { data, isLoading: overviewLoading, error } = useAdsOverview({
    workspaceId: workspaceId ?? "",
    dateFrom,
    dateTo,
    enabled: Boolean(workspaceId),
  });

  if (workspacesLoading) {
    return (
      <Stack alignItems="center" sx={{ py: 8 }}>
        <CircularProgress />
      </Stack>
    );
  }

  if (!workspaceId) {
    return (
      <Alert severity="info">
        У вас ещё нет workspace. Создайте его на странице настроек.
      </Alert>
    );
  }

  const isLoading = overviewLoading;

  if (error) {
    return (
      <Alert severity="error">
        Не удалось загрузить дашборд: {(error as Error).message}
      </Alert>
    );
  }

  // Empty-cabinets onboarding: workspace exists but no WB cabinets yet —
  // a freshly registered user lands here and sees only zeros + dashes,
  // which is confusing. Direct them to /settings to wire up integrations.
  const cabinetCount = data?.cabinets?.length ?? 0;
  if (!isLoading && cabinetCount === 0) {
    return (
      <Stack spacing={3}>
        <Box>
          <Typography variant="h1">Командный центр</Typography>
        </Box>
        <Alert
          severity="info"
          action={
            <Button component={RouterLink} to="/settings" color="inherit" size="small">
              К настройкам
            </Button>
          }
        >
          <Typography variant="body1" sx={{ fontWeight: 500, mb: 0.5 }}>
            Подключите первый WB-кабинет
          </Typography>
          <Typography variant="body2">
            Данные появятся в течение часа после первого sync. Если кабинет идёт
            из Sellico — discovery подцепит его автоматически.
          </Typography>
        </Alert>
      </Stack>
    );
  }

  const compare = data?.performance_compare;
  const current = compare?.current;
  const previous = compare?.previous;

  return (
    <Stack spacing={3}>
      <Box>
        <Typography variant="h1">Командный центр</Typography>
        <Typography variant="body2" color="text.secondary">
          Период: {dateFrom} — {dateTo}
        </Typography>
      </Box>

      <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
        <KpiCard
          label="Показы"
          value={formatCompact(current?.impressions ?? null)}
          current={current?.impressions}
          previous={previous?.impressions}
          loading={isLoading}
        />
        <KpiCard
          label="Клики"
          value={formatNumber(current?.clicks ?? null)}
          current={current?.clicks}
          previous={previous?.clicks}
          loading={isLoading}
        />
        <KpiCard
          label="Расход"
          value={formatMoney(current?.spend ?? null)}
          current={current?.spend}
          previous={previous?.spend}
          inverted /* меньше расход — лучше */
          loading={isLoading}
        />
        <KpiCard
          label="Заказы"
          value={formatNumber(current?.orders ?? null)}
          current={current?.orders}
          previous={previous?.orders}
          loading={isLoading}
        />
      </Stack>

      <Box>
        <Typography variant="h2" sx={{ fontSize: "1.25rem", mb: 1.5 }}>
          Требует внимания
          {data?.attention?.length ? ` (${data.attention.length})` : null}
        </Typography>
        <AttentionList items={data?.attention} loading={isLoading} />
      </Box>

      <Stack direction={{ xs: "column", lg: "row" }} spacing={2}>
        <TopProductsTable
          title="Топ товаров"
          items={data?.top_products}
          loading={isLoading}
          emptyHint="Товары появятся после первого sync с WB."
        />
        <TopCampaignsTable
          title="Топ кампаний"
          items={data?.top_campaigns}
          loading={isLoading}
          emptyHint="Кампании появятся после первого sync."
        />
        <TopQueriesTable
          title="Топ фраз"
          items={data?.top_queries}
          loading={isLoading}
          emptyHint="Фразы появятся после индексации."
        />
      </Stack>
    </Stack>
  );
}
