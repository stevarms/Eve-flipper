import { describe, expect, it } from "vitest";
import { formatIsk, formatIskSigned } from "./format";

describe("formatIsk", () => {
  it("abbreviates each tier with a space by default", () => {
    expect(formatIsk(1_234_567_890, "en")).toBe("1.23 B");
    expect(formatIsk(1_234_567, "en")).toBe("1.23 M");
    expect(formatIsk(1_234, "en")).toBe("1.2 K");
    expect(formatIsk(123, "en")).toBe("123");
  });

  /* The bug that shipped in four local copies: they tested the SIGNED value
     against the tier thresholds, so negatives skipped every branch and
     leaked raw digits. */
  it("handles negatives at every tier instead of leaking raw digits", () => {
    expect(formatIsk(-1_234_567_890, "en")).toBe("-1.23 B");
    expect(formatIsk(-1_234_567, "en")).toBe("-1.23 M");
    expect(formatIsk(-1_234, "en")).toBe("-1.2 K");
    expect(formatIsk(-123, "en")).toBe("-123");
  });

  it("stops promoting at maxTier rather than falling back to raw digits", () => {
    // Default ceiling is B, so a trillion stays in billions.
    expect(formatIsk(2e12, "en")).toBe("2,000 B");
    // Opting into T changes that — this was the TradeJournal/CharacterPopup
    // disagreement, where the same figure read "2000B" and "2T".
    expect(formatIsk(2e12, "en", { maxTier: "T" })).toBe("2 T");
    expect(formatIsk(3e15, "en", { maxTier: "Q" })).toBe("3 Q");
  });

  it("honours the space and decimals options", () => {
    expect(formatIsk(1_234_567_890, "en", { space: false })).toBe("1.23B");
    expect(formatIsk(1_234_567_890, "en", { decimals: { b: 1 } })).toBe("1.2 B");
    expect(formatIsk(1_234_567_890, "en", { decimals: { b: 0 } })).toBe("1 B");
  });

  it("is locale aware", () => {
    // ru-RU uses a comma decimal separator and a narrow-nbsp group separator.
    expect(formatIsk(1_234_567_890, "ru")).toContain(",");
  });

  it("survives null, undefined and non-finite input", () => {
    expect(formatIsk(NaN, "en")).toBe("0");
    expect(formatIsk(Infinity, "en")).toBe("0");
    expect(formatIsk(null as unknown as number, "en")).toBe("0");
    expect(formatIsk(undefined as unknown as number, "en")).toBe("0");
  });

  it("treats zero as unsigned", () => {
    expect(formatIsk(0, "en")).toBe("0");
    expect(formatIskSigned(0, "en")).toBe("+0");
  });
});

describe("formatIskSigned", () => {
  it("prefixes non-negative values with +", () => {
    expect(formatIskSigned(1_500_000, "en")).toBe("+1.5 M");
    expect(formatIskSigned(-1_500_000, "en")).toBe("-1.5 M");
  });
});
