import { useEffect, useState } from "react";
import { getPortfolioPnL, type CharacterScope } from "../../lib/api";
import { type TranslationKey } from "../../lib/i18n";
import type { PortfolioPnL } from "../../lib/types";
import { StatCard } from "./shared";
import {
  PnLChart,
  PnLItemsTable,
  PnLLedgerTable,
  PnLOpenPositionsTable,
  PnLStationsTable,
  SlotEfficiencyTable,
} from "../journal/PnLPrimitives";
type PnLPeriod = 7 | 30 | 90 | 180;

interface PnLTabProps {
  formatIsk: (v: number) => string;
  characterScope: CharacterScope;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}

export function PnLTab({ formatIsk, characterScope, t }: PnLTabProps) {
  const [period, setPeriod] = useState<PnLPeriod>(30);
  const [data, setData] = useState<PortfolioPnL | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [salesTax, setSalesTax] = useState(8);
  const [brokerFee, setBrokerFee] = useState(1);
  const [chartMode, setChartMode] = useState<"daily" | "cumulative" | "drawdown">("daily");
  const [itemView, setItemView] = useState<"profit" | "loss">("profit");
  const [bottomView, setBottomView] = useState<"slots" | "items" | "stations">("slots");

  useEffect(() => {
    setLoading(true);
    setError(null);
    getPortfolioPnL(period, { salesTax, brokerFee, ledgerLimit: 500, characterId: characterScope })
      .then(setData)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, [period, salesTax, brokerFee, characterScope]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-eve-dim text-xs">
        <span className="inline-block w-4 h-4 border-2 border-eve-accent/40 border-t-eve-accent rounded-full animate-spin mr-2" />
        {t("loading")}...
      </div>
    );
  }

  if (error) {
    return <div className="flex items-center justify-center h-full text-eve-error text-xs">{error}</div>;
  }

  if (!data || (data.daily_pnl.length === 0 && (data.slot_efficiency?.length ?? 0) === 0 && (data.open_positions?.length ?? 0) === 0)) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-eve-dim text-xs space-y-2">
        <div>{t("pnlNoData")}</div>
        <div className="text-[10px] max-w-md text-center">{t("pnlNoDataHint")}</div>
      </div>
    );
  }

  const { summary } = data;
  const slotRows = data.slot_efficiency ?? [];
  const activeSlotCount = slotRows.reduce((sum, row) => sum + (row.active_orders ?? 0), 0);
  const bestSlot = slotRows[0];

  // Separate top items into profit and loss
  const profitItems = data.top_items.filter((item) => item.net_pnl > 0).sort((a, b) => b.net_pnl - a.net_pnl);
  const lossItems = data.top_items.filter((item) => item.net_pnl < 0).sort((a, b) => a.net_pnl - b.net_pnl);

  return (
    <div className="space-y-4">
      {/* Period selector */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-xs text-eve-dim uppercase tracking-wider">{t("pnlTitle")}</div>
        <div className="flex items-center gap-2 flex-wrap">
          <div className="flex gap-1">
            {([7, 30, 90, 180] as PnLPeriod[]).map((p) => (
              <button
                key={p}
                onClick={() => setPeriod(p)}
                className={`px-2.5 py-1 text-[10px] rounded-sm border transition-colors ${
                  period === p
                    ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                    : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
                }`}
              >
                {t(`pnlPeriod${p}d` as TranslationKey)}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-1 text-[10px]">
            <span className="text-eve-dim">{t("pnlSalesTax")}</span>
            <input
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={salesTax}
              onChange={(e) => setSalesTax(parseFloat(e.target.value) || 0)}
              className="w-14 px-1 py-0.5 rounded-sm border border-eve-border bg-eve-dark text-eve-text"
            />
          </div>
          <div className="flex items-center gap-1 text-[10px]">
            <span className="text-eve-dim">{t("pnlBrokerFee")}</span>
            <input
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={brokerFee}
              onChange={(e) => setBrokerFee(parseFloat(e.target.value) || 0)}
              className="w-14 px-1 py-0.5 rounded-sm border border-eve-border bg-eve-dark text-eve-text"
            />
          </div>
        </div>
      </div>

      {/* Summary cards row 1: P&L, ROI, Win Rate */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label={t("pnlTotalPnl")}
          value={`${summary.total_pnl >= 0 ? "+" : ""}${formatIsk(summary.total_pnl)} ISK`}
          color={summary.total_pnl >= 0 ? "text-eve-profit" : "text-eve-error"}
          large
        />
        <StatCard
          label={t("pnlROI")}
          value={`${summary.roi_percent >= 0 ? "+" : ""}${summary.roi_percent.toFixed(1)}%`}
          color={summary.roi_percent >= 0 ? "text-eve-profit" : "text-eve-error"}
        />
        <StatCard
          label={t("pnlWinRate")}
          value={`${summary.win_rate.toFixed(0)}%`}
          subvalue={`${summary.profitable_days}/${summary.total_days} ${t("pnlProfitableDays").toLowerCase()}`}
          color="text-eve-accent"
        />
        <StatCard
          label={t("pnlAvgDaily")}
          value={`${summary.avg_daily_pnl >= 0 ? "+" : ""}${formatIsk(summary.avg_daily_pnl)} ISK`}
          color={summary.avg_daily_pnl >= 0 ? "text-eve-profit" : "text-eve-error"}
        />
      </div>

      {/* Summary cards row 2: Best day, Worst day, Volume */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label={t("pnlBestDay")}
          value={`+${formatIsk(summary.best_day_pnl)} ISK`}
          subvalue={summary.best_day_date}
          color="text-eve-profit"
        />
        <StatCard
          label={t("pnlWorstDay")}
          value={`${formatIsk(summary.worst_day_pnl)} ISK`}
          subvalue={summary.worst_day_date}
          color="text-eve-error"
        />
        <StatCard
          label={t("pnlTotalBought")}
          value={`${formatIsk(summary.total_bought)} ISK`}
        />
        <StatCard
          label={t("pnlTotalSold")}
          value={`${formatIsk(summary.total_sold)} ISK`}
        />
      </div>

      {/* Summary cards row 3: Sharpe, Max DD, Profit Factor, Expectancy */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label={t("pnlSharpeRatio")}
          value={(summary.sharpe_ratio ?? 0) !== 0 ? (summary.sharpe_ratio ?? 0).toFixed(2) : "—"}
          subvalue={t("pnlSharpeHint")}
          color={(summary.sharpe_ratio ?? 0) > 1 ? "text-eve-profit" : (summary.sharpe_ratio ?? 0) > 0 ? "text-eve-accent" : "text-eve-error"}
        />
        <StatCard
          label={t("pnlMaxDrawdown")}
          value={(summary.max_drawdown_isk ?? 0) > 0 ? `-${formatIsk(summary.max_drawdown_isk ?? 0)} ISK` : "—"}
          subvalue={(summary.max_drawdown_pct ?? 0) > 0 ? `-${(summary.max_drawdown_pct ?? 0).toFixed(1)}% (${summary.max_drawdown_days ?? 0}d)` : undefined}
          color="text-eve-error"
        />
        <StatCard
          label={t("pnlProfitFactor")}
          value={(summary.profit_factor ?? 0) > 0 ? (summary.profit_factor ?? 0).toFixed(2) : "—"}
          subvalue={t("pnlProfitFactorHint")}
          color={(summary.profit_factor ?? 0) >= 1.5 ? "text-eve-profit" : (summary.profit_factor ?? 0) >= 1 ? "text-eve-accent" : "text-eve-error"}
        />
        <StatCard
          label={t("pnlExpectancy")}
          value={`${(summary.expectancy_per_trade ?? 0) >= 0 ? "+" : ""}${formatIsk(summary.expectancy_per_trade ?? 0)} ISK`}
          subvalue={t("pnlExpectancyHint")}
          color={(summary.expectancy_per_trade ?? 0) >= 0 ? "text-eve-profit" : "text-eve-error"}
        />
      </div>

      {/* Ledger quality / matching stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label={t("pnlCoverageQty")}
          value={`${(data.coverage?.match_rate_qty_pct ?? 0).toFixed(1)}%`}
          subvalue={t("pnlCoverageHint")}
          color={(data.coverage?.match_rate_qty_pct ?? 0) >= 80 ? "text-eve-profit" : (data.coverage?.match_rate_qty_pct ?? 0) >= 50 ? "text-eve-accent" : "text-eve-error"}
        />
        <StatCard
          label={t("pnlMatchedSellQty")}
          value={(data.coverage?.matched_sell_qty ?? 0).toLocaleString()}
          subvalue={t("pnlTxns")}
        />
        <StatCard
          label={t("pnlUnmatchedSellQty")}
          value={(data.coverage?.unmatched_sell_qty ?? 0).toLocaleString()}
          subvalue={t("pnlCoverageHint")}
          color={(data.coverage?.unmatched_sell_qty ?? 0) > 0 ? "text-eve-warning" : "text-eve-dim"}
        />
        <StatCard
          label={t("pnlOpenCostBasis")}
          value={`${formatIsk(summary.open_cost_basis ?? 0)} ISK`}
          subvalue={`${summary.open_positions ?? 0} ${t("pnlOpenPositions").toLowerCase()}`}
        />
      </div>

      {/* Slot efficiency summary */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label="Active order slots"
          value={activeSlotCount.toLocaleString()}
          subvalue="Current market order slots used"
          color={activeSlotCount > 0 ? "text-eve-accent" : "text-eve-dim"}
        />
        <StatCard
          label="Best ISK / slot"
          value={bestSlot ? `${bestSlot.isk_per_slot >= 0 ? "+" : ""}${formatIsk(bestSlot.isk_per_slot)} ISK` : "--"}
          subvalue={bestSlot?.type_name || "No reviewed positions"}
          color={(bestSlot?.isk_per_slot ?? 0) >= 0 ? "text-eve-profit" : "text-eve-error"}
        />
        <StatCard
          label="Capital / slot"
          value={bestSlot ? `${formatIsk(bestSlot.capital_per_slot ?? 0)} ISK` : "--"}
          subvalue={bestSlot?.slot_source || "Active orders + inventory"}
        />
        <StatCard
          label="Slot score"
          value={bestSlot ? `${(bestSlot.slot_efficiency_score ?? 0).toFixed(0)}/100` : "--"}
          subvalue={bestSlot?.review || "Review order-slot efficiency"}
          color={(bestSlot?.slot_efficiency_score ?? 0) >= 70 ? "text-eve-profit" : (bestSlot?.slot_efficiency_score ?? 0) >= 45 ? "text-eve-accent" : "text-eve-error"}
        />
      </div>

      {/* Daily P&L Chart */}
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="flex items-center justify-between mb-3">
          <div className="text-[10px] text-eve-dim uppercase tracking-wider">
            {chartMode === "daily" ? t("pnlDailyChart") : chartMode === "cumulative" ? t("pnlCumulativeChart") : t("pnlDrawdownChart")}
          </div>
          <div className="flex gap-1">
            <button
              onClick={() => setChartMode("daily")}
              className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                chartMode === "daily"
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              {t("pnlDailyChart")}
            </button>
            <button
              onClick={() => setChartMode("cumulative")}
              className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                chartMode === "cumulative"
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              {t("pnlCumulativeChart")}
            </button>
            <button
              onClick={() => setChartMode("drawdown")}
              className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                chartMode === "drawdown"
                  ? "bg-red-500/20 border-red-500 text-red-400"
                  : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              {t("pnlDrawdownChart")}
            </button>
          </div>
        </div>
        <PnLChart data={data.daily_pnl} mode={chartMode} formatIsk={formatIsk} />
      </div>

      {/* Top Items / Station Breakdown */}
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="flex items-center justify-between mb-3">
          <div className="flex gap-2">
            <button
              onClick={() => setBottomView("slots")}
              className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                bottomView === "slots"
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              Slot Efficiency ({slotRows.length})
            </button>
            <button
              onClick={() => setBottomView("items")}
              className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                bottomView === "items"
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              {t("pnlTopItems")}
            </button>
            <button
              onClick={() => setBottomView("stations")}
              className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                bottomView === "stations"
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              {t("pnlStationBreakdown")} ({data.top_stations?.length ?? 0})
            </button>
          </div>
          {bottomView === "items" && (
            <div className="flex gap-1">
              <button
                onClick={() => setItemView("profit")}
                className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                  itemView === "profit"
                    ? "bg-emerald-500/20 border-emerald-500 text-emerald-400"
                    : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
                }`}
              >
                {t("pnlTopProfit")} ({profitItems.length})
              </button>
              <button
                onClick={() => setItemView("loss")}
                className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                  itemView === "loss"
                    ? "bg-red-500/20 border-red-500 text-red-400"
                    : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
                }`}
              >
                {t("pnlTopLoss")} ({lossItems.length})
              </button>
            </div>
          )}
        </div>
        {bottomView === "slots" ? (
          <SlotEfficiencyTable rows={slotRows} formatIsk={formatIsk} />
        ) : bottomView === "items" ? (
          <PnLItemsTable
            items={itemView === "profit" ? profitItems : lossItems}
            formatIsk={formatIsk}
            t={t}
          />
        ) : (
          <PnLStationsTable
            stations={data.top_stations ?? []}
            formatIsk={formatIsk}
            t={t}
          />
        )}
      </div>

      {/* Realized ledger */}
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-2">
          {t("pnlRealizedLedger")} ({data.ledger?.length ?? 0})
        </div>
        <PnLLedgerTable ledger={data.ledger ?? []} formatIsk={formatIsk} t={t} />
      </div>

      {/* Open positions */}
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-2">
          {t("pnlOpenPositions")} ({data.open_positions?.length ?? 0})
        </div>
        <PnLOpenPositionsTable positions={data.open_positions ?? []} formatIsk={formatIsk} t={t} />
      </div>
    </div>
  );
}
// --- Optimizer Tab ---

