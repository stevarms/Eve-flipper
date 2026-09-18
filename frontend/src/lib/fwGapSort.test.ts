import { describe, expect, it } from "vitest";
import { sortGapRows, type GapSortKey } from "./fwGapSort";
import type { FWSupplyRow } from "./api";

/** A row with only the fields the sort reads. The rest of FWSupplyRow is not
 *  what this is about, so it is filled in once and forgotten. */
function row(over: Partial<FWSupplyRow>): FWSupplyRow {
  return {
    type_id: 1,
    type_name: "Thing",
    category: "ammo",
    daily_destroyed: 1,
    kills_with_item: 10,
    daily_destroyed_long: 0,
    kills_with_item_long: 0,
    sized_by: "short",
    total_destroyed: 10,
    stocked_qty: 0,
    local_order_count: 0,
    days_of_cover: 0,
    jita_best_sell: 100,
    local_best_sell: 0,
    landed_cost: 100,
    floor_price: 110,
    reference_price: 150,
    reference_markup: 1.5,
    reference_source: "ladder",
    suggested_price: 150,
    suggested_markup: 1.5,
    price_rule: "reference",
    price_reason: "",
    competing_units_below: 0,
    competing_orders_below: 0,
    verdict: "gap",
    verdict_reason: "",
    suggested_qty: 10,
    cover_sized_qty: 10,
    qty_reason: "",
    cargo_m3: 1,
    cost_isk: 1000,
    net_unit_isk: 140,
    unit_profit_isk: 40,
    margin_pct: 40,
    profit_isk: 400,
    ...over,
  } as FWSupplyRow;
}

const names = (rows: FWSupplyRow[]) => rows.map((r) => r.type_name);

