import { Card, CardContent, Stack, Typography, Skeleton, Box } from "@mui/material";
import TrendingUpIcon from "@mui/icons-material/TrendingUp";
import TrendingDownIcon from "@mui/icons-material/TrendingDown";
import TrendingFlatIcon from "@mui/icons-material/TrendingFlat";

import type { AdsMetricsSummary, AdsPeriodCompare } from "@/api/queries/ads";
import { formatCompact, formatMoney, formatNumber, formatPercent, formatDeltaPercent } from "@/lib/format/numbers";

interface MetricsGridProps {
  performance?: AdsMetricsSummary;
  compare?: AdsPeriodCompare;
  loading?: boolean;
}

interface MetricCellProps {
  label: string;
  value: string;
  current?: number;
  previous?: number;
  /** When true, lower is better (CPC, CPO, DRR); inverts pill colour. */
  inverted?: boolean;
  loading?: boolean;
}

/**
 * Compact 6-cell metrics row used inside DetailLayout.
 *
 * Layout: impressions / clicks / spend / orders / CPC / ROAS — these were
 * picked as the smallest set that lets a seller answer "is this good?"
 * without scrolling. CTR and CPO are intentionally NOT here because they're
 * derivable; we surface them in the secondary table on each detail page.
 *
 * On <md screens cells stack 2-per-row (still readable on phones).
 */
export function MetricsGrid({ performance, compare, loading }: MetricsGridProps) {
  const current = performance ?? compare?.current;
  const previous = compare?.previous;

  return (
    <Stack
      direction="row"
      spacing={1.5}
      flexWrap="wrap"
      useFlexGap
      sx={{ "& > *": { flex: { xs: "1 1 calc(50% - 12px)", md: "1 1 0" }, minWidth: 140 } }}
    >
      <MetricCell
        label="Показы"
        value={formatCompact(current?.impressions)}
        current={current?.impressions}
        previous={previous?.impressions}
        loading={loading}
      />
      <MetricCell
        label="Клики"
        value={formatNumber(current?.clicks)}
        current={current?.clicks}
        previous={previous?.clicks}
        loading={loading}
      />
      <MetricCell
        label="Расход"
        value={formatMoney(current?.spend)}
        current={current?.spend}
        previous={previous?.spend}
        inverted
        loading={loading}
      />
      <MetricCell
        label="Заказы"
        value={formatNumber(current?.orders)}
        current={current?.orders}
        previous={previous?.orders}
        loading={loading}
      />
      <MetricCell
        label="CTR"
        value={formatPercent(current?.ctr)}
        current={current?.ctr}
        previous={previous?.ctr}
        loading={loading}
      />
      <MetricCell
        label="ROAS"
        value={current?.roas != null ? current.roas.toFixed(2) : "—"}
        current={current?.roas}
        previous={previous?.roas}
        loading={loading}
      />
    </Stack>
  );
}

function MetricCell({ label, value, current, previous, inverted, loading }: MetricCellProps) {
  return (
    <Card variant="outlined" sx={{ minWidth: 140 }}>
      <CardContent sx={{ p: 1.5, "&:last-child": { pb: 1.5 } }}>
        <Typography variant="caption" color="text.secondary" sx={{ textTransform: "uppercase", letterSpacing: 0.5 }}>
          {label}
        </Typography>
        {loading ? (
          <Skeleton variant="text" height={28} />
        ) : (
          <Typography variant="h3" sx={{ fontSize: "1.15rem", lineHeight: 1.4 }}>
            {value}
          </Typography>
        )}
        {!loading && current != null && previous != null && (
          <DeltaRow current={current} previous={previous} inverted={inverted} />
        )}
      </CardContent>
    </Card>
  );
}

function DeltaRow({ current, previous, inverted }: { current: number; previous: number; inverted?: boolean }) {
  const delta = current - previous;
  const isFlat = Math.abs(delta) < 0.01;
  const isUp = delta > 0;
  const isGood = inverted ? !isUp : isUp;
  const color = isFlat ? "text.disabled" : isGood ? "success.main" : "error.main";
  const Icon = isFlat ? TrendingFlatIcon : isUp ? TrendingUpIcon : TrendingDownIcon;
  return (
    <Stack direction="row" alignItems="center" spacing={0.25}>
      <Box sx={{ color, display: "flex" }}>
        <Icon sx={{ fontSize: 14 }} />
      </Box>
      <Typography variant="caption" sx={{ color }}>
        {formatDeltaPercent(current, previous)}
      </Typography>
    </Stack>
  );
}
