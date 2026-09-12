import { describe, expect, it } from "vitest";
import { todayPasteDisplay, todayPasteText } from "./todayFormat";

/**
 * The two halves of one price.
 *
 * `todayPasteText` is what lands on the clipboard and goes into EVE's price
 * field, which takes digits and a decimal point and nothing else.
 * `todayPasteDisplay` is what the eye reads. Keeping them in agreement is the
 * whole point: a reader who sees one number and pastes another places a real
 * order at the wrong price.
 *
 * Expectations here are the grid's actual output, not tidy round numbers --
 * EVE snaps to four significant digits, so 1013.4 is 1013 and 999 is 999.0.
 */
describe("today paste price", () => {
  it("groups thousands for reading", () => {
    expect(todayPasteDisplay(1000)).toBe("1,000");
    expect(todayPasteDisplay(12_345)).toBe("12,345");
    expect(todayPasteDisplay(1_237_000)).toBe("1,237,000");
    expect(todayPasteDisplay(12_370_000)).toBe("12,370,000");
    expect(todayPasteDisplay(1_237_000_000)).toBe("1,237,000,000");
  });

  // Below 1000 the grid spends its four digits on decimals instead, so there
  // is nothing to group and the two strings are identical.
  it("leaves sub-thousand prices ungrouped", () => {
    expect(todayPasteDisplay(999)).toBe("999.0");
    expect(todayPasteDisplay(45.6)).toBe("45.60");
    expect(todayPasteDisplay(3.99)).toBe("3.990");
  });

  // Grouping and decimals never co-occur in real grid output, so this is a
  // guard rather than an observed case: if the grid ever changes, a separator
  // must not land on the fraction side, where EVE would read it as a different
  // number entirely.
  it("never puts a separator in the decimals", () => {
    for (const price of [3.99, 45.6, 999, 1013.4, 1_237_000]) {
      const fraction = todayPasteDisplay(price).split(".")[1];
      if (fraction) expect(fraction).not.toContain(",");
    }
  });

  it("never puts a separator on the clipboard string", () => {
    for (const price of [1_237_000, 12_370_000, 1_237_000_000, 1013.4, 3.99]) {
      expect(todayPasteText(price)).toMatch(/^[0-9]+(\.[0-9]+)?$/);
    }
  });

  // The real safety property: display is derived from the clipboard string
  // rather than reformatted from the number, so the two cannot round
  // differently and show a price the clipboard does not hold.
  it("displays exactly the digits it copies", () => {
    for (const price of [1_237_000, 12_370_000, 1013.4, 3.99, 45_600_000, 999]) {
      expect(todayPasteDisplay(price).replace(/,/g, "")).toBe(todayPasteText(price));
    }
  });

  it("returns empty for prices there is nothing to paste for", () => {
    for (const bad of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(todayPasteDisplay(bad)).toBe("");
      expect(todayPasteText(bad)).toBe("");
    }
  });
});
