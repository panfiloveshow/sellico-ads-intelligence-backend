import { useMemo } from "react";
import { Alert, Box, CircularProgress, Stack, Typography } from "@mui/material";

import { KpiCard } from "@/components/dashboard/KpiCard";
import { AttentionList } from "@/components/dashboard/AttentionList";
import { useAdsOverview } from "@/api/queries/ads";
import { useWorkspaces } from "@/api/queries/workspaces";
import { formatCompact, formatMoney, formatNumber } from "@/lib/format/numbers";

// MVP date range: trailing 28 days. Settings page (Sprint 7) will let users
// pick a custom range and pin it to URL params.
function defaultDateRange() {
  const today = new Date();
  const past = new Date(today);
  past.setDate(past.getDate() - 27);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  return { dateFrom: fmt(past), dateTo: fmt(today) };
}

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

      {/* TODO Sprint 6 follow-up: Top products / campaigns / queries (DataGrid). */}
    </Stack>
  );
}
