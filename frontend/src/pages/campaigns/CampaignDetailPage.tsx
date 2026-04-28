import { useMemo } from "react";
import { useParams } from "react-router-dom";
import { Stack, Typography } from "@mui/material";

import { DetailLayout } from "@/components/detail/DetailLayout";
import { MetricsGrid } from "@/components/detail/MetricsGrid";
import { RelatedEntities } from "@/components/detail/RelatedEntities";
import { useAdsCampaign } from "@/api/queries/ads";
import { useWorkspaces } from "@/api/queries/workspaces";

function defaultDateRange() {
  const today = new Date();
  const past = new Date(today);
  past.setDate(past.getDate() - 27);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  return { dateFrom: fmt(past), dateTo: fmt(today) };
}

const statusChipColor: Record<string, "default" | "success" | "warning" | "error"> = {
  active: "success",
  paused: "warning",
  stopped: "default",
  archived: "default",
};
const healthChipColor: Record<string, "default" | "success" | "warning" | "error"> = {
  healthy: "success",
  warning: "warning",
  attention: "warning",
  critical: "error",
};

/**
 * Campaign detail page — `/campaigns/:id`.
 * Mirrors ProductDetailPage layout; the related-entity routing is symmetric:
 * a campaign's related products navigate to /products/:id.
 *
 * The `bid_type` and `payment_type` are surfaced as chips because they
 * change the expected metrics shape: a CPC campaign reports raw spend
 * directly; a CPM-with-views campaign needs spend = views * cpm / 1000.
 * The MetricsGrid hides this complexity — the backend pre-computes spend
 * either way — but the chips help the seller orient.
 */
export function CampaignDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const { dateFrom, dateTo } = useMemo(defaultDateRange, []);
  const { data: workspaces } = useWorkspaces();
  const workspaceId = workspaces?.[0]?.id ?? "";

  const { data, isLoading, error } = useAdsCampaign({ workspaceId, id, dateFrom, dateTo });

  const chips =
    data && [
      { label: data.status, color: statusChipColor[data.status] ?? "default" },
      { label: data.health_status, color: healthChipColor[data.health_status] ?? "default" },
      { label: data.payment_type.toUpperCase() },
    ];

  return (
    <DetailLayout
      title={data?.name ?? "Кампания"}
      subtitle={data?.cabinet_name}
      chips={chips}
      loading={isLoading}
      error={error as Error | null}
    >
      <MetricsGrid performance={data?.performance} compare={data?.period_compare} loading={isLoading} />

      <Stack direction={{ xs: "column", lg: "row" }} spacing={2}>
        <RelatedEntities
          title="Связанные товары"
          hrefPrefix="/products"
          items={data?.related_products}
          emptyHint="К кампании не привязаны товары."
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
            title="Прибыльные запросы"
            hrefPrefix="/queries"
            items={data?.winning_queries}
            loading={isLoading}
          />
          <RelatedEntities
            title="Сжигающие бюджет"
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
