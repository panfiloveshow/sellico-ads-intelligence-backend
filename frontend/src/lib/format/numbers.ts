// Backend returns money in kopecks (int64); display in roubles with 2 decimals.
// Counts (impressions, clicks, etc.) come as int64 — cast to number is safe up
// to 2^53; for an ads dashboard the practical ceiling is well below that.

const RUB = new Intl.NumberFormat("ru-RU", {
  style: "currency",
  currency: "RUB",
  maximumFractionDigits: 0,
});

const COMPACT = new Intl.NumberFormat("ru-RU", {
  notation: "compact",
  maximumFractionDigits: 1,
});

const NUMBER = new Intl.NumberFormat("ru-RU", {
  maximumFractionDigits: 0,
});

const PERCENT = new Intl.NumberFormat("ru-RU", {
  style: "percent",
  maximumFractionDigits: 2,
});

/** Money in kopecks → human roubles like "12 345 ₽". */
export const formatMoney = (kopecks: number | null | undefined): string => {
  if (kopecks == null) return "—";
  return RUB.format(kopecks / 100);
};

/** Big counts → "1,2K", "3,4M". */
export const formatCompact = (n: number | null | undefined): string => {
  if (n == null) return "—";
  return COMPACT.format(n);
};

/** Plain integer with locale-aware thousands separator. */
export const formatNumber = (n: number | null | undefined): string => {
  if (n == null) return "—";
  return NUMBER.format(n);
};

/** Backend gives ratios as 0-1 (CTR=0.034 = 3.4%). */
export const formatPercent = (ratio: number | null | undefined): string => {
  if (ratio == null) return "—";
  return PERCENT.format(ratio);
};

/** Compute a delta string for period compare ("+12,3%" or "-4,5%"). */
export const formatDeltaPercent = (current: number, previous: number): string => {
  if (previous === 0) {
    if (current === 0) return "0%";
    return current > 0 ? "+∞" : "−∞";
  }
  const delta = (current - previous) / previous;
  const formatted = PERCENT.format(Math.abs(delta));
  return delta >= 0 ? `+${formatted}` : `−${formatted}`;
};
