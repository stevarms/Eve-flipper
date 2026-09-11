import { describe, it, expect } from "vitest";
import { formatGridPrice, nextBuyOverbid, nextSellUndercut, priceStep, snapToGrid } from "./pricing";

// These cases are lifted verbatim from internal/engine/pricing_test.go. The
// point of duplicating them is drift: the clipboard price and the Order Desk's
// suggested reprice have to describe the same book, and a silent divergence
// between the two implementations is exactly the bug this port replaced.

describe("nextSellUndercut", () => {
  const cases: [string, number, number][] = [
    ["1234M off-grid", 12_345_678, 12_340_000],
    ["1234M on-grid steps down one place", 12_340_000, 12_330_000],
    ["2.456B off-grid", 2_456_789_000, 2_456_000_000],
    ["2.456B on-grid", 2_456_000_000, 2_455_000_000],
    ["456k off-grid", 456_789, 456_700],
    ["456k on-grid", 456_700, 456_600],
    ["90 ISK small-price", 90, 89.99],
    ["12.34 ISK small-price", 12.34, 12.33],
    ["1.234T off-grid", 1_234_500_000_000, 1_234_000_000_000],
    ["zero", 0, 0],
    ["negative", -100, 0],
  ];
  it.each(cases)("%s", (_name, input, want) => {
    expect(nextSellUndercut(input)).toBeCloseTo(want, 6);
  });

  it("refuses non-finite input", () => {
    expect(nextSellUndercut(Infinity)).toBe(0);
    expect(nextSellUndercut(NaN)).toBe(0);
  });
});

describe("nextBuyOverbid", () => {
  const cases: [string, number, number][] = [
    ["1234M off-grid", 12_345_678, 12_350_000],
    ["1234M on-grid steps up one place", 12_340_000, 12_350_000],
    ["2.456B off-grid", 2_456_789_000, 2_457_000_000],
    ["2.456B on-grid", 2_456_000_000, 2_457_000_000],
    ["456k off-grid", 456_789, 456_800],
    ["456k on-grid", 456_700, 456_800],
    ["90 ISK small-price", 90, 90.01],
    ["12.34 ISK small-price", 12.34, 12.35],
    ["zero", 0, 0],
    ["negative", -100, 0],
  ];
  it.each(cases)("%s", (_name, input, want) => {
    expect(nextBuyOverbid(input)).toBeCloseTo(want, 6);
  });
});

// Catches drift bugs where snapping accidentally lands back on the input.
it("undercut is strictly below and overbid strictly above", () => {
  for (const x of [12.34, 89.99, 456_789, 1_234_567, 12_345_678, 987_654_321, 2_456_789_000]) {
    expect(nextSellUndercut(x)).toBeLessThan(x);
    expect(nextBuyOverbid(x)).toBeGreaterThan(x);
  }
});

it("snapToGrid scrubs float noise and passes through a zero place", () => {
  expect(snapToGrid(89.99 - 0.01, 0.01)).toBeCloseTo(89.98, 9);
  expect(snapToGrid(42, 0)).toBe(42);
});

describe("formatGridPrice", () => {
  it("drops decimals EVE would never accept at that magnitude", () => {
    expect(formatGridPrice(nextSellUndercut(12_345_678), priceStep(12_345_678))).toBe("12340000");
    expect(formatGridPrice(nextBuyOverbid(456_789), priceStep(456_789))).toBe("456800");
  });

  it("keeps two decimals in the small-price band", () => {
    expect(formatGridPrice(nextSellUndercut(90), priceStep(90))).toBe("89.99");
    expect(formatGridPrice(nextBuyOverbid(12.34), priceStep(12.34))).toBe("12.35");
  });

  it("does not round a sub-10-ISK undercut back above its basis", () => {
    // toFixed(2) would turn 5.499 into "5.50" — above the 5.5 we undercut.
    const basis = 5.5;
    const text = formatGridPrice(nextSellUndercut(basis), priceStep(basis));
    expect(Number(text)).toBeLessThan(basis);
  });
});
