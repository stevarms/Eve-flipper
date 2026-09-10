import { useEffect, useMemo, useRef, useState } from "react";
import {
  getJournalAnalytics,
  type JournalAnalyticsSource,
  type JournalFIFOMode,
  type WalletScope,
} from "../../lib/api";
import { useI18n, type TranslationKey } from "../../lib/i18n";
import type { PortfolioPnL } from "../../lib/types";
import { StatCard } from "../character-popup/shared";
import {
  PnLChart,
  PnLItemsTable,
  PnLLedgerTable,
  PnLStationsTable,
  SlotEfficiencyTable,
} from "./PnLPrimitives";
import { LoadingBlock } from "@/components/ui/LoadingBlock";

// JournalAnalyticsView — the deep-dive half of the Trade Journal tab: risk
// statistics, the drawdown chart, per-item / per-station / slot-efficiency
// tables and the realized ledger.
//
// This was its own top-level "P&L" tab, computing profit from a second FIFO
// engine that could not see industry jobs — so a built-and-sold item showed up
// there as a zero-cost windfall while the Journal priced it correctly. Both
// now read one matcher, and the two views are the same numbers at two depths.
//
// `source` filters the ledger *before* any statistic is computed, so a
// Manufacturing view's Sharpe ratio describes manufacturing rather than
// manufacturing's share of a combined figure.

interface Props {
  scope: WalletScope;
  period: number | "all";
  fifoMode: JournalFIFOMode;
  source: JournalAnalyticsSource;
  /** Explicit rate pair, or null to use the resolved profile. */
  feeOverride: { salesTax: number; brokerFee: number } | null;
  /** Bumped by the parent after a sync so the analytics refetch too. */
  reloadToken?: number;
  formatIsk: (v: number) => string;
  /** Jumps to Assets → Positions, which owns open positions now. */
  onOpenPositions?: () => void;
}

