import type { FWSupplyRow } from "@/lib/api";

/* Sorting the FW Supply gap table.
 *
 * The table arrives already ranked by the server -- gap, then thin, then
 * unpriceable, then covered, and within each the deepest hole first -- and that
 * ranking is the one ordering that encodes "ship this first". So sorting is
 * something you turn on, never the state the table starts in, and `null` for the
 * key means "leave the server's order alone".
 */

/** The server's verdict ranking, mirrored so the verdict column sorts the way
 *  the table is ranked rather than alphabetically. */
export const VERDICT_RANK: Record<string, number> = { gap: 0, thin: 1, unpriceable: 2, covered: 3 };

export type GapSortKey =
  | "item"
  | "destroyed"
  /** The long window's rate. One key for a column that shows a rate and a kill
   *  count together, the way `local` shows a price and an order count: the count
   *  qualifies the rate, so sorting on the rate is what the column is for. */
  | "destroyedLong"
  | "kills"
  | "stocked"
  | "cover"
  | "jita"
  | "local"
  | "reference"
  | "suggested"
  | "qty"
  | "m3"
  | "isk"
  | "profit"
  | "verdict";

export type SortDir = "asc" | "desc";

/** blank() marks a value the table renders as an em dash. Those sort last in
 *  both directions: an unpriceable row's absent profit must not win a
 *  descending sort on profit, and it must not head an ascending one either. */
function blank(value: number): number | null {
  return Number.isFinite(value) && value !== 0 ? value : null;
}

/** gapSortValue is the sortable value of one cell, and it agrees with what the
 *  cell shows. Where the cell shows an em dash the value is null; where it shows
 *  infinity -- cover with nothing being destroyed -- the value is Infinity,
 *  because that is a reading at the top of the scale, not a missing one. */
export function gapSortValue(key: GapSortKey, r: FWSupplyRow): number | string | null {
  switch (key) {
    case "item":
      return r.type_name || null;
    case "destroyed":
      return blank(r.daily_destroyed);
    case "destroyedLong":
      // Blank, not zero. A staple that shows nothing over a quarter and a row
      // from a plan generated before the long window existed look identical
      // here, and neither is a reading worth heading a descending sort.
      return blank(r.daily_destroyed_long);
    case "kills":
      return r.kills_with_item;
    case "stocked":
      return r.stocked_qty;
    case "cover":
      return r.days_of_cover == null ? Infinity : r.days_of_cover;
    case "jita":
      return blank(r.jita_best_sell);
    case "local":
      return blank(r.local_best_sell);
    case "reference":
      return blank(r.reference_price);
    case "suggested":
      return blank(r.suggested_price);
    case "qty":
      return r.suggested_qty;
    case "m3":
      return r.cargo_m3;
    case "isk":
      return r.cost_isk;
    case "profit":
      return blank(r.profit_isk);
    case "verdict":
      return VERDICT_RANK[r.verdict] ?? 99;
  }
}

/** sortGapRows returns rows in the chosen order, or the rows unchanged when no
 *  column is chosen. Position is the tiebreak, so equal cells keep the server's
 *  ranking instead of reshuffling, and the sort never mutates its input. */
export function sortGapRows<T extends FWSupplyRow>(rows: T[], key: GapSortKey | null, dir: SortDir): T[] {
  if (key == null) return rows;
  const sign = dir === "asc" ? 1 : -1;
  return rows
    .map((r, i) => ({ r, i }))
    .sort((a, b) => {
      const av = gapSortValue(key, a.r);
      const bv = gapSortValue(key, b.r);
      if (av == null && bv == null) return a.i - b.i;
      if (av == null) return 1;
      if (bv == null) return -1;
      if (typeof av === "string" || typeof bv === "string") {
        const c = String(av).localeCompare(String(bv));
        return c !== 0 ? c * sign : a.i - b.i;
      }
      return av !== bv ? (av < bv ? -1 : 1) * sign : a.i - b.i;
    })
    .map((x) => x.r);
}
