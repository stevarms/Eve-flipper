import { describe, expect, it } from "vitest";
import {
  defaultDirForSortKey,
  normalizeOrdersPrefs,
  ORDERS_DEFAULT_PREFS,
  ORDERS_DEFAULT_REFRESH_MINUTES,
} from "./ordersPrefs";

describe("orders preferences normalization", () => {
  it("opens on item A-to-Z with the default refresh interval", () => {
    // The tab is used beside the in-game Orders window, which lists items
    // alphabetically; anything else makes the two lists impossible to walk
    // together.
    expect(ORDERS_DEFAULT_PREFS.sortKey).toBe("type");
    expect(ORDERS_DEFAULT_PREFS.sortDir).toBe("asc");
    expect(ORDERS_DEFAULT_PREFS.refreshMinutes).toBe(ORDERS_DEFAULT_REFRESH_MINUTES);
  });

  it("falls back to the defaults for anything unreadable", () => {
    for (const raw of [null, "", "{bad json", "[]", '"a string"', "null", "42"]) {
      expect(normalizeOrdersPrefs(raw)).toEqual(ORDERS_DEFAULT_PREFS);
    }
  });

  it("restores a full set of saved preferences", () => {
    const prefs = normalizeOrdersPrefs(
      JSON.stringify({
        sortKey: "notional",
        sortDir: "desc",
        actionFilter: "needs_action",
        collapsedSell: false,
        collapsedBuy: true,
        refreshMinutes: 10,
      }),
    );

    expect(prefs).toEqual({
      sortKey: "notional",
      sortDir: "desc",
      actionFilter: "needs_action",
      collapsedSell: false,
      collapsedBuy: true,
      refreshMinutes: 10,
    });
  });

  it("keeps the good fields when only some are bad", () => {
    // A blob written by an older build can carry a sort key that no longer
    // exists; that must not cost the user their other settings.
    const prefs = normalizeOrdersPrefs(
      JSON.stringify({
        sortKey: "someRemovedColumn",
        sortDir: "sideways",
        actionFilter: "needs_action",
        collapsedBuy: true,
      }),
    );

    expect(prefs.sortKey).toBe(ORDERS_DEFAULT_PREFS.sortKey);
    expect(prefs.sortDir).toBe(ORDERS_DEFAULT_PREFS.sortDir);
    expect(prefs.actionFilter).toBe("needs_action");
    expect(prefs.collapsedBuy).toBe(true);
  });

  it("only accepts refresh intervals the dropdown can express", () => {
    // Off is a real choice and must survive a round trip; an arbitrary
    // number would leave the dropdown showing nothing.
    expect(normalizeOrdersPrefs(JSON.stringify({ refreshMinutes: 0 })).refreshMinutes).toBe(0);
    expect(normalizeOrdersPrefs(JSON.stringify({ refreshMinutes: 7 })).refreshMinutes).toBe(
      ORDERS_DEFAULT_REFRESH_MINUTES,
    );
    expect(normalizeOrdersPrefs(JSON.stringify({ refreshMinutes: -3 })).refreshMinutes).toBe(
      ORDERS_DEFAULT_REFRESH_MINUTES,
    );
    expect(normalizeOrdersPrefs(JSON.stringify({ refreshMinutes: "3" })).refreshMinutes).toBe(
      ORDERS_DEFAULT_REFRESH_MINUTES,
    );
  });

  it("does not confuse a collapsed section with an unset one", () => {
    // `false` is a deliberate value, not an absent one.
    expect(normalizeOrdersPrefs(JSON.stringify({ collapsedSell: false })).collapsedSell).toBe(false);
    expect(normalizeOrdersPrefs(JSON.stringify({ collapsedSell: true })).collapsedSell).toBe(true);
    expect(normalizeOrdersPrefs(JSON.stringify({ collapsedSell: "yes" })).collapsedSell).toBe(false);
  });
});

describe("defaultDirForSortKey", () => {
  it("reads names forwards and money backwards", () => {
    expect(defaultDirForSortKey("type")).toBe("asc");
    expect(defaultDirForSortKey("owner")).toBe("asc");
    expect(defaultDirForSortKey("station")).toBe("asc");
    // Soonest to expire and soonest to fill are the ones needing attention.
    expect(defaultDirForSortKey("eta")).toBe("asc");
    expect(defaultDirForSortKey("expiry")).toBe("asc");
    expect(defaultDirForSortKey("notional")).toBe("desc");
    expect(defaultDirForSortKey("current")).toBe("desc");
    expect(defaultDirForSortKey("priority")).toBe("desc");
  });
});
