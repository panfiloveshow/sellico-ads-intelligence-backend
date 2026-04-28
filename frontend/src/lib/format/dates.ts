/**
 * Format a Date as `YYYY-MM-DD` in the **local** timezone — NOT UTC.
 *
 * `Date.toISOString().slice(0, 10)` works for English-speakers near GMT but
 * silently shifts the date by one day for users east of UTC (Moscow, Asia)
 * who open the dashboard in the early morning. Their `dateTo` would show
 * yesterday's UTC day and they'd miss the last few hours of activity.
 *
 * Example:
 *   user in MSK at 2026-04-28 02:30 local → toISOString → 2026-04-27
 *   formatLocalDate                       → 2026-04-28 ✓
 */
export function formatLocalDate(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/**
 * Trailing N-day range based on the user's local "today".
 * Returns YYYY-MM-DD strings ready to pass into `date_from` / `date_to`
 * query params (the backend treats them as inclusive day boundaries).
 */
export function trailingDays(days: number): { dateFrom: string; dateTo: string } {
  const today = new Date();
  const past = new Date(today);
  past.setDate(past.getDate() - (days - 1));
  return { dateFrom: formatLocalDate(past), dateTo: formatLocalDate(today) };
}
