import { useCallback, useMemo, useState } from "react";
import { priceAudit, type PriceAuditRow } from "@/lib/api";
import { formatISK, formatISKFull } from "@/lib/format";
import type { IndustryProjectSnapshot } from "@/lib/types";

/**
 * Sell-floor guard.
 *
 * Every project records, at commit time, what each unit of its saleable
 * output was expected to fetch and what it cost to make (see
 * industryPlanPatch's expected-value capture). Weeks later, when the stuff
 * finally comes out of the oven, the market has moved and there is no way to
 * tell from the in-game sell window whether the price on screen is still
 * above what the build cost.
 *
 * This panel answers exactly that: for each output product it puts the live
 * market price next to the plan-time cost basis and says whether selling now
 * is profit, a haircut, or an outright loss.
 *
 * Fee handling: the plan's expected_unit_revenue is NET of broker fee and
 * sales tax (it comes from the analyzer's sell_revenue). The live quote from
 * price-audit is a RAW ask. So the same haircut is applied to the live price
 * before either comparison is made — otherwise the guard would read several
 * percent optimistic and happily green-light a marginal loss.
 *
 * The market lookup is manual (a button) rather than automatic: it is an
 * external market call per output product, and it is only meaningful at the
 * moment the user is actually about to list something.
 */

interface OpsSellFloorPanelProps {
  ledgerSnapshot: IndustryProjectSnapshot | null;
  /** Pricing location for the live quote; the Industry tab's build station. */
  stationID: number;
  stationName: string;
  brokerFeePercent: number;
  salesTaxPercent: number;
  onError: (message: string) => void;
}

interface SellFloorRow {
  typeID: number;
  name: string;
  qty: number;
  /** Plan-time per-unit cost basis — the number you must not sell below. */
  costBasis: number;
  /** Plan-time per-unit revenue, net of fees. */
  plannedNet: number;
}

