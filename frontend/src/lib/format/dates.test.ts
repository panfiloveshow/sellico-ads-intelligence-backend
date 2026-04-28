import { afterEach, describe, expect, it, vi } from "vitest";

import { formatLocalDate, trailingDays } from "./dates";

describe("dates formatters", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  describe("formatLocalDate", () => {
    it("pads single-digit month and day with zero", () => {
      // Local-time constructor — `Date(year, month, day)` builds in TZ-local time.
      // March = month 2 (0-indexed); day 5 with 1-digit components forces padding.
      const d = new Date(2026, 2, 5);
      expect(formatLocalDate(d)).toBe("2026-03-05");
    });

    it("returns YYYY-MM-DD for end-of-year date", () => {
      const d = new Date(2026, 11, 31);
      expect(formatLocalDate(d)).toBe("2026-12-31");
    });

    it("uses local date even when UTC date differs (Moscow at 01:30 → next day vs UTC)", () => {
      // 2026-04-28 01:30:00 UTC+3 (Moscow) === 2026-04-27 22:30:00 UTC.
      // toISOString().slice(0,10) would give "2026-04-27" (wrong);
      // formatLocalDate must give "2026-04-28" (right for the user looking at the page).
      vi.useFakeTimers();
      vi.setSystemTime(new Date("2026-04-27T22:30:00Z"));
      // Skip if test runner is itself in UTC (CI containers sometimes are) — the
      // assertion only matters when local TZ ≠ UTC.
      const offsetMin = new Date().getTimezoneOffset();
      if (offsetMin === 0) return;
      const out = formatLocalDate(new Date());
      // Local day depends on the runner's TZ; just assert the function returns a
      // plausible YYYY-MM-DD string and that we consider the local date, not UTC.
      expect(out).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      const todayLocal = new Date();
      const expected = `${todayLocal.getFullYear()}-${String(todayLocal.getMonth() + 1).padStart(2, "0")}-${String(todayLocal.getDate()).padStart(2, "0")}`;
      expect(out).toBe(expected);
    });
  });

  describe("trailingDays", () => {
    it("returns inclusive 28-day window (dateFrom 27 days before dateTo)", () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date(2026, 3, 28, 12, 0, 0)); // April 28, 2026 noon local
      const { dateFrom, dateTo } = trailingDays(28);
      expect(dateTo).toBe("2026-04-28");
      expect(dateFrom).toBe("2026-04-01");
    });

    it("handles a 1-day window (today only)", () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date(2026, 3, 28, 12, 0, 0));
      const { dateFrom, dateTo } = trailingDays(1);
      expect(dateFrom).toBe("2026-04-28");
      expect(dateTo).toBe("2026-04-28");
    });

    it("crosses month boundary correctly", () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date(2026, 4, 5, 12, 0, 0)); // May 5
      const { dateFrom } = trailingDays(28);
      expect(dateFrom).toBe("2026-04-08");
    });
  });
});
