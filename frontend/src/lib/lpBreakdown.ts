import type { LPMethod, LPOfferRow } from "@/lib/api";
import type { TranslationKey } from "@/lib/i18n";

// The worked sum behind each LP Store value, line by line: what the sale (and
// for a build, the build) costs and brings in, every fee, and the profit that
// becomes ISK/LP. The table's tooltips and the row detail both show it, so
// any number on the page can be checked by hand.

export type LPValueMethod = Exclude<LPMethod, "">;

type T = (key: TranslationKey, params?: Record<string, string | number>) => string;
type Fmt = (isk: number) => string;

// Counts as text. A field missing from a row (one saved by an older version,
// say) reads as 0 rather than throwing while the table renders.
const count = (x: number | undefined | null) => (x ?? 0).toLocaleString();
const num = (x: number | undefined | null) => x ?? 0;

/** The value for a method, as the row carries it. */
export function lpValueFor(r: LPOfferRow, method: LPValueMethod): number | null {
  switch (method) {
    case "sell":
      return r.instant;
    case "list":
      return r.listed;
    case "sell_bpc":
      return r.bpc_sale;
    case "build_sell":
      return r.build_instant;
    case "build_list":
      return r.build_listed;
  }
}

/** Methods that apply to this row and have a value, best first. */
export function lpMethodsWithValues(r: LPOfferRow): LPValueMethod[] {
  const all: LPValueMethod[] = r.is_blueprint ? ["build_list", "build_sell", "sell_bpc"] : ["list", "sell"];
  return all
    .filter((m) => lpValueFor(r, m) != null)
    .sort((a, b) => (lpValueFor(r, b) ?? 0) - (lpValueFor(r, a) ?? 0));
}

/**
 * The breakdown lines for one way of realising an offer. Empty when the value
 * is unknown. The last three lines are always the same: the offer's cost,
 * the profit from one redemption, and that profit per LP.
 */
export function lpBreakdownLines(r: LPOfferRow, method: LPValueMethod, t: T, fmt: Fmt): string[] {
  const v = lpValueFor(r, method);
  if (r.unpriced || v == null) return [];
  const broker = num(r.broker_fee_percent);
  const tax = num(r.sales_tax_percent);
  const lines: string[] = [];

  const saleFees = (gross: number, withBroker: boolean) => {
    if (withBroker) lines.push(t("lpBdBroker", { rate: broker, amount: fmt(gross * (broker / 100)) }));
    lines.push(t("lpBdTax", { rate: tax, amount: fmt(gross * (tax / 100)) }));
  };

  switch (method) {
    case "sell": {
      const gross = r.quantity * r.unit_bid;
      lines.push(t("lpBdSell", { qty: count(r.quantity), price: fmt(num(r.unit_bid)), gross: fmt(gross) }));
      saleFees(gross, false);
      lines.push(t("lpBdNet", { amount: fmt(gross * (1 - tax / 100)) }));
      break;
    }
    case "list": {
      const gross = r.quantity * r.unit_ask;
      lines.push(t("lpBdList", { qty: count(r.quantity), price: fmt(num(r.unit_ask)), gross: fmt(gross) }));
      saleFees(gross, true);
      lines.push(t("lpBdNet", { amount: fmt(gross * (1 - (broker + tax) / 100)) }));
      break;
    }
    case "sell_bpc": {
      lines.push(
        t(r.bpc_override ? "lpBdBPCOverride" : "lpBdBPC", {
          runs: r.runs,
          price: fmt(num(r.bpc_per_run)),
          gross: fmt(r.runs * r.bpc_per_run),
          samples: r.bpc_samples,
        }),
      );
      lines.push(t("lpBdNoContractFees"));
      break;
    }
    case "build_sell":
    case "build_list": {
      const listed = method === "build_list";
      lines.push(t("lpBdBuild", { units: count(r.build_units), product: r.product_name, runs: r.runs }));
      for (const m of r.build_materials ?? []) {
        lines.push(t("lpBdMaterial", { qty: count(m.quantity), item: m.type_name, price: fmt(num(m.unit_price)), total: fmt(num(m.total_price)) }));
      }
      lines.push(t("lpBdMaterialsTotal", { amount: fmt(num(r.build_material_cost)) }));
      lines.push(t("lpBdJob", { amount: fmt(num(r.build_job_cost)) }));
      lines.push(t("lpBdBuildCost", { amount: fmt(num(r.build_cost)) }));
      const gross = num(listed ? r.build_listed_gross : r.build_instant_gross);
      const net = num(listed ? r.build_listed_net : r.build_instant_net);
      lines.push(
        listed
          ? t("lpBdBuildList", { units: count(r.build_units), price: fmt(num(r.unit_ask)), gross: fmt(gross) })
          : t("lpBdBuildSell", { units: count(r.build_units), gross: fmt(gross) }),
      );
      saleFees(gross, listed);
      lines.push(t("lpBdNet", { amount: fmt(net) }));
      lines.push(t("lpBdBuildProfit", { amount: fmt(net - num(r.build_cost)) }));
      break;
    }
  }

  const profit = v * r.lp_cost;
  lines.push(t("lpBdOfferCost", { amount: fmt(num(r.cost)) }));
  lines.push(t("lpBdProfit", { amount: fmt(profit) }));
  lines.push(t("lpBdPerLP", { lp: count(r.lp_cost), value: Math.round(v).toLocaleString() }));
  return lines;
}
