import { type TranslationKey } from "../../lib/i18n";
import type {
  PortfolioPnL,
  PortfolioSlotEfficiency,
  StationPnL,
} from "../../lib/types";

// PnLChart's data shape — just the fields the CSS bar chart uses. Kept
// permissive here (rather than PortfolioPnL["daily_pnl"]) so callers with
// their own daily-entry shape (the Journal's per-source series) can pass
// compatible rows without a cast. The analytics view passes
// PortfolioPnL["daily_pnl"] directly — that shape is a superset.
export interface PnLChartEntry {
  date: string;
  net_pnl: number;
  cumulative_pnl: number;
  drawdown_pct?: number;
  transactions?: number;
}

// Shared P&L primitives — the Trade Journal's Summary chart and its Analytics
// view both render from here, so the two depths of the same numbers cannot
// drift apart visually either.

// --- P&L Line Chart (SVG) ---

export interface PnLLineSeries {
  key: string;
  label: string;
  // A Tailwind text-* class. The SVG paints with currentColor so a series
  // follows the active theme instead of hard-coding a hex that the dark and
  // light palettes would disagree about.
  colorClass: string;
  data: PnLChartEntry[];
}

// The viewBox is a fixed 1000 units wide with preserveAspectRatio="none", so
// the geometry scales to any container width without measuring it in JS.
// vector-effect="non-scaling-stroke" is what stops the strokes being
// stretched along with it.
const LINE_VB_WIDTH = 1000;