export function OpsSellFloorPanel({
  ledgerSnapshot,
  stationID,
  stationName,
  brokerFeePercent,
  salesTaxPercent,
  onError,
}: OpsSellFloorPanelProps) {
  const [quotes, setQuotes] = useState<Map<number, PriceAuditRow> | null>(null);
  const [loading, setLoading] = useState(false);
  const [checkedAt, setCheckedAt] = useState<string>("");

  // Only tasks carrying expected value are saleable output; intermediate
  // components deliberately hold zeros so they don't show up here.
  const rows: SellFloorRow[] = useMemo(() => {
    if (!ledgerSnapshot) return [];
    const byType = new Map<number, SellFloorRow>();
    for (const task of ledgerSnapshot.tasks) {
      if (task.status === "cancelled") continue;
      const qty = task.expected_output_qty ?? 0;
      const net = task.expected_unit_revenue ?? 0;
      const cost = task.expected_unit_cost ?? 0;
      if (qty <= 0 || net <= 0) continue;
      const existing = byType.get(task.product_type_id);
      if (existing) {
        // Two tasks can produce the same item (a split run). Blend their
        // economics by quantity so one line per product still reads true.
        const total = existing.qty + qty;
        existing.costBasis = (existing.costBasis * existing.qty + cost * qty) / total;
        existing.plannedNet = (existing.plannedNet * existing.qty + net * qty) / total;
        existing.qty = total;
        continue;
      }
      byType.set(task.product_type_id, {
        typeID: task.product_type_id,
        // Task names are prefixed with their activity ("manufacturing
        // Ishtar"); strip it so the column reads like an item name.
        name: (task.name || `Type ${task.product_type_id}`).replace(/^manufacturing\s+/i, ""),
        qty,
        costBasis: cost,
        plannedNet: net,
      });
    }
    return [...byType.values()].sort((a, b) => b.qty * b.plannedNet - a.qty * a.plannedNet);
  }, [ledgerSnapshot]);

  // Fraction of a listed price that survives broker fee + sales tax.
  const netFactor = Math.max(0, 1 - (brokerFeePercent + salesTaxPercent) / 100);

  const check = useCallback(async () => {
    if (rows.length === 0) return;
    if (stationID <= 0) {
      onError("Pick a build station first — the sell-floor check needs a market to price against.");
      return;
    }
    setLoading(true);
    try {
      const res = await priceAudit({
        station_id: stationID,
        items: rows.map((r) => ({ type_id: r.typeID, qty: r.qty })),
      });
      const map = new Map<number, PriceAuditRow>();
      for (const q of res.results) {
        if (q.type_id) map.set(q.type_id, q);
      }
      setQuotes(map);
      setCheckedAt(new Date().toLocaleTimeString());
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [rows, stationID, onError]);

  if (!ledgerSnapshot || rows.length === 0) return null;

  return (
    <div className="mt-2 border border-eve-border/40 rounded-sm bg-eve-dark/20">
      <div className="flex items-center flex-wrap gap-2 px-2 py-1.5 border-b border-eve-border/40">
        <span className="text-[10px] uppercase tracking-wider text-eve-dim">Sell floor</span>
        <span className="text-[11px] text-eve-dim">
          {rows.length} product{rows.length === 1 ? "" : "s"}
          {stationName ? ` · priced at ${stationName}` : ""}
        </span>
        {checkedAt && <span className="text-[10px] text-eve-dim">checked {checkedAt}</span>}
        <div className="flex-1" />
        <button
          type="button"
          onClick={() => { void check(); }}
          disabled={loading}
          className="px-2 py-0.5 text-[10px] uppercase tracking-wider border border-eve-accent/50 text-eve-accent rounded-sm hover:bg-eve-accent/10 disabled:opacity-50"
        >
          {loading ? "Checking…" : quotes ? "Re-check market" : "Check market"}
        </button>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-[11px]">
          <thead>
            <tr className="text-eve-dim uppercase tracking-wider border-b border-eve-border/60 text-[10px]">
              <th className="px-1.5 py-1 text-left">Product</th>
              <th className="px-1.5 py-1 text-right w-16">Qty</th>
              <th
                className="px-1.5 py-1 text-right w-28 cursor-help"
                title="Plan-time cost per unit. Selling below this loses money."
              >
                Floor / unit
              </th>
              <th className="px-1.5 py-1 text-right w-28 cursor-help" title="Plan-time revenue per unit, net of fees">
                Planned / unit
              </th>
              <th className="px-1.5 py-1 text-right w-28">Market ask</th>
              <th className="px-1.5 py-1 text-right w-28 cursor-help" title="Market ask after broker fee + sales tax">
                Net now
              </th>
              <th className="px-1.5 py-1 text-right w-20">vs plan</th>
              <th className="px-1.5 py-1 text-left w-32">Verdict</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const quote = quotes?.get(r.typeID);
              // low_sell is the cheapest competing ask — matching it is the
              // realistic outcome of listing right now.
              const ask = quote?.low_sell ?? quote?.suggested_price ?? 0;
              const netNow = ask * netFactor;
              const haveQuote = quotes !== null && ask > 0;
              const deltaPct = haveQuote && r.plannedNet > 0 ? (netNow - r.plannedNet) / r.plannedNet : 0;

              let verdict = "—";
              let verdictClass = "text-eve-dim";
              if (haveQuote) {
                if (netNow < r.costBasis) {
                  verdict = "BELOW COST";
                  verdictClass = "text-red-300";
                } else if (netNow < r.plannedNet) {
                  verdict = "UNDER PLAN";
                  verdictClass = "text-amber-300";
                } else {
                  verdict = "AT OR ABOVE PLAN";
                  verdictClass = "text-emerald-300";
                }
              } else if (quotes !== null) {
                verdict = "NO MARKET DATA";
                verdictClass = "text-slate-300";
              }

              return (
                <tr key={`sf-${r.typeID}`} className="border-b border-eve-border/30 hover:bg-eve-accent/5">
                  <td className="px-1.5 py-1 text-eve-text truncate">{r.name}</td>
                  <td className="px-1.5 py-1 text-right font-mono text-eve-accent">{r.qty.toLocaleString()}</td>
                  <td
                    className="px-1.5 py-1 text-right font-mono text-eve-text"
                    title={`${formatISKFull(r.costBasis)} ISK`}
                  >
                    {formatISK(r.costBasis)}
                  </td>
                  <td className="px-1.5 py-1 text-right font-mono text-eve-dim" title={`${formatISKFull(r.plannedNet)} ISK`}>
                    {formatISK(r.plannedNet)}
                  </td>
                  <td className="px-1.5 py-1 text-right font-mono text-eve-dim">
                    {haveQuote ? formatISK(ask) : "—"}
                  </td>
                  <td className={`px-1.5 py-1 text-right font-mono ${haveQuote && netNow < r.costBasis ? "text-red-300" : "text-eve-text"}`}>
                    {haveQuote ? formatISK(netNow) : "—"}
                  </td>
                  <td
                    className={`px-1.5 py-1 text-right font-mono ${
                      !haveQuote ? "text-eve-dim" : deltaPct >= 0 ? "text-emerald-300" : "text-red-300"
                    }`}
                  >
                    {haveQuote ? `${deltaPct >= 0 ? "+" : ""}${(deltaPct * 100).toFixed(1)}%` : "—"}
                  </td>
                  <td className={`px-1.5 py-1 text-[10px] uppercase tracking-wider ${verdictClass}`}>{verdict}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {quotes === null && (
        <div className="px-2 py-1.5 text-[10px] text-eve-dim border-t border-eve-border/40">
          Floor and planned prices are what this project was committed at. Check the market to see whether today's
          price still clears them.
        </div>
      )}
    </div>
  );
}
