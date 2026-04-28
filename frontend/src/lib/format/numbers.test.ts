import { describe, it, expect } from "vitest";

import { formatMoney, formatCompact, formatNumber, formatPercent, formatDeltaPercent } from "./numbers";

describe("numbers formatters", () => {
  describe("formatMoney", () => {
    it("converts kopecks → roubles with currency suffix", () => {
      // ₽-символ может быть NBSP-разделён; используем contains-проверку чтобы
      // не привязываться к конкретному пробелу-разделителю Intl
      const out = formatMoney(123_45);
      expect(out).toContain("123");
      expect(out).toMatch(/[₽р]/);
    });

    it("returns dash for null/undefined", () => {
      expect(formatMoney(null)).toBe("—");
      expect(formatMoney(undefined)).toBe("—");
    });

    it("handles zero", () => {
      expect(formatMoney(0)).toContain("0");
    });

    it("handles big sums (millions of kopecks)", () => {
      const out = formatMoney(50_000_000); // 500_000 ₽
      // Локаль ru-RU использует NBSP/тонкий пробел между группами
      expect(out).toMatch(/500.000/);
    });
  });

  describe("formatCompact", () => {
    it("compacts large numbers", () => {
      expect(formatCompact(1500)).toMatch(/1[.,]5/);
      expect(formatCompact(1_500_000)).toMatch(/1[.,]5/);
    });

    it("dash for null", () => {
      expect(formatCompact(null)).toBe("—");
    });
  });

  describe("formatNumber", () => {
    it("plain integer with thousand separator", () => {
      const out = formatNumber(12345);
      expect(out).toMatch(/12.345/);
    });
  });

  describe("formatPercent", () => {
    it("ratio 0..1 → '%'", () => {
      const out = formatPercent(0.0345);
      expect(out).toMatch(/3[.,]45/);
      expect(out).toContain("%");
    });
  });

  describe("formatDeltaPercent", () => {
    it("positive delta gets +", () => {
      expect(formatDeltaPercent(110, 100)).toMatch(/^\+/);
    });

    it("negative delta gets minus sign (could be − or -)", () => {
      const out = formatDeltaPercent(90, 100);
      expect(out).toMatch(/^[-−]/);
    });

    it("flat 0", () => {
      expect(formatDeltaPercent(100, 100)).toMatch(/0/);
    });

    it("previous=0 with current>0 → +∞", () => {
      expect(formatDeltaPercent(50, 0)).toBe("+∞");
    });

    it("previous=0 with current<0 → −∞", () => {
      expect(formatDeltaPercent(-50, 0)).toBe("−∞");
    });

    it("both zero → 0%", () => {
      expect(formatDeltaPercent(0, 0)).toBe("0%");
    });
  });
});
