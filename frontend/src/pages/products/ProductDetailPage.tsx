import { useMemo } from "react";
import { useParams } from "react-router-dom";
import { Stack, Typography } from "@mui/material";

import { DetailLayout } from "@/components/detail/DetailLayout";
import { MetricsGrid } from "@/components/detail/MetricsGrid";
import { RelatedEntities } from "@/components/detail/RelatedEntities";
import { useAdsProduct } from "@/api/queries/ads";
import { useWorkspaces } from "@/api/queries/workspaces";

function defaultDateRange() {
  const today = new Date();
  const past = new Date(today);
  past.setDate(past.getDate() - 27);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  return { dateFrom: fmt(past), dateTo: fmt(today) };
}

const healthChipColor: Record<string, "default" | "success" | "warning" | "error"> = {
  healthy: "success",
  warning: "warning",
  attention: "warning",
  critical: "error",
};

/**
 * Product detail page — `/products/:id`.
 *
 * Layout:
 *   - DetailLayout shell (back / title / status chip)
 *   - MetricsGrid (impressions / clicks / spend / orders / CTR / ROAS with deltas)
 *   - Two-column grid:
 *       left: related campaigns
 *       right: top + waste + winning queries
 *
 * Position history chart (Recharts) is intentionally deferred to a follow-up —
 * needs a dedicated /api/v1/products/{id}/positions endpoint that's already in
 * the router but not yet shipped to the typed schema. MetricsGrid + relations
 * answer 80% of the "is this product OK?" question without a chart.
 */
export function ProductDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const { dateFrom, dateTo } = useMemo(defaultDateRange, []);
  const { data: workspaces } = useWorkspaces();
  const workspaceId = workspaces?.[0]?.id ?? "";

  const { data, isLoading, error } = useAdsProduct({ workspaceId, id, dateFrom, dateTo });

  const chips =
    data && [
      { label: data.health_status, color: healthChipColor[data.health_status] ?? "default" },
      { label: `${data.campaigns_count} кампаний` },
      { label: `${data.queries_count} фраз` },
    ];

  return (
    <DetailLayout
      title={data?.title ?? "Товар"}
      subtitle={data?.cabinet_name}
      chips={chips}
      loading={isLoading}
      error={error as Error | null}
    >
      <MetricsGrid performance={data?.performance} compare={data?.period_compare} loading={isLoading} />

      <Stack direction={{ xs: "column", lg: "row" }} spacing={2}>
        <RelatedEntities
          title="Связанные кампании"
          hrefPrefix="/campaigns"
          items={data?.related_campaigns}
          emptyHint="Кампании ещё не подключены к товару."
          loading={isLoading}
        />
        <Stack spacing={2} sx={{ flex: 1 }}>
          <RelatedEntities
            title="Топ запросов"
            hrefPrefix="/queries"
            items={data?.top_queries}
            loading={isLoading}
          />
          <RelatedEntities
            title="Бесполезный трафик"
            hrefPrefix="/queries"
            items={data?.waste_queries}
            loading={isLoading}
          />
        </Stack>
      </Stack>

      {data?.health_reason && (
        <Typography variant="body2" color="text.secondary">
          {data.health_reason}
        </Typography>
      )}
    </DetailLayout>
  );
}
