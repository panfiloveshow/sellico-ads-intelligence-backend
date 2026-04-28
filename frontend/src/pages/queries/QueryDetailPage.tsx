import { useMemo } from "react";
import { useParams, Link as RouterLink } from "react-router-dom";
import { Stack, Typography, Card, CardContent, Link } from "@mui/material";

import { DetailLayout } from "@/components/detail/DetailLayout";
import { MetricsGrid } from "@/components/detail/MetricsGrid";
import { RelatedEntities } from "@/components/detail/RelatedEntities";
import { useAdsQuery } from "@/api/queries/ads";
import { useWorkspaces } from "@/api/queries/workspaces";
import { formatMoney } from "@/lib/format/numbers";

function defaultDateRange() {
  const today = new Date();
  const past = new Date(today);
  past.setDate(past.getDate() - 27);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  return { dateFrom: fmt(past), dateTo: fmt(today) };
}

const signalChipColor: Record<string, "default" | "success" | "warning" | "error" | "info"> = {
  winning: "success",
  promising: "info",
  watch: "warning",
  waste: "error",
  blocked: "default",
};

/**
 * Query (search-phrase) detail page — `/queries/:id`.
 *
 * Differences from product/campaign:
 *  - The "current bid" is the headline number to surface (not just metrics
 *    in MetricsGrid) — it's what the seller would change to act on a
 *    recommendation. Big card at the top makes that obvious.
 *  - The `signal_category` (winning / promising / watch / waste) drives
 *    chip colour and primary CTA copy on the recommendation engine side.
 *  - There's a back-link to the parent campaign as part of the subtitle.
 */
export function QueryDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const { dateFrom, dateTo } = useMemo(defaultDateRange, []);
  const { data: workspaces } = useWorkspaces();
  const workspaceId = workspaces?.[0]?.id ?? "";

  const { data, isLoading, error } = useAdsQuery({ workspaceId, id, dateFrom, dateTo });

  const chips =
    data && [
      { label: data.signal_category, color: signalChipColor[data.signal_category] ?? "default" },
      { label: data.health_status },
      { label: data.source },
    ];

  return (
    <DetailLayout
      title={data?.keyword ?? "Поисковая фраза"}
      subtitle={
        data ? (
          <>
            кампания{" "}
            <Link component={RouterLink} to={`/campaigns/${data.campaign_id}`}>
              {data.campaign_name}
            </Link>
          </>
        ) : undefined
      }
      chips={chips}
      loading={isLoading}
      error={error as Error | null}
    >
      <Card variant="outlined">
        <CardContent>
          <Typography variant="caption" color="text.secondary" sx={{ textTransform: "uppercase", letterSpacing: 0.5 }}>
            Текущая ставка
          </Typography>
          <Typography variant="h2" sx={{ fontSize: "2rem" }}>
            {formatMoney(data?.current_bid)}
          </Typography>
          {data?.cluster_size != null && (
            <Typography variant="body2" color="text.secondary">
              Кластер: {data.cluster_size} запросов
            </Typography>
          )}
        </CardContent>
      </Card>

      <MetricsGrid performance={data?.performance} compare={data?.period_compare} loading={isLoading} />

      <Stack direction={{ xs: "column", lg: "row" }} spacing={2}>
        <RelatedEntities
          title="Релевантные товары"
          hrefPrefix="/products"
          items={data?.related_products}
          emptyHint="Товары не связаны с этим запросом."
          loading={isLoading}
        />
      </Stack>

      {data?.health_reason && (
        <Typography variant="body2" color="text.secondary">
          {data.health_reason}
        </Typography>
      )}
    </DetailLayout>
  );
}