// Cumulative P&L drawn as lines rather than bars.
//
// A running total is a continuous quantity. As bars it becomes ~30 near-equal
// filled rectangles whose top edge is the only part carrying information --
// a line chart with a great deal of surplus ink -- and three solid masses are
// much harder to compare than three curves, which is exactly what the
// side-by-side layout asks you to do.
export function PnLLineChart({
  series,
  formatIsk,
  height = 120,
}: {
  series: PnLLineSeries[];
  formatIsk: (v: number) => string;
  height?: number;
}) {
  const withData = series.filter((s) => s.data.length > 0);
  if (withData.length === 0) return null;

  // The longest series drives the x-axis; a shorter one simply stops early.
  const spine = withData.reduce((a, b) => (b.data.length > a.data.length ? b : a));
  const points = spine.data.length;

  // One y-scale across every series: an overlay whose lines each had their own
  // scale would invite exactly the comparison it cannot support.
  const all = withData.flatMap((s) => s.data.map((d) => d.cumulative_pnl));
  const maxVal = Math.max(...all, 0);
  const minVal = Math.min(...all, 0);
  const range = maxVal - minVal || 1;

  const yFor = (v: number) => ((maxVal - v) / range) * height;
  const xFor = (i: number) =>
    points <= 1 ? LINE_VB_WIDTH / 2 : (i / (points - 1)) * LINE_VB_WIDTH;
  const zeroY = yFor(0);
  // One series gets a filled area; three overlaid would just muddy each other.
  const fillArea = withData.length === 1;

  const linePath = (s: PnLLineSeries) =>
    s.data
      .map(
        (d, i) =>
          `${i === 0 ? "M" : "L"} ${xFor(i).toFixed(2)} ${yFor(d.cumulative_pnl).toFixed(2)}`
      )
      .join(" ");
  const areaPath = (s: PnLLineSeries) =>
    `${linePath(s)} L ${xFor(s.data.length - 1).toFixed(2)} ${zeroY.toFixed(2)} L ${xFor(0).toFixed(2)} ${zeroY.toFixed(2)} Z`;

  return (
    <div className="relative">
      <div className="relative pl-11" style={{ height }}>
        <svg
          className="w-full h-full"
          viewBox={`0 0 ${LINE_VB_WIDTH} ${height}`}
          preserveAspectRatio="none"
        >
          <line
            x1={0}
            x2={LINE_VB_WIDTH}
            y1={zeroY}
            y2={zeroY}
            className="text-eve-border"
            stroke="currentColor"
            strokeWidth={1}
            strokeDasharray="4 4"
            vectorEffect="non-scaling-stroke"
          />
          {withData.map((s) => (
            <g key={s.key} className={s.colorClass}>
              {fillArea && <path d={areaPath(s)} fill="currentColor" opacity={0.12} />}
              <path
                d={linePath(s)}
                fill="none"
                stroke="currentColor"
                strokeWidth={1.5}
                strokeLinejoin="round"
                strokeLinecap="round"
                vectorEffect="non-scaling-stroke"
              />
            </g>
          ))}
        </svg>

        {/* Hover columns over the SVG: one invisible strip per day, which buys
            a crosshair, per-series dots and a tooltip with no pointer maths. */}
        <div className="absolute inset-y-0 right-0 left-11 flex">
          {spine.data.map((entry, i) => (
            <div key={entry.date} className="relative flex-1 group">
              <div className="absolute inset-y-0 left-1/2 w-px bg-eve-border-light opacity-0 group-hover:opacity-100" />
              {withData.map((s) => {
                const d = s.data[i];
                if (!d) return null;
                return (
                  <div
                    key={s.key}
                    className={`absolute w-1.5 h-1.5 rounded-full bg-current -translate-x-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 ${s.colorClass}`}
                    style={{ left: "50%", top: yFor(d.cumulative_pnl) }}
                  />
                );
              })}
              {/* Both numbers, named. The line is a running total from the
                  start of the selected window, so the same calendar day reads
                  differently at 7d and 30d -- correct, but indistinguishable
                  from a wrong number when only one figure is shown. */}
              <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-10 pointer-events-none">
                <div className="bg-eve-dark border border-eve-border rounded px-2 py-1 text-[10px] whitespace-nowrap shadow-lg">
                  <div className="text-eve-dim">{entry.date}</div>
                  {withData.map((s) => {
                    const d = s.data[i];
                    if (!d) return null;
                    return (
                      <div key={s.key} className="text-eve-dim">
                        {s.label ? <span className={s.colorClass}>{s.label} </span> : null}
                        day{" "}
                        <span className={d.net_pnl >= 0 ? "text-emerald-400" : "text-red-400"}>
                          {d.net_pnl >= 0 ? "+" : ""}
                          {formatIsk(d.net_pnl)}
                        </span>
                        {" / running "}
                        <span
                          className={d.cumulative_pnl >= 0 ? "text-emerald-400" : "text-red-400"}
                        >
                          {d.cumulative_pnl >= 0 ? "+" : ""}
                          {formatIsk(d.cumulative_pnl)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
          ))}
        </div>

        {/* Y-axis ticks, in their own gutter so they cannot land on the dates. */}
        <div className="absolute left-0 top-0 w-11 pointer-events-none" style={{ height }}>
          <span className="absolute right-1 top-0 text-[9px] text-eve-dim leading-none">
            {maxVal >= 0 ? "+" : ""}
            {formatIsk(maxVal)}
          </span>
          {zeroY > 12 && zeroY < height - 12 && (
            <span
              className="absolute right-1 text-[9px] text-eve-dim leading-none -translate-y-1/2"
              style={{ top: zeroY }}
            >
              0
            </span>
          )}
          <span
            className="absolute right-1 text-[9px] text-eve-dim leading-none -translate-y-full"
            style={{ top: height }}
          >
            {formatIsk(minVal)}
          </span>
        </div>
      </div>

      <div className="flex justify-between mt-1 pl-11 pr-1">
        <span className="text-[9px] text-eve-dim">{spine.data[0]?.date.slice(5)}</span>
        {points > 2 && (
          <span className="text-[9px] text-eve-dim">
            {spine.data[Math.floor(points / 2)]?.date.slice(5)}
          </span>
        )}
        <span className="text-[9px] text-eve-dim">{spine.data[points - 1]?.date.slice(5)}</span>
      </div>
    </div>
  );
}

// --- P&L Bar Chart (CSS-based) ---

export function PnLChart({
  data,
  mode,
  formatIsk,
}: {
  data: PnLChartEntry[];
  mode: "daily" | "cumulative" | "drawdown";
  formatIsk: (v: number) => string;
}) {
  if (data.length === 0) return null;

  // Bars are for discrete per-day quantities. A running total is not one, so
  // cumulative hands off to the line renderer -- here rather than at each call
  // site, so there is only ever one cumulative chart to keep correct.
  if (mode === "cumulative") {
    return (
      <PnLLineChart
        series={[{ key: "cumulative", label: "", colorClass: "text-eve-accent", data }]}
        formatIsk={formatIsk}
      />
    );
  }

  const valueOf = (d: PnLChartEntry) => (mode === "daily" ? d.net_pnl : (d.drawdown_pct ?? 0));
  const values = data.map(valueOf);
  const maxAbs = Math.max(...values.map(Math.abs), 1);

  // Show fewer bars if too many days
  const maxBars = 60;
  const step = data.length > maxBars ? Math.ceil(data.length / maxBars) : 1;
  const sampled = step > 1 ? data.filter((_, i) => i % step === 0) : data;
  // Read through the same accessor as `values`. These used to disagree in
  // drawdown mode -- the scale came from drawdown_pct while the bars were
  // drawn from cumulative_pnl, so the bars were ISK rendered as percentages.
  const sampledValues = sampled.map(valueOf);

  // Bars stretch to fill whatever width the panel gives them rather than
  // sitting at a fixed 12px in the middle of it.
  const barBox = { flex: "1 1 0%", minWidth: 2, maxWidth: 28 } as const;
  const chartHeight = 120;
  const midY = chartHeight / 2;

  return (
    <div className="relative">
      {/* Chart area */}
      <div className="relative" style={{ height: chartHeight }}>
        {mode === "drawdown" ? (
          /* Drawdown mode: all bars go downward from top (0%) */
          <div className="flex items-start justify-center gap-px h-full pl-11">
            {sampled.map((entry, i) => {
              const val = sampledValues[i]; // always <= 0
              const barH = Math.max(1, (Math.abs(val) / maxAbs) * (chartHeight - 8));
              return (
                <div
                  key={entry.date}
                  className="relative group"
                  style={{ ...barBox, height: chartHeight }}
                >
                  <div
                    className="bg-red-500/60 hover:bg-red-400/80 transition-colors rounded-b-[1px]"
                    style={{ width: "100%", height: barH }}
                  />
                  {/* Tooltip */}
                  <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-10 pointer-events-none">
                    <div className="bg-eve-dark border border-eve-border rounded px-2 py-1 text-[10px] whitespace-nowrap shadow-lg">
                      <div className="text-eve-dim">{entry.date}</div>
                      <div className="text-red-400">{val.toFixed(1)}%</div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          /* Daily mode: bars grow from the center line */
          <div className="flex items-end justify-center gap-px h-full pl-11">
            {sampled.map((entry, i) => {
              const val = sampledValues[i];
              const pct = Math.abs(val) / maxAbs;
              const barH = Math.max(1, pct * (chartHeight / 2 - 4));
              const isPositive = val >= 0;

              return (
                <div
                  key={entry.date}
                  className="relative group flex flex-col items-center"
                  style={{ ...barBox, height: chartHeight }}
                >
                  {/* Top half */}
                  <div className="flex-1 flex items-end justify-center w-full">
                    {isPositive && (
                      <div
                        className="rounded-t-[1px] bg-emerald-500/80 hover:bg-emerald-400 transition-colors"
                        style={{ width: "100%", height: barH }}
                      />
                    )}
                  </div>
                  {/* Bottom half */}
                  <div className="flex-1 flex items-start justify-center w-full">
                    {!isPositive && (
                      <div
                        className="rounded-b-[1px] bg-red-500/80 hover:bg-red-400 transition-colors"
                        style={{ width: "100%", height: barH }}
                      />
                    )}
                  </div>

                  {/* Tooltip */}
                  <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-10 pointer-events-none">
                    <div className="bg-eve-dark border border-eve-border rounded px-2 py-1 text-[10px] whitespace-nowrap shadow-lg">
                      <div className="text-eve-dim">{entry.date}</div>
                      <div className={isPositive ? "text-emerald-400" : "text-red-400"}>
                        {val >= 0 ? "+" : ""}
                        {formatIsk(val)} ISK
                      </div>
                      <div className="text-eve-dim">{entry.transactions} txns</div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}

        {/* Zero line */}
        {mode === "daily" && (
          <div
            className="absolute left-11 right-0 border-t border-eve-border/50"
            style={{ top: midY }}
          />
        )}

        {/* Y-axis labels, in their own gutter. They used to span the whole
            component and be pulled left by -translate-x-full, which put the
            bottom tick outside the box and on top of the first x-axis date --
            visible as soon as two charts sat side by side. */}
        <div
          className="absolute left-0 top-0 w-11 pointer-events-none"
          style={{ height: chartHeight }}
        >
          <span className="absolute right-1 top-0 text-[9px] text-eve-dim leading-none">
            {mode === "drawdown" ? "0%" : `+${formatIsk(maxAbs)}`}
          </span>
          {mode === "daily" && (
            <span
              className="absolute right-1 text-[9px] text-eve-dim leading-none -translate-y-1/2"
              style={{ top: midY }}
            >
              0
            </span>
          )}
          <span
            className="absolute right-1 text-[9px] text-eve-dim leading-none -translate-y-full"
            style={{ top: chartHeight }}
          >
            {mode === "drawdown" ? `${Math.min(...values).toFixed(1)}%` : `-${formatIsk(maxAbs)}`}
          </span>
        </div>
      </div>

      {/* X-axis labels */}
      <div className="flex justify-between mt-1 pl-11 pr-1">
        <span className="text-[9px] text-eve-dim">{sampled[0]?.date.slice(5)}</span>
        {sampled.length > 2 && (
          <span className="text-[9px] text-eve-dim">
            {sampled[Math.floor(sampled.length / 2)]?.date.slice(5)}
          </span>
        )}
        <span className="text-[9px] text-eve-dim">{sampled[sampled.length - 1]?.date.slice(5)}</span>
      </div>
    </div>
  );
}

// --- Slot Efficiency Table ---

export function SlotEfficiencyTable({
  rows,
  formatIsk,
}: {
  rows: PortfolioSlotEfficiency[];
  formatIsk: (v: number) => string;
}) {
  if (!rows || rows.length === 0) {
    return (
      <div className="text-center text-eve-dim text-xs py-4">
        No slot efficiency data yet. Sync active orders and wallet transactions to review ISK per market slot.
      </div>
    );
  }

  const maxAbs = Math.max(...rows.map((row) => Math.abs(row.isk_per_slot ?? 0)), 1);

  return (
    <div className="border border-eve-border rounded-sm overflow-x-auto">
      <table className="w-full min-w-[980px] text-xs">
        <thead className="bg-eve-panel">
          <tr className="text-eve-dim">
            <th className="px-3 py-2 text-left">Item</th>
            <th className="px-3 py-2 text-right">ISK / slot</th>
            <th className="px-3 py-2 text-right">Score</th>
            <th className="px-3 py-2 text-right">Slots</th>
            <th className="px-3 py-2 text-right">Realized</th>
            <th className="px-3 py-2 text-right">Turnover / slot</th>
            <th className="px-3 py-2 text-right">Capital / slot</th>
            <th className="px-3 py-2 text-right">Avg entry</th>
            <th className="px-3 py-2 text-right">Avg exit</th>
            <th className="px-3 py-2 text-right">Win</th>
            <th className="px-3 py-2 text-right">Hold</th>
            <th className="px-3 py-2 text-left">Review</th>
          </tr>
        </thead>
        <tbody>
          {rows.slice(0, 30).map((row) => {
            const isProfit = (row.isk_per_slot ?? 0) >= 0;
            const barPct = Math.max(4, Math.min(100, Math.abs(row.isk_per_slot ?? 0) / maxAbs * 100));
            return (
              <tr key={`${row.type_id}-${row.slot_source}`} className="border-t border-eve-border/50 hover:bg-eve-panel/50">
                <td className="px-3 py-2 text-eve-text">
                  <div className="flex items-center gap-2">
                    <img
                      src={`https://images.evetech.net/types/${row.type_id}/icon?size=32`}
                      alt=""
                      className="w-5 h-5"
                    />
                    <div className="min-w-0">
                      <div className="truncate max-w-[220px]" title={row.type_name}>
                        {row.type_name || `Type #${row.type_id}`}
                      </div>
                      <div className="text-[10px] text-eve-dim">
                        {row.active_buy_orders} buy / {row.active_sell_orders} sell, {row.slot_source}
                      </div>
                    </div>
                  </div>
                </td>
                <td className="px-3 py-2 text-right">
                  <div className="flex items-center justify-end gap-2">
                    <div className="w-16 h-1.5 bg-eve-dark rounded-full overflow-hidden">
                      <div
                        className={`h-full rounded-full ${isProfit ? "bg-emerald-500" : "bg-red-500"}`}
                        style={{ width: `${barPct}%` }}
                      />
                    </div>
                    <span className={isProfit ? "text-eve-profit" : "text-eve-error"}>
                      {isProfit ? "+" : ""}{formatIsk(row.isk_per_slot ?? 0)}
                    </span>
                  </div>
                </td>
                <td className={`px-3 py-2 text-right ${(row.slot_efficiency_score ?? 0) >= 70 ? "text-eve-profit" : (row.slot_efficiency_score ?? 0) >= 45 ? "text-eve-accent" : "text-eve-error"}`}>
                  {(row.slot_efficiency_score ?? 0).toFixed(0)}
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">{row.order_slots}</td>
                <td className={`px-3 py-2 text-right ${(row.realized_pnl ?? 0) >= 0 ? "text-eve-profit" : "text-eve-error"}`}>
                  {(row.realized_pnl ?? 0) >= 0 ? "+" : ""}{formatIsk(row.realized_pnl ?? 0)}
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(row.turnover_per_slot ?? 0)}</td>
                <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(row.capital_per_slot ?? 0)}</td>
                <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(row.avg_entry_price ?? 0)}</td>
                <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(row.avg_exit_price ?? 0)}</td>
                <td className="px-3 py-2 text-right text-eve-dim">{(row.win_rate_pct ?? 0).toFixed(0)}%</td>
                <td className="px-3 py-2 text-right text-eve-dim">{(row.avg_holding_days ?? 0).toFixed(1)}d</td>
                <td className="px-3 py-2 text-left">
                  <span className={`inline-flex rounded-sm border px-2 py-0.5 text-[10px] uppercase tracking-wider ${
                    (row.slot_efficiency_score ?? 0) >= 70
                      ? "border-eve-profit/40 text-eve-profit bg-eve-profit/10"
                      : (row.slot_efficiency_score ?? 0) >= 45
                        ? "border-eve-accent/40 text-eve-accent bg-eve-accent/10"
                        : "border-eve-error/40 text-eve-error bg-eve-error/10"
                  }`}>
                    {row.review}
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {rows.length > 30 && (
        <div className="text-center text-eve-dim text-xs py-2 bg-eve-panel">
          +{rows.length - 30} more reviewed positions
        </div>
      )}
    </div>
  );
}

// --- P&L Stations Table ---

export function PnLStationsTable({
  stations,
  formatIsk,
  t,
}: {
  stations: StationPnL[];
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  if (stations.length === 0) {
    return <div className="text-center text-eve-dim text-xs py-4">{t("pnlNoData")}</div>;
  }

  const maxAbsPnl = Math.max(...stations.map((s) => Math.abs(s.net_pnl)), 1);

  return (
    <div className="border border-eve-border rounded-sm overflow-hidden">
      <table className="w-full text-xs">
        <thead className="bg-eve-panel">
          <tr className="text-eve-dim">
            <th className="px-3 py-2 text-left">{t("pnlStationName")}</th>
            <th className="px-3 py-2 text-right">{t("pnlStationPnl")}</th>
            <th className="px-3 py-2 text-right">{t("pnlStationBought")}</th>
            <th className="px-3 py-2 text-right">{t("pnlStationSold")}</th>
            <th className="px-3 py-2 text-right">{t("pnlStationTxns")}</th>
          </tr>
        </thead>
        <tbody>
          {stations.map((st) => {
            const isProfit = st.net_pnl >= 0;
            const barPct = (Math.abs(st.net_pnl) / maxAbsPnl) * 100;

            return (
              <tr key={st.location_id} className="border-t border-eve-border/50 hover:bg-eve-panel/50">
                <td className="px-3 py-2 text-eve-text max-w-[220px] truncate" title={st.location_name}>
                  {st.location_name || `#${st.location_id}`}
                </td>
                <td className="px-3 py-2 text-right">
                  <div className="flex items-center justify-end gap-2">
                    <div className="w-16 h-1.5 bg-eve-dark rounded-full overflow-hidden">
                      <div
                        className={`h-full rounded-full ${isProfit ? "bg-emerald-500" : "bg-red-500"}`}
                        style={{ width: `${barPct}%` }}
                      />
                    </div>
                    <span className={isProfit ? "text-eve-profit" : "text-eve-error"}>
                      {isProfit ? "+" : ""}{formatIsk(st.net_pnl)}
                    </span>
                  </div>
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(st.total_bought)}</td>
                <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(st.total_sold)}</td>
                <td className="px-3 py-2 text-right text-eve-dim">{st.transactions}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// --- P&L Ledger Table ---

export function PnLLedgerTable({
  ledger,
  formatIsk,
  t,
}: {
  ledger: PortfolioPnL["ledger"];
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  if (!ledger || ledger.length === 0) {
    return <div className="text-center text-eve-dim text-xs py-4">{t("pnlNoData")}</div>;
  }

  return (
    <div className="border border-eve-border rounded-sm overflow-hidden">
      <table className="w-full text-xs">
        <thead className="bg-eve-panel">
          <tr className="text-eve-dim">
            <th className="px-2 py-1.5 text-left">{t("pnlLedgerDate")}</th>
            <th className="px-2 py-1.5 text-left">{t("pnlLedgerItem")}</th>
            <th className="px-2 py-1.5 text-right">{t("pnlLedgerQty")}</th>
            <th className="px-2 py-1.5 text-right">{t("pnlLedgerBuy")}</th>
            <th className="px-2 py-1.5 text-right">{t("pnlLedgerSell")}</th>
            <th className="px-2 py-1.5 text-right">{t("pnlLedgerHold")}</th>
            <th className="px-2 py-1.5 text-right">{t("pnlLedgerPnl")}</th>
            <th className="px-2 py-1.5 text-right">{t("pnlLedgerMargin")}</th>
          </tr>
        </thead>
        <tbody>
          {ledger.slice(0, 120).map((row, idx) => {
            const isProfit = (row.realized_pnl ?? 0) >= 0;
            return (
              <tr key={`${row.sell_transaction_id}-${row.buy_transaction_id}-${idx}`} className="border-t border-eve-border/50 hover:bg-eve-panel/50">
                <td className="px-2 py-1.5 text-eve-dim">{(row.sell_date ?? "").slice(0, 10)}</td>
                <td className="px-2 py-1.5 text-eve-text truncate max-w-[220px]" title={row.type_name}>
                  {row.type_name || `#${row.type_id}`}
                </td>
                <td className="px-2 py-1.5 text-right text-eve-dim">{(row.quantity ?? 0).toLocaleString()}</td>
                <td className="px-2 py-1.5 text-right text-eve-dim">{formatIsk(row.buy_total ?? 0)}</td>
                <td className="px-2 py-1.5 text-right text-eve-dim">{formatIsk(row.sell_total ?? 0)}</td>
                <td className="px-2 py-1.5 text-right text-eve-dim">{row.holding_days ?? 0}d</td>
                <td className={`px-2 py-1.5 text-right ${isProfit ? "text-eve-profit" : "text-eve-error"}`}>
                  {isProfit ? "+" : ""}{formatIsk(row.realized_pnl ?? 0)}
                </td>
                <td className={`px-2 py-1.5 text-right ${isProfit ? "text-eve-profit" : "text-eve-error"}`}>
                  {(row.margin_percent ?? 0).toFixed(1)}%
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {ledger.length > 120 && (
        <div className="text-center text-eve-dim text-xs py-2 bg-eve-panel">
          {t("andMore", { count: ledger.length - 120 })}
        </div>
      )}
    </div>
  );
}
