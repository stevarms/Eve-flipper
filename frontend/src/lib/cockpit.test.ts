import { describe, expect, it } from "vitest";
import {
  MAIN_TAB_IDS,
  WORKSPACE_META,
  sanitizeCockpitPreferences,
  visibleTabsForWorkspace,
} from "./cockpit";
import type { MainTabId } from "./cockpit";

/** A stored preference from before the IA rewrite: the seven original tabs,
 *  none of the ten that were promoted out of the character modal. */
const LEGACY_ORDER: MainTabId[] = [
  "station",
  "radius",
  "region",
  "route",
  "contracts",
  "industry",
  "demand",
];

describe("sanitizeCockpitPreferences tab order", () => {
  it("keeps every tab exactly once", () => {
    const { mainTabOrder } = sanitizeCockpitPreferences({ mainTabOrder: LEGACY_ORDER });
    expect([...mainTabOrder].sort()).toEqual([...MAIN_TAB_IDS].sort());
  });

  it("puts a newly-added tab at its designed slot, not at the end", () => {
    const prefs = sanitizeCockpitPreferences({ mainTabOrder: LEGACY_ORDER });
    // Assets is the sharp case: price_audit is the only tab a legacy
    // preference knows about, and Positions is designed to lead the workspace.
    expect(visibleTabsForWorkspace(prefs, "assets")).toEqual([
      "positions",
      "stockpiles",
      "price_audit",
    ]);
    expect(visibleTabsForWorkspace(prefs, "journal")).toEqual([
      "trade_journal",
      "pnl",
      "transactions",
      "wallet",
      "risk",
    ]);
  });

  it("preserves the user's relative order among tabs they already had", () => {
    const prefs = sanitizeCockpitPreferences({ mainTabOrder: LEGACY_ORDER });
    const trade = visibleTabsForWorkspace(prefs, "trade");
    // station before radius before region, as stored — even though the
    // canonical literal lists radius first.
    expect(trade.indexOf("station")).toBeLessThan(trade.indexOf("radius"));
    expect(trade.indexOf("radius")).toBeLessThan(trade.indexOf("region"));
    // The promoted tools still land where the IA puts them: after contracts.
    expect(trade.indexOf("contracts")).toBeLessThan(trade.indexOf("orders"));
  });

  it("falls back to the canonical order when nothing is stored", () => {
    const prefs = sanitizeCockpitPreferences({});
    for (const ws of Object.keys(WORKSPACE_META) as (keyof typeof WORKSPACE_META)[]) {
      expect(visibleTabsForWorkspace(prefs, ws)).toEqual([...WORKSPACE_META[ws].tabs]);
    }
  });
});
