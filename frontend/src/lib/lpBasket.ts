import type { LPOfferRow } from "@/lib/api";

// The LP Store tab's basket: the offers you have ticked and how many times you
// mean to redeem each. Everything here is plain arithmetic over the rows, kept
// out of the component so it can be tested without rendering one.

/** More days of the market's volume than this, and a selection gets a warning. */
export const LP_LIQUIDITY_WARN_DAYS = 7;

export interface LPLiquidityWarning {
  offer_id: number;
  name: string;
  /** Days of average volume the selection represents; null with no volume data. */
  days: number | null;
}

export interface LPBasket {
  /** LP the selection spends. */
  lp: number;
  /** ISK needed: store ISK plus required items at their buy price. */
  isk: number;
  /** Expected profit, each row at its best method. */
  profit: number;
  /** profit / lp over the rows that have a value. */
  blendedISKPerLP: number;
  /** Selected rows with no value (unpriced, or nothing known yet). */
  unvalued: number;
  overBalance: boolean;
  /** One "Name quantity" line per type, merged across every selected offer. */
  multibuy: string;
  warnings: LPLiquidityWarning[];
}

export interface LPBasketOptions {
  /** Add each selected blueprint's build materials to the multibuy. */
  includeBuild: boolean;
  /** LP available, or null when unknown. */
  balance: number | null;
}

export function computeLPBasket(
  rows: LPOfferRow[],
  selection: Map<number, number>,
  opts: LPBasketOptions,
): LPBasket {
  let lp = 0;
  let isk = 0;
  let profit = 0;
  let valuedLP = 0;
  let unvalued = 0;
  const lines = new Map<number, { name: string; qty: number }>();
  const warnings: LPLiquidityWarning[] = [];

  const add = (typeID: number, name: string, qty: number) => {
    if (qty <= 0) return;
    const cur = lines.get(typeID);
    if (cur) cur.qty += qty;
    else lines.set(typeID, { name, qty });
  };

  for (const row of rows) {
    const count = selection.get(row.offer_id) ?? 0;
    if (count <= 0) continue;

    lp += row.lp_cost * count;
    isk += row.cost * count;
    if (row.best == null) {
      unvalued++;
    } else {
      profit += row.best * row.lp_cost * count;
      valuedLP += row.lp_cost * count;
    }

    for (const ri of row.required_items) add(ri.type_id, ri.type_name, ri.quantity * count);
    if (opts.includeBuild && row.is_blueprint) {
      for (const m of row.build_materials ?? []) add(m.type_id, m.type_name, m.quantity * count);
    }

    const units = row.units_per_redemption * count;
    if (row.avg_daily_volume <= 0) {
      warnings.push({ offer_id: row.offer_id, name: row.type_name, days: null });
    } else {
      const days = units / row.avg_daily_volume;
      if (days > LP_LIQUIDITY_WARN_DAYS) warnings.push({ offer_id: row.offer_id, name: row.type_name, days });
    }
  }

  const multibuy = [...lines.values()].map((l) => `${l.name} ${Math.round(l.qty)}`).join("\n");

  return {
    lp,
    isk,
    profit,
    blendedISKPerLP: valuedLP > 0 ? profit / valuedLP : 0,
    unvalued,
    overBalance: opts.balance != null && lp > opts.balance,
    multibuy,
    warnings,
  };
}
