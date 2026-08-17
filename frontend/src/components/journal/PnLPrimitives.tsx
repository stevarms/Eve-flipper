import { type TranslationKey } from "../../lib/i18n";
import type {
  ItemPnL,
  PortfolioPnL,
  PortfolioSlotEfficiency,
  StationPnL,
} from "../../lib/types";

// Shared P&L primitives — used by the character-popup Ledger tab and the
// main Trade Journal tab. Extracted from PnLTab.tsx so both surfaces render
// identical widgets without code duplication.

// --- P&L Bar Chart (CSS-based) ---

export function PnLChart({
  data,
  mode,
  formatIsk,
}: {
  data: PortfolioPnL["daily_pnl"];
  mode: "daily" | "cumulative" | "drawdown";
  formatIsk: (v: number) => string;
}) {
  if (data.length === 0) return null;

  const values = data.map((d) =>
    mode === "daily" ? d.net_pnl : mode === "cumulative" ? d.cumulative_pnl : (d.drawdown_pct ?? 0)
  );
  const maxAbs = Math.max(...values.map(Math.abs), 1);

  // For cumulative mode, compute range from min to max.
  const maxVal = Math.max(...values, 0);
  const minVal = Math.min(...values, 0);
  const range = maxVal - minVal || 1;

  // Show fewer bars if too many days
  const maxBars = 60;
  const step = data.length > maxBars ? Math.ceil(data.length / maxBars) : 1;
  const sampled = step > 1 ? data.filter((_, i) => i % step === 0) : data;
  const sampledValues = sampled.map((d) => (mode === "daily" ? d.net_pnl : d.cumulative_pnl));

  const barWidth = Math.max(2, Math.min(12, Math.floor(680 / sampled.length) - 1));
  const chartHeight = 120;
  const midY = chartHeight / 2;

  // For cumulative mode: compute the zero-line position.
  // The chart spans from minVal at bottom to maxVal at top.
  // Zero line is at (1 - (0 - minVal) / range) * chartHeight from top.
  const cumulativeZeroY = range > 0 ? (1 - (0 - minVal) / range) * chartHeight : chartHeight;

  return (
    <div className="relative">
      {/* Chart area */}
      <div className="relative" style={{ height: chartHeight }}>
        {mode === "drawdown" ? (
          /* Drawdown mode: all bars go downward from top (0%) */
          <div className="flex items-start justify-center gap-px h-full">
            {sampled.map((entry, i) => {
              const val = sampledValues[i]; // always <= 0
              const absMin = Math.max(...values.map((v) => Math.abs(v)), 1);
              const barH = Math.max(1, (Math.abs(val) / absMin) * (chartHeight - 8));
              return (
                <div
                  key={entry.date}
                  className="relative group"
                  style={{ width: barWidth, height: chartHeight }}
                >
                  <div
                    className="bg-red-500/60 hover:bg-red-400/80 transition-colors rounded-b-[1px]"
                    style={{ width: barWidth, height: barH }}
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
        ) : mode === "daily" ? (
          /* Daily mode: bars grow from the center line */
          <div className="flex items-end justify-center gap-px h-full">
            {sampled.map((entry, i) => {
              const val = sampledValues[i];
              const pct = Math.abs(val) / maxAbs;
              const barH = Math.max(1, pct * (chartHeight / 2 - 4));
              const isPositive = val >= 0;

              return (
                <div
                  key={entry.date}
                  className="relative group flex flex-col items-center"
                  style={{ width: barWidth, height: chartHeight }}
                >
                  {/* Top half */}
                  <div className="flex-1 flex items-end justify-center">
                    {isPositive && (
                      <div
                        className="rounded-t-[1px] bg-emerald-500/80 hover:bg-emerald-400 transition-colors"
                        style={{ width: barWidth, height: barH }}
                      />
                    )}
                  </div>
                  {/* Bottom half */}
                  <div className="flex-1 flex items-start justify-center">
                    {!isPositive && (
                      <div
                        className="rounded-b-[1px] bg-red-500/80 hover:bg-red-400 transition-colors"
                        style={{ width: barWidth, height: barH }}
                      />
                    )}
                  </div>

                  {/* Tooltip */}
                  <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-10 pointer-events-none">
                    <div className="bg-eve-dark border border-eve-border rounded px-2 py-1 text-[10px] whitespace-nowrap shadow-lg">
                      <div className="text-eve-dim">{entry.date}</div>
                      <div className={isPositive ? "text-emerald-400" : "text-red-400"}>
                        {val >= 0 ? "+" : ""}{formatIsk(val)} ISK
                      </div>
                      <div className="text-eve-dim">{entry.transactions} txns</div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          /* Cumulative mode: bars grow from the zero line, both up and down */
          <div className="flex items-end justify-center gap-px h-full">
            {sampled.map((entry, i) => {
              const val = sampledValues[i];
              const isPositive = val >= 0;

              const barH = Math.max(1, (Math.abs(val) / range) * chartHeight);
              const barTop = isPositive ? cumulativeZeroY - barH : cumulativeZeroY;

              return (
                <div
                  key={entry.date}
                  className="relative group"
                  style={{ width: barWidth, height: chartHeight }}
                >
                  <div
                    className={`absolute transition-colors ${
                      isPositive
                        ? "bg-emerald-500/80 hover:bg-emerald-400 rounded-t-[1px]"
                        : "bg-red-500/80 hover:bg-red-400 rounded-b-[1px]"
                    }`}
                    style={{
                      width: barWidth,
                      height: barH,
                      top: barTop,
                    }}
                  />

                  {/* Tooltip */}
                  <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-10 pointer-events-none">
                    <div className="bg-eve-dark border border-eve-border rounded px-2 py-1 text-[10px] whitespace-nowrap shadow-lg">
                      <div className="text-eve-dim">{entry.date}</div>
                      <div className={isPositive ? "text-emerald-400" : "text-red-400"}>
                        {val >= 0 ? "+" : ""}{formatIsk(val)} ISK
                      </div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}

        {/* Zero line */}
        {mode === "daily" ? (
          <div
            className="absolute left-0 right-0 border-t border-eve-border/50"
            style={{ top: midY }}
          />
        ) : (
          <div
            className="absolute left-0 right-0 border-t border-eve-border/50"
            style={{ top: cumulativeZeroY }}
          />
        )}
      </div>

      {/* X-axis labels */}
      <div className="flex justify-between mt-1 px-1">
        <span className="text-[9px] text-eve-dim">{sampled[0]?.date.slice(5)}</span>
        {sampled.length > 2 && (
          <span className="text-[9px] text-eve-dim">{sampled[Math.floor(sampled.length / 2)]?.date.slice(5)}</span>
        )}
        <span className="text-[9px] text-eve-dim">{sampled[sampled.length - 1]?.date.slice(5)}</span>
      </div>

      {/* Y-axis labels */}
      <div className="absolute left-0 top-0 bottom-0 flex flex-col justify-between pointer-events-none" style={{ width: 0 }}>
        <span className="text-[9px] text-eve-dim -translate-x-full pr-1">
          {mode === "drawdown" ? "0%" : `+${formatIsk(mode === "daily" ? maxAbs : maxVal)}`}
        </span>
        <span className="text-[9px] text-eve-dim -translate-x-full pr-1">
          {mode === "drawdown" ? "" : "0"}
        </span>
        <span className="text-[9px] text-eve-dim -translate-x-full pr-1">
          {mode === "drawdown"
            ? `${Math.min(...values).toFixed(1)}%`
            : mode === "daily" ? `-${formatIsk(maxAbs)}` : `${formatIsk(minVal)}`}
        </span>
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

// --- P&L Items Table ---

export function PnLItemsTable({
  items,
  formatIsk,
  t,
}: {
  items: ItemPnL[];
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  if (items.length === 0) {
    return <div className="text-center text-eve-dim text-xs py-4">{t("pnlNoData")}</div>;
  }

  const maxAbsPnl = Math.max(...items.map((i) => Math.abs(i.net_pnl)), 1);

  return (
    <div className="border border-eve-border rounded-sm overflow-hidden">
      <table className="w-full text-xs">
        <thead className="bg-eve-panel">
          <tr className="text-eve-dim">
            <th className="px-3 py-2 text-left">{t("pnlItemName")}</th>
            <th className="px-3 py-2 text-right">{t("pnlItemPnl")}</th>
            <th className="px-3 py-2 text-right">{t("pnlItemMargin")}</th>
            <th className="px-3 py-2 text-right">{t("pnlItemBought")}</th>
            <th className="px-3 py-2 text-right">{t("pnlItemSold")}</th>
            <th className="px-3 py-2 text-right">{t("pnlItemTxns")}</th>
          </tr>
        </thead>
        <tbody>
          {items.slice(0, 20).map((item) => {
            const isProfit = item.net_pnl >= 0;
            const barPct = (Math.abs(item.net_pnl) / maxAbsPnl) * 100;

            return (
              <tr key={item.type_id} className="border-t border-eve-border/50 hover:bg-eve-panel/50">
                <td className="px-3 py-2 text-eve-text">
                  <div className="flex items-center gap-2">
                    <img
                      src={`https://images.evetech.net/types/${item.type_id}/icon?size=32`}
                      alt=""
                      className="w-5 h-5"
                    />
                    <span className="truncate max-w-[180px]">{item.type_name || `Type #${item.type_id}`}</span>
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
                      {isProfit ? "+" : ""}{formatIsk(item.net_pnl)}
                    </span>
                  </div>
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">
                  {item.margin_percent !== 0 ? `${item.margin_percent.toFixed(1)}%` : "—"}
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">
                  {formatIsk(item.total_bought)}
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">
                  {formatIsk(item.total_sold)}
                </td>
                <td className="px-3 py-2 text-right text-eve-dim">
                  {item.transactions}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {items.length > 20 && (
        <div className="text-center text-eve-dim text-xs py-2 bg-eve-panel">
          {t("andMore", { count: items.length - 20 })}
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

// --- P&L Open Positions Table ---

export function PnLOpenPositionsTable({
  positions,
  formatIsk,
  t,
}: {
  positions: PortfolioPnL["open_positions"];
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  if (!positions || positions.length === 0) {
    return <div className="text-center text-eve-dim text-xs py-4">{t("pnlNoData")}</div>;
  }

  return (
    <div className="border border-eve-border rounded-sm overflow-hidden">
      <table className="w-full text-xs">
        <thead className="bg-eve-panel">
          <tr className="text-eve-dim">
            <th className="px-3 py-2 text-left">{t("pnlOpenItem")}</th>
            <th className="px-3 py-2 text-right">{t("pnlOpenQty")}</th>
            <th className="px-3 py-2 text-right">{t("pnlOpenAvgCost")}</th>
            <th className="px-3 py-2 text-right">{t("pnlOpenCostBasis")}</th>
            <th className="px-3 py-2 text-right">{t("pnlOpenOldest")}</th>
          </tr>
        </thead>
        <tbody>
          {positions.map((row) => (
            <tr key={`${row.type_id}-${row.location_id}`} className="border-t border-eve-border/50 hover:bg-eve-panel/50">
              <td className="px-3 py-2 text-eve-text truncate max-w-[260px]" title={row.type_name}>
                {row.type_name || `#${row.type_id}`}
              </td>
              <td className="px-3 py-2 text-right text-eve-dim">{(row.quantity ?? 0).toLocaleString()}</td>
              <td className="px-3 py-2 text-right text-eve-dim">{formatIsk(row.avg_cost ?? 0)}</td>
              <td className="px-3 py-2 text-right text-eve-text">{formatIsk(row.cost_basis ?? 0)}</td>
              <td className="px-3 py-2 text-right text-eve-dim">{row.oldest_lot_date || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