describe("the gap table's sort", () => {
  /* The default is not a column. Server order is gap, then thin, then
   * unpriceable, then covered, and within each the deepest hole first -- the one
   * ordering that says "ship this first". A column default would discard it
   * silently, which is the worst way to lose a ranking. */
  it("leaves the server's ranking alone when no column is chosen", () => {
    const rows = [
      row({ type_id: 1, type_name: "deep gap", profit_isk: 10 }),
      row({ type_id: 2, type_name: "shallow gap", profit_isk: 900 }),
      row({ type_id: 3, type_name: "thin", verdict: "thin", profit_isk: 500 }),
    ];
    expect(names(sortGapRows(rows, null, "desc"))).toEqual(["deep gap", "shallow gap", "thin"]);
  });

  /* An unpriceable row has no profit to compare, and the table shows an em dash
   * for it. If that read as zero it would head an ascending sort; if it read as
   * absent-but-sortable it could head a descending one. Neither is useful, so it
   * goes last either way and the rows that carry a number stay adjacent. */
  it("sinks blanks in both directions", () => {
    const rows = [
      row({ type_id: 1, type_name: "unpriceable", verdict: "unpriceable", profit_isk: 0 }),
      row({ type_id: 2, type_name: "small", profit_isk: 100 }),
      row({ type_id: 3, type_name: "big", profit_isk: 900 }),
    ];
    expect(names(sortGapRows(rows, "profit", "desc"))).toEqual(["big", "small", "unpriceable"]);
    expect(names(sortGapRows(rows, "profit", "asc"))).toEqual(["small", "big", "unpriceable"]);
  });

  /* Cover with nothing being destroyed is not a blank -- the table shows an
   * infinity sign, and it means "stocked past measuring". It belongs at the top
   * of a descending sort on cover, with the finite readings under it. */
  it("treats unbounded cover as the top of the scale, not as missing", () => {
    const rows = [
      row({ type_id: 1, type_name: "some cover", days_of_cover: 4 }),
      row({ type_id: 2, type_name: "no destruction", days_of_cover: null, verdict: "covered" }),
      row({ type_id: 3, type_name: "empty", days_of_cover: 0 }),
    ];
    expect(names(sortGapRows(rows, "cover", "desc"))).toEqual(["no destruction", "some cover", "empty"]);
    expect(names(sortGapRows(rows, "cover", "asc"))).toEqual(["empty", "some cover", "no destruction"]);
  });

  it("sorts the verdict column by the server's rank, not alphabetically", () => {
    const rows = [
      row({ type_id: 1, type_name: "u", verdict: "unpriceable" }),
      row({ type_id: 2, type_name: "c", verdict: "covered" }),
      row({ type_id: 3, type_name: "g", verdict: "gap" }),
      row({ type_id: 4, type_name: "t", verdict: "thin" }),
    ];
    // Ascending on the rank is the ranking itself: gap, thin, unpriceable,
    // covered -- and alphabetically it would have been covered, gap, thin,
    // unpriceable, which is the mistake this pins.
    expect(names(sortGapRows(rows, "verdict", "asc"))).toEqual(["g", "t", "u", "c"]);
    expect(names(sortGapRows(rows, "verdict", "desc"))).toEqual(["c", "u", "t", "g"]);
  });

  it("returns to the ranked order when the column is cleared", () => {
    const rows = [
      row({ type_id: 1, type_name: "first", cost_isk: 5 }),
      row({ type_id: 2, type_name: "second", cost_isk: 500 }),
    ];
    const sorted = sortGapRows(rows, "isk", "desc");
    expect(names(sorted)).toEqual(["second", "first"]);
    expect(names(sortGapRows(rows, null, "desc"))).toEqual(["first", "second"]);
    // And the sort did not mutate what it was handed, which is why clearing works.
    expect(names(rows)).toEqual(["first", "second"]);
  });

  it("keeps ties in ranked order rather than reshuffling them", () => {
    const rows = [
      row({ type_id: 1, type_name: "a", cost_isk: 100 }),
      row({ type_id: 2, type_name: "b", cost_isk: 100 }),
      row({ type_id: 3, type_name: "c", cost_isk: 100 }),
    ];
    for (const dir of ["asc", "desc"] as const) {
      expect(names(sortGapRows(rows, "isk" as GapSortKey, dir))).toEqual(["a", "b", "c"]);
    }
  });

  /* The long-window column sorts on its own rate, independently of the short
   * one. The two windows disagreeing is the entire point of showing both, so a
   * sort that quietly fell back to the short rate would hide exactly the
   * divergence the reader clicked the column to find. */
  it("sorts the long window's rate independently of the short one", () => {
    const rows = [
      row({ type_id: 1, type_name: "spike", daily_destroyed: 40, daily_destroyed_long: 2 }),
      row({ type_id: 2, type_name: "staple", daily_destroyed: 1, daily_destroyed_long: 30 }),
      row({ type_id: 3, type_name: "steady", daily_destroyed: 9, daily_destroyed_long: 9 }),
    ];
    expect(names(sortGapRows(rows, "destroyed", "desc"))).toEqual(["spike", "steady", "staple"]);
    expect(names(sortGapRows(rows, "destroyedLong", "desc"))).toEqual(["staple", "steady", "spike"]);
  });

  /* A zero long rate is a blank, and blanks sort last both ways -- the same rule
   * the other columns follow. It matters more here than elsewhere: every row of
   * a plan generated before a long window was set carries zero, and those must
   * not head the column in either direction. */
  it("sinks rows with no long-window reading in both directions", () => {
    const rows = [
      row({ type_id: 1, type_name: "unmeasured", daily_destroyed_long: 0 }),
      row({ type_id: 2, type_name: "low", daily_destroyed_long: 0.4 }),
      row({ type_id: 3, type_name: "high", daily_destroyed_long: 900 }),
    ];
    expect(names(sortGapRows(rows, "destroyedLong", "desc"))).toEqual(["high", "low", "unmeasured"]);
    expect(names(sortGapRows(rows, "destroyedLong", "asc"))).toEqual(["low", "high", "unmeasured"]);
  });
});
