import { describe, expect, it } from "vitest";
import { normalizeColumnPrefs } from "./tablePrefs";

describe("table preferences normalization", () => {
  const defaults = ["Item", "Profit", "Jumps"] as const;

  it("ignores malformed stored preferences", () => {
    const prefs = normalizeColumnPrefs("{bad json", [...defaults]);

    expect(prefs.order).toEqual(["Item", "Profit", "Jumps"]);
    expect([...prefs.hidden]).toEqual([]);
    expect(prefs.widths).toEqual({});
    expect([...prefs.pinned]).toEqual([]);
  });

  it("drops stale keys, restores missing columns, and clamps widths", () => {
    const prefs = normalizeColumnPrefs(
      JSON.stringify({
        order: ["Profit", "OldColumn"],
        hidden: ["OldColumn", "Profit"],
        widths: { Item: 12, Profit: 900, Jumps: 88, OldColumn: 100 },
        pinned: ["Jumps", "OldColumn"],
      }),
      [...defaults],
    );

    expect(prefs.order).toEqual(["Profit", "Item", "Jumps"]);
    expect([...prefs.hidden]).toEqual(["Profit"]);
    expect(prefs.widths).toEqual({ Item: 44, Profit: 520, Jumps: 88 });
    expect([...prefs.pinned]).toEqual(["Jumps"]);
  });

  /* The decide-tier default from the UI overhaul. It must apply ONLY when
     there is no saved preference — a user who has explicitly un-hidden
     everything must stay that way, which is why an empty saved `hidden`
     still counts as a real preference. */
  it("applies defaultHidden when nothing is stored", () => {
    const prefs = normalizeColumnPrefs(null, [...defaults], ["Jumps"]);

    expect([...prefs.hidden]).toEqual(["Jumps"]);
    expect(prefs.order).toEqual(["Item", "Profit", "Jumps"]);
  });

  it("lets a stored preference override defaultHidden, including an empty one", () => {
    const prefs = normalizeColumnPrefs(
      JSON.stringify({ order: ["Item", "Profit", "Jumps"], hidden: [] }),
      [...defaults],
      ["Jumps"],
    );

    expect([...prefs.hidden]).toEqual([]);
  });

  it("ignores defaultHidden entries that are not real columns", () => {
    const prefs = normalizeColumnPrefs(null, [...defaults], ["Jumps", "Nonsense"]);

    expect([...prefs.hidden]).toEqual(["Jumps"]);
  });

  it("keeps at least one column visible", () => {
    const prefs = normalizeColumnPrefs(
      JSON.stringify({
        order: ["Profit", "Item", "Jumps"],
        hidden: ["Profit", "Item", "Jumps"],
      }),
      [...defaults],
    );

    expect(prefs.hidden.has("Profit")).toBe(false);
    expect(prefs.hidden.has("Item")).toBe(true);
    expect(prefs.hidden.has("Jumps")).toBe(true);
  });
});