export function JournalAnalyticsView({
  scope,
  period,
  fifoMode,
  source,
  feeOverride,
  reloadToken,
  formatIsk,
  onOpenPositions,
}: Props) {
  const { t } = useI18n();
  const [data, setData] = useState<PortfolioPnL | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [chartMode, setChartMode] = useState<"daily" | "cumulative" | "drawdown">("cumulative");
  const [itemView, setItemView] = useState<"profit" | "loss">("profit");
  const [bottomView, setBottomView] = useState<"slots" | "items" | "stations">("items");

  const controllerRef = useRef<AbortController | null>(null);
  useEffect(() => {
    controllerRef.current?.abort();
    const c = new AbortController();
    controllerRef.current = c;
    setLoading(true);
    setError(null);
    getJournalAnalytics({
      scope,
      days: period,
      fifoMode,
      source,
      ledgerLimit: 500,
      salesTax: feeOverride?.salesTax,
      brokerFee: feeOverride?.brokerFee,
    })
      .then((resp) => {
        if (c.signal.aborted) return;
        setData(resp.analytics);
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!c.signal.aborted) setLoading(false);
      });
    return () => c.abort();
  }, [scope, period, fifoMode, source, feeOverride, reloadToken]);

  const slotRows = useMemo(() => data?.slot_efficiency ?? [], [data]);
  const { profitItems, lossItems } = useMemo(() => {
    const items = data?.top_items ?? [];
    return {
      profitItems: items.filter((i) => i.net_pnl > 0).sort((a, b) => b.net_pnl - a.net_pnl),
      lossItems: items.filter((i) => i.net_pnl < 0).sort((a, b) => a.net_pnl - b.net_pnl),
    };
  }, [data]);

  if (loading && !data) return <LoadingBlock label={`${t("loading")}…`} fill />;
  if (error) {
    return (
      <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
        {error}
      </div>
    );
  }
  if (!data || (data.daily_pnl.length === 0 && (data.ledger?.length ?? 0) === 0)) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-eve-dim text-xs space-y-2">
        <div>{t("pnlNoData")}</div>
        <div className="text-[10px] max-w-md text-center">{t("pnlNoDataHint")}</div>
      </div>
    );
  }

  const { summary } = data;
  const activeSlotCount = slotRows.reduce((sum, row) => sum + (row.active_orders ?? 0), 0);
  const bestSlot = slotRows[0];

  return (
    <div className="space-y-3">
      {/* Headline: what the window earned, and how reliably. */}
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
        <StatCard label={t("pnlTotalBought")} value={`${formatIsk(summary.total_bought)} ISK`} />
        <StatCard label={t("pnlTotalSold")} value={`${formatIsk(summary.total_sold)} ISK`} />
      </div>

      {/* Risk statistics. */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label={t("pnlSharpeRatio")}
          value={(summary.sharpe_ratio ?? 0) !== 0 ? (summary.sharpe_ratio ?? 0).toFixed(2) : "—"}
          subvalue={t("pnlSharpeHint")}
          color={
            (summary.sharpe_ratio ?? 0) > 1
              ? "text-eve-profit"
              : (summary.sharpe_ratio ?? 0) > 0
                ? "text-eve-accent"
                : "text-eve-error"
          }
        />
        <StatCard
          label={t("pnlMaxDrawdown")}
          value={(summary.max_drawdown_isk ?? 0) > 0 ? `-${formatIsk(summary.max_drawdown_isk ?? 0)} ISK` : "—"}
          subvalue={
            (summary.max_drawdown_pct ?? 0) > 0
              ? `-${(summary.max_drawdown_pct ?? 0).toFixed(1)}% (${summary.max_drawdown_days ?? 0}d)`
              : undefined
          }
          color="text-eve-error"
        />
        <StatCard
          label={t("pnlProfitFactor")}
          value={(summary.profit_factor ?? 0) > 0 ? (summary.profit_factor ?? 0).toFixed(2) : "—"}
          subvalue={t("pnlProfitFactorHint")}
          color={
            (summary.profit_factor ?? 0) >= 1.5
              ? "text-eve-profit"
              : (summary.profit_factor ?? 0) >= 1
                ? "text-eve-accent"
                : "text-eve-error"
          }
        />
        <StatCard
          label={t("pnlExpectancy")}
          value={`${(summary.expectancy_per_trade ?? 0) >= 0 ? "+" : ""}${formatIsk(summary.expectancy_per_trade ?? 0)} ISK`}
          subvalue={t("pnlExpectancyHint")}
          color={(summary.expectancy_per_trade ?? 0) >= 0 ? "text-eve-profit" : "text-eve-error"}
        />
      </div>

      {/* How much of the window the matcher could actually account for. */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          label={t("pnlCoverageQty")}
          value={`${(data.coverage?.match_rate_qty_pct ?? 0).toFixed(1)}%`}
          subvalue={t("pnlCoverageHint")}
          color={
            (data.coverage?.match_rate_qty_pct ?? 0) >= 80
              ? "text-eve-profit"
              : (data.coverage?.match_rate_qty_pct ?? 0) >= 50
                ? "text-eve-accent"
                : "text-eve-error"
          }
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

      {/* Order-slot economics — only meaningful while live orders exist. */}
      {slotRows.length > 0 && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <StatCard
            label={t("journalSlotActive")}
            value={activeSlotCount.toLocaleString()}
            subvalue={t("journalSlotActiveHint")}
            color={activeSlotCount > 0 ? "text-eve-accent" : "text-eve-dim"}
          />
          <StatCard
            label={t("journalSlotBestIsk")}
            value={bestSlot ? `${bestSlot.isk_per_slot >= 0 ? "+" : ""}${formatIsk(bestSlot.isk_per_slot)} ISK` : "—"}
            subvalue={bestSlot?.type_name || t("journalSlotNoPositions")}
            color={(bestSlot?.isk_per_slot ?? 0) >= 0 ? "text-eve-profit" : "text-eve-error"}
          />
          <StatCard
            label={t("journalSlotCapital")}
            value={bestSlot ? `${formatIsk(bestSlot.capital_per_slot ?? 0)} ISK` : "—"}
            subvalue={bestSlot?.slot_source || t("journalSlotCapitalHint")}
          />
          <StatCard
            label={t("journalSlotScore")}
            value={bestSlot ? `${(bestSlot.slot_efficiency_score ?? 0).toFixed(0)}/100` : "—"}
            subvalue={bestSlot?.review || t("journalSlotScoreHint")}
            color={
              (bestSlot?.slot_efficiency_score ?? 0) >= 70
                ? "text-eve-profit"
                : (bestSlot?.slot_efficiency_score ?? 0) >= 45
                  ? "text-eve-accent"
                  : "text-eve-error"
            }
          />
        </div>
      )}

      {/* Chart. Drawdown is the mode the Summary view has no answer for. */}
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="flex items-center justify-between mb-3">
          <div className="text-[10px] text-eve-dim uppercase tracking-wider">
            {chartMode === "daily"
              ? t("pnlDailyChart")
              : chartMode === "cumulative"
                ? t("pnlCumulativeChart")
                : t("pnlDrawdownChart")}
          </div>
          <div className="flex gap-1">
            {(["daily", "cumulative", "drawdown"] as const).map((mode) => (
              <ModeBtn
                key={mode}
                active={chartMode === mode}
                danger={mode === "drawdown"}
                label={t(
                  (mode === "daily"
                    ? "pnlDailyChart"
                    : mode === "cumulative"
                      ? "pnlCumulativeChart"
                      : "pnlDrawdownChart") as TranslationKey,
                )}
                onClick={() => setChartMode(mode)}
              />
            ))}
          </div>
        </div>
        <PnLChart data={data.daily_pnl} mode={chartMode} formatIsk={formatIsk} />
      </div>

      {/* Where the profit came from: items, stations, or order slots. */}
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
          <div className="flex gap-2">
            <ModeBtn
              active={bottomView === "items"}
              label={t("pnlTopItems")}
              onClick={() => setBottomView("items")}
            />
            <ModeBtn
              active={bottomView === "stations"}
              label={`${t("pnlStationBreakdown")} (${data.top_stations?.length ?? 0})`}
              onClick={() => setBottomView("stations")}
            />
            <ModeBtn
              active={bottomView === "slots"}
              label={`${t("journalSlotEfficiency")} (${slotRows.length})`}
              onClick={() => setBottomView("slots")}
            />
          </div>
          {bottomView === "items" && (
            <div className="flex gap-1">
              <ModeBtn
                active={itemView === "profit"}
                label={`${t("pnlTopProfit")} (${profitItems.length})`}
                onClick={() => setItemView("profit")}
              />
              <ModeBtn
                active={itemView === "loss"}
                danger
                label={`${t("pnlTopLoss")} (${lossItems.length})`}
                onClick={() => setItemView("loss")}
              />
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
          <PnLStationsTable stations={data.top_stations ?? []} formatIsk={formatIsk} t={t} />
        )}
      </div>

      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-2">
          {t("pnlRealizedLedger")} ({data.ledger?.length ?? 0})
        </div>
        <PnLLedgerTable ledger={data.ledger ?? []} formatIsk={formatIsk} t={t} />
      </div>

      {/* Open positions live in Assets → Positions, which prices them live and
          nets out fees. Two tables answering that question differently was the
          confusing part; this one is the pointer. */}
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-sm border border-eve-border bg-eve-panel p-3">
        <div className="min-w-0">
          <div className="mb-1 text-[10px] uppercase tracking-wider text-eve-dim">
            {t("pnlOpenPositions")} ({data.open_positions?.length ?? 0})
          </div>
          <p className="text-xs text-eve-dim">{t("pnlOpenPositionsMoved")}</p>
        </div>
        {onOpenPositions && (
          <button
            type="button"
            onClick={onOpenPositions}
            className="rounded-sm border border-eve-accent/60 bg-eve-accent/10 px-3 py-1.5 text-xs text-eve-accent transition-colors hover:bg-eve-accent/20"
          >
            {t("pnlOpenPositionsGo")}
          </button>
        )}
      </div>
    </div>
  );
}

/** The small segmented-control button this view uses everywhere. */
function ModeBtn({
  active,
  label,
  onClick,
  danger = false,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
  danger?: boolean;
}) {
  const activeClass = danger
    ? "bg-red-500/20 border-red-500 text-red-400"
    : "bg-eve-accent/20 border-eve-accent text-eve-accent";
  return (
    <button
      type="button"
      onClick={onClick}
      className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
        active ? activeClass : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
      }`}
    >
      {label}
    </button>
  );
}
