import type { LPMethod, LPOfferRow } from "@/lib/api";
import type { TranslationKey } from "@/lib/i18n";

// The worked sum behind each LP Store value, line by line: what the sale (and
// for a build, the build) costs and brings in, every fee, and the profit that
// becomes ISK/LP. The table's tooltips and the row detail both show it, so
// any number on the page can be checked by hand.

export type LPValueMethod = Exclude<LPMethod, "">;

type T = (key: TranslationKey, params?: Record<string, string | number>) => string;
type Fmt = (isk: number) => string;

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
  const broker = r.broker_fee_percent;
  const tax = r.sales_tax_percent;
  const lines: string[] = [];

  const saleFees = (gross: number, withBroker: boolean) => {
    if (withBroker) lines.push(t("lpBdBroker", { rate: broker, amount: fmt(gross * (broker / 100)) }));
    lines.push(t("lpBdTax", { rate: tax, amount: fmt(gross * (tax / 100)) }));
  };

  switch (method) {
    case "sell": {
      const gross = r.quantity * r.unit_bid;
      lines.push(t("lpBdSell", { qty: r.quantity.toLocaleString(), price: fmt(r.unit_bid), gross: fmt(gross) }));
      saleFees(gross, false);
      lines.push(t("lpBdNet", { amount: fmt(gross * (1 - tax / 100)) }));
      break;
    }
    case "list": {
      const gross = r.quantity * r.unit_ask;
      lines.push(t("lpBdList", { qty: r.quantity.toLocaleString(), price: fmt(r.unit_ask), gross: fmt(gross) }));
      saleFees(gross, true);
      lines.push(t("lpBdNet", { amount: fmt(gross * (1 - (broker + tax) / 100)) }));
      break;
    }
    case "sell_bpc": {
      lines.push(
        t(r.bpc_override ? "lpBdBPCOverride" : "lpBdBPC", {
          runs: r.runs,
          price: fmt(r.bpc_per_run),
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
      lines.push(t("lpBdBuild", { units: r.build_units.toLocaleString(), product: r.product_name, runs: r.runs }));
      for (const m of r.build_materials ?? []) {
        lines.push(t("lpBdMaterial", { qty: m.quantity.toLocaleString(), item: m.type_name, price: fmt(m.unit_price), total: fmt(m.total_price) }));
      }
      lines.push(t("lpBdMaterialsTotal", { amount: fmt(r.build_material_cost) }));
      lines.push(t("lpBdJob", { amount: fmt(r.build_job_cost) }));
      lines.push(t("lpBdBuildCost", { amount: fmt(r.build_cost) }));
      const gross = listed ? r.build_listed_gross : r.build_instant_gross;
      const net = listed ? r.build_listed_net : r.build_instant_net;
      lines.push(
        listed
          ? t("lpBdBuildList", { units: r.build_units.toLocaleString(), price: fmt(r.unit_ask), gross: fmt(gross) })
          : t("lpBdBuildSell", { units: r.build_units.toLocaleString(), gross: fmt(gross) }),
      );
      saleFees(gross, listed);
      lines.push(t("lpBdNet", { amount: fmt(net) }));
      lines.push(t("lpBdBuildProfit", { amount: fmt(net - r.build_cost) }));
      break;
    }
  }

  const profit = v * r.lp_cost;
  lines.push(t("lpBdOfferCost", { amount: fmt(r.cost) }));
  lines.push(t("lpBdProfit", { amount: fmt(profit) }));
  lines.push(t("lpBdPerLP", { lp: r.lp_cost.toLocaleString(), value: Math.round(v).toLocaleString() }));
  return lines;
}
