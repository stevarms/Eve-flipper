import { describe, expect, it } from "vitest";
import {
  applySortClick,
  defaultDirForSortKey,
  normalizeOrdersPrefs,
  ORDERS_DEFAULT_PREFS,
  ORDERS_DEFAULT_REFRESH_MINUTES,
  ORDERS_DEFAULT_TARGET_ETA_DAYS,
  ORDERS_MAX_SORT_LAYERS,
  type OrdersSortLayer,
} from "./ordersPrefs";

describe("orders preferences normalization", () => {
  it("opens on item A-to-Z with the default refresh interval", () => {
    // The tab is used beside the in-game Orders window, which lists items
    // alphabetically; anything else makes the two lists impossible to walk
    // together.
    expect(ORDERS_DEFAULT_PREFS.sort).toEqual([{ key: "type", dir: "asc" }]);
    expect(ORDERS_DEFAULT_PREFS.refreshMinutes).toBe(ORDERS_DEFAULT_REFRESH_MINUTES);
    expect(ORDERS_DEFAULT_PREFS.targetEtaDays).toBe(ORDERS_DEFAULT_TARGET_ETA_DAYS);
  });

  it("falls back to the defaults for anything unreadable", () => {
    for (const raw of [null, "", "{bad json", "[]", '"a string"', "null", "42"]) {
      expect(normalizeOrdersPrefs(raw)).toEqual(ORDERS_DEFAULT_PREFS);
    }
  });

  it("restores a full set of saved preferences", () => {
    const prefs = normalizeOrdersPrefs(
      JSON.stringify({
        sort: [
          { key: "type", dir: "asc" },
          { key: "priority", dir: "desc" },
        ],
        actionFilter: "needs_action",
        collapsedSell: false,
        collapsedBuy: true,
        refreshMinutes: 10,
        targetEtaDays: 7,
      }),
    );

    expect(prefs).toEqual({
      sort: [
        { key: "type", dir: "asc" },
        { key: "priority", dir: "desc" },
      ],
      actionFilter: "needs_action",
      collapsedSell: false,
      collapsedBuy: true,
      refreshMinutes: 10,
      targetEtaDays: 7,
    });
  });

  it("lifts the pre-stack single-key shape rather than resetting", () => {
    // The localStorage key did not change when the sort became a stack, so a
    // blob written by an earlier build is still there in every browser that
    // has used the tab.
    const prefs = normalizeOrdersPrefs(
      JSON.stringify({ sortKey: "notional", sortDir: "desc", collapsedBuy: true }),
    );
    expect(prefs.sort).toEqual([{ key: "notional", dir: "desc" }]);
    expect(prefs.collapsedBuy).toBe(true);

    // A legacy key with no usable direction still lands somewhere sensible.
    expect(normalizeOrdersPrefs(JSON.stringify({ sortKey: "owner" })).sort).toEqual([
      { key: "owner", dir: "asc" },
    ]);
  });

  it("refuses a stack that would not sort anything", () => {
    // Duplicates are dead weight, unknown columns cannot be compared, and an
    // empty stack is not a state the table should be able to reach.
    expect(
      normalizeOrdersPrefs(
        JSON.stringify({
          sort: [
            { key: "type", dir: "asc" },
            { key: "type", dir: "desc" },
            { key: "someRemovedColumn", dir: "asc" },
            { key: "owner", dir: "sideways" },
            null,
            "owner",
          ],
        }),
      ).sort,
    ).toEqual([{ key: "type", dir: "asc" }]);

    expect(normalizeOrdersPrefs(JSON.stringify({ sort: [] })).sort).toEqual(
      ORDERS_DEFAULT_PREFS.sort,
    );
    expect(normalizeOrdersPrefs(JSON.stringify({ sort: "type" })).sort).toEqual(
      ORDERS_DEFAULT_PREFS.sort,
    );
  });

  it("caps the stack at the number of layers the chips can show", () => {
    const prefs = normalizeOrdersPrefs(
      JSON.stringify({
        sort: [
          { key: "type", dir: "asc" },
          { key: "priority", dir: "desc" },
          { key: "notional", dir: "desc" },
          { key: "owner", dir: "asc" },
        ],
      }),
    );
    expect(prefs.sort).toHaveLength(ORDERS_MAX_SORT_LAYERS);
    expect(prefs.sort.map((l) => l.key)).toEqual(["type", "priority", "notional"]);
  });

  it("hands every caller its own sort array", () => {
    // The defaults are a module-level constant; a caller mutating the array
    // it got back would rewrite the default for the rest of the session.
    const a = normalizeOrdersPrefs(null);
    const b = normalizeOrdersPrefs(null);
    a.sort.push({ key: "owner", dir: "asc" });
    expect(b.sort).toEqual([{ key: "type", dir: "asc" }]);
    expect(ORDERS_DEFAULT_PREFS.sort).toEqual([{ key: "type", dir: "asc" }]);
  });

  it("only accepts a target ETA the input can express", () => {
    expect(normalizeOrdersPrefs(JSON.stringify({ targetEtaDays: 7 })).targetEtaDays).toBe(7);
    expect(normalizeOrdersPrefs(JSON.stringify({ targetEtaDays: 0.5 })).targetEtaDays).toBe(0.5);
    for (const bad of [0, -1, 31, 1000, "3", null, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(normalizeOrdersPrefs(JSON.stringify({ targetEtaDays: bad })).targetEtaDays).toBe(
        ORDERS_DEFAULT_TARGET_ETA_DAYS,
      );
    }
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

    expect(prefs.sort).toEqual(ORDERS_DEFAULT_PREFS.sort);
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

describe("applySortClick", () => {
  const stack = (...keys: Array<[OrdersSortLayer["key"], OrdersSortLayer["dir"]]>) =>
    keys.map(([key, dir]) => ({ key, dir }));

  it("makes a plain click mean 'sort by just this'", () => {
    // Single-column sorting is what the tab had before the stack, and it has
    // to keep working for someone who never learns the shortcut.
    expect(applySortClick(stack(["type", "asc"], ["priority", "desc"]), "owner", false)).toEqual(
      stack(["owner", "asc"]),
    );
    // Collapsing onto a column already in the stack does not also flip it —
    // one click, one change.
    expect(applySortClick(stack(["type", "desc"], ["priority", "desc"]), "type", false)).toEqual(
      stack(["type", "asc"]),
    );
  });

  it("flips direction when the lone column is clicked again", () => {
    expect(applySortClick(stack(["type", "asc"]), "type", false)).toEqual(stack(["type", "desc"]));
    expect(applySortClick(stack(["type", "desc"]), "type", false)).toEqual(stack(["type", "asc"]));
  });

  it("cycles a shift-clicked column through default, reversed, gone", () => {
    const one = applySortClick(stack(["type", "asc"]), "priority", true);
    expect(one).toEqual(stack(["type", "asc"], ["priority", "desc"]));

    const two = applySortClick(one, "priority", true);
    expect(two).toEqual(stack(["type", "asc"], ["priority", "asc"]));

    // The third shift-click drops it again, leaving the rest in place.
    expect(applySortClick(two, "priority", true)).toEqual(stack(["type", "asc"]));
  });

  it("displaces the least significant layer rather than ignoring the click", () => {
    const full = stack(["type", "asc"], ["priority", "desc"], ["notional", "desc"]);
    expect(applySortClick(full, "owner", true)).toEqual(
      stack(["type", "asc"], ["priority", "desc"], ["owner", "asc"]),
    );
  });

  it("never leaves the table unsorted", () => {
    // Cycling the only column past its last state has to land somewhere.
    const reversed = applySortClick(stack(["type", "asc"]), "type", true);
    expect(reversed).toEqual(stack(["type", "desc"]));
    expect(applySortClick(reversed, "type", true)).toHaveLength(1);
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
