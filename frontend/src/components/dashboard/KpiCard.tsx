import { Card, CardContent, Stack, Typography, Skeleton, Box } from "@mui/material";
import TrendingUpIcon from "@mui/icons-material/TrendingUp";
import TrendingDownIcon from "@mui/icons-material/TrendingDown";
import TrendingFlatIcon from "@mui/icons-material/TrendingFlat";

import { formatDeltaPercent } from "@/lib/format/numbers";

interface KpiCardProps {
  /** Short label shown above the value (e.g. "Расход"). */
  label: string;
  /** Pre-formatted current value. We don't format here so the caller can pick the right unit (₽, %, count). */
  value: string;
  /** Numeric current — for delta math. Pass null to disable the trend pill. */
  current?: number | null;
  /** Numeric previous-period value for the same metric. */
  previous?: number | null;
  /** When `inverted` is true, "down is good" (e.g. CPC, CPO, DRR). Affects icon colour. */
  inverted?: boolean;
  /** Loading state — render skeletons in place of value + delta. */
  loading?: boolean;
}

/**
 * Single dashboard KPI tile. Four of these live across the top of the
 * Command Center page (impressions / clicks / spend / orders by default).
 *
 * Visual decisions:
 *  - Value uses h3 weight so it dominates the card hierarchy
 *  - Trend pill uses semantic colour (green=good, red=bad), inverted via the
 *    `inverted` prop for "lower is better" metrics like CPC/CPO/DRR
 *  - Skeletons match real layout to avoid card-size jitter on first paint
 */
export function KpiCard({ label, value, current, previous, inverted, loading }: KpiCardProps) {
  return (
    <Card sx={{ minWidth: 200, flex: 1 }}>
      <CardContent>
        <Typography variant="body2" color="text.secondary" gutterBottom>
          {label}
        </Typography>
        {loading ? (
          <>
            <Skeleton variant="text" width="60%" height={40} />
            <Skeleton variant="text" width="40%" height={20} />
          </>
        ) : (
          <>
            <Typography variant="h3" component="div" sx={{ mb: 1 }}>
              {value}
            </Typography>
            <DeltaPill current={current} previous={previous} inverted={inverted} />
          </>
        )}
      </CardContent>
    </Card>
  );
}

function DeltaPill({
  current,
  previous,
  inverted,
}: {
  current?: number | null;
  previous?: number | null;
  inverted?: boolean;
}) {
  if (current == null || previous == null) {
    return (
      <Typography variant="body2" color="text.disabled">
        нет сравнения
      </Typography>
    );
  }

  const delta = current - previous;
  const isFlat = Math.abs(delta) < 0.01;
  const isUp = delta > 0;
  // Semantic: for inverted metrics (cost-style), down is good.
  const isGood = inverted ? !isUp : isUp;

  const color = isFlat ? "text.disabled" : isGood ? "success.main" : "error.main";
  const Icon = isFlat ? TrendingFlatIcon : isUp ? TrendingUpIcon : TrendingDownIcon;

  return (
    <Stack direction="row" alignItems="center" spacing={0.5}>
      <Box sx={{ color, display: "flex" }}>
        <Icon fontSize="small" />
      </Box>
      <Typography variant="body2" sx={{ color }}>
        {formatDeltaPercent(current, previous)}
      </Typography>
      <Typography variant="body2" color="text.disabled">
        к прошлому периоду
      </Typography>
    </Stack>
  );
}
