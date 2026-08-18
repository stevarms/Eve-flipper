import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getJournalByType,
  getJournalLinkCandidates,
  getJournalLots,
  getJournalSummary,
  linkJournalJob,
  syncTradeJournal,
  type JournalByTypeRow,
  type JournalFIFOMode,
  type JournalLinkCandidate,
  type JournalLot,
  type JournalManufacturingLot,
  type JournalSummaryResponse,
  type JournalSyncResponse,
  type WalletScope,
} from "../lib/api";
import { useI18n, type TranslationKey } from "../lib/i18n";
import { PnLChart } from "./journal/PnLPrimitives";

// TradeJournal.tsx — main-tab realization of the Eve-Tycoon-style profit
// tracker. Aggregates trading + manufacturing P&L across every authorized
// wallet (character + corp division), with three toggleable KPI tiles, a
// unified cumulative-profit chart, a per-item table, and a per-lot drawer
// that shows the cross-wallet FIFO trace. See the plan file for the full
// design (parallel-wibbling-abelson.md).

interface Props {
  isLoggedIn: boolean;
  /** Set by the parent when the user clicks the ProfitPill so this tab
   *  can trigger a fresh summary fetch on activation. */
  visitToken?: number;
}

type PeriodPreset = 7 | 30 | 90 | "all";

// Local alias for the PnLChart data shape (avoids re-exporting DailyPnLEntry
// from lib/types just for this file).
interface DailyEntryLike {
  date: string;
  buy_total: number;
  sell_total: number;
  net_pnl: number;
  cumulative_pnl: number;
  drawdown_pct: number;
  transactions: number;
}

const DEFAULT_PERIOD: PeriodPreset = 30;
const FIFO_STORAGE_KEY = "trade_journal.fifo_mode";

function formatIsk(v: number): string {
  const abs = Math.abs(v);
  if (abs >= 1e12) return `${(v / 1e12).toFixed(2)}T`;
  if (abs >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (abs >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (abs >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

function formatIskSigned(v: number): string {
  const sign = v >= 0 ? "+" : "";
  return `${sign}${formatIsk(v)}`;
}

function humanTimeSince(iso: string | undefined): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const ms = Date.now() - t;
  const min = Math.floor(ms / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export function TradeJournal({ isLoggedIn, visitToken }: Props) {
  const { t } = useI18n();
  const [period, setPeriod] = useState<PeriodPreset>(DEFAULT_PERIOD);
  const [fifoMode, setFifoMode] = useState<JournalFIFOMode>(() => {
    if (typeof window === "undefined") return "strict_date";
    const raw = window.localStorage.getItem(FIFO_STORAGE_KEY);
    if (raw === "trade_first" || raw === "manufacture_first" || raw === "strict_date") return raw;
    return "strict_date";
  });
  const setPersistedFifoMode = (m: JournalFIFOMode) => {
    setFifoMode(m);
    if (typeof window !== "undefined") window.localStorage.setItem(FIFO_STORAGE_KEY, m);
  };

  // v1: pool everything. The scope-picker UI is a v1.5 (per the plan the
  // grouped chip UI is stubbed for now — pass include_all: true and let
  // the user filter via a future dropdown).
  const scope = useMemo<WalletScope>(() => ({ include_all: true }), []);

  const [summary, setSummary] = useState<JournalSummaryResponse | null>(null);
  const [byType, setByType] = useState<JournalByTypeRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastSyncResp, setLastSyncResp] = useState<JournalSyncResponse | null>(null);

  // Drawer state
  const [drawerTypeID, setDrawerTypeID] = useState<number | null>(null);
  const [drawerTypeName, setDrawerTypeName] = useState<string>("");
  const [drawerLots, setDrawerLots] = useState<JournalLot[]>([]);
  const [drawerMfg, setDrawerMfg] = useState<JournalManufacturingLot[]>([]);
  const [drawerLoading, setDrawerLoading] = useState(false);

  // Chart series toggles
  const [showTrading, setShowTrading] = useState(true);
  const [showMfg, setShowMfg] = useState(true);
  const [showCombined, setShowCombined] = useState(true);

  // Table sort
  type SortKey =
    | "type_name"
    | "combined_profit"
    | "trading_profit"
    | "manufacturing_profit"
    | "sells_qty"
    | "buys_qty";
  const [sortKey, setSortKey] = useState<SortKey>("combined_profit");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");

  const controllerRef = useRef<AbortController | null>(null);

  const loadAll = useCallback(async () => {
    if (!isLoggedIn) {
      setSummary(null);
      setByType([]);
      return;
    }
    controllerRef.current?.abort();
    const c = new AbortController();
    controllerRef.current = c;
    setLoading(true);
    setError(null);
    try {
      const [s, bt] = await Promise.all([
        getJournalSummary({ scope, days: period, fifoMode }),
        getJournalByType({ scope, days: period, fifoMode }),
      ]);
      if (c.signal.aborted) return;
      setSummary(s);
      setByType(bt.rows ?? []);
    } catch (e) {
      if (!c.signal.aborted) setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (!c.signal.aborted) setLoading(false);
    }
  }, [isLoggedIn, scope, period, fifoMode]);

  // Refetch when period / mode / login / visitToken changes.
  useEffect(() => {
    void loadAll();
  }, [loadAll, visitToken]);

  // Silent sync on mount if last sync is >1d old (any wallet).
  useEffect(() => {
    if (!isLoggedIn || !summary) return;
    const stale = (summary.stale_syncs ?? []).some((s) => s.days_ago >= 1);
    if (stale) {
      void doSync(true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [summary, isLoggedIn]);

  const doSync = async (silent = false) => {
    setSyncing(true);
    if (!silent) setError(null);
    try {
      const resp = await syncTradeJournal(scope);
      setLastSyncResp(resp);
      // After a sync the compute cache is invalidated server-side; reload.
      await loadAll();
    } catch (e) {
      if (!silent) setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSyncing(false);
    }
  };

  const openDrawer = async (row: JournalByTypeRow) => {
    setDrawerTypeID(row.type_id);
    setDrawerTypeName(row.type_name || `Type #${row.type_id}`);
    setDrawerLoading(true);
    try {
      const data = await getJournalLots(row.type_id, { scope, days: period, fifoMode });
      setDrawerLots(data.lots ?? []);
      setDrawerMfg(data.manufacturing_lots ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setDrawerLoading(false);
    }
  };

  const closeDrawer = () => {
    setDrawerTypeID(null);
    setDrawerLots([]);
    setDrawerMfg([]);
  };

  const sortedRows = useMemo(() => {
    const rows = [...byType];
    rows.sort((a, b) => {
      let av: number | string, bv: number | string;
      if (sortKey === "type_name") {
        av = a.type_name || "";
        bv = b.type_name || "";
      } else {
        av = (a as unknown as Record<SortKey, number>)[sortKey];
        bv = (b as unknown as Record<SortKey, number>)[sortKey];
      }
      const cmp =
        typeof av === "string" && typeof bv === "string"
          ? av.localeCompare(bv)
          : (av as number) - (bv as number);
      return sortDir === "asc" ? cmp : -cmp;
    });
    return rows;
  }, [byType, sortKey, sortDir]);

  const toggleSort = (k: SortKey) => {
    if (sortKey === k) setSortDir(sortDir === "asc" ? "desc" : "asc");
    else {
      setSortKey(k);
      setSortDir(k === "type_name" ? "asc" : "desc");
    }
  };

  // Chart series overlay: PnLChart expects the DailyPnLEntry shape from
  // PortfolioPnL. Adapt each source (trading / mfg / combined) into that
  // shape with net_pnl set from the corresponding series.
  const chartData = useMemo(() => {
    if (!summary)
      return {
        trading: [] as DailyEntryLike[],
        mfg: [] as DailyEntryLike[],
        combined: [] as DailyEntryLike[],
      };
    const trading: DailyEntryLike[] = [];
    const mfg: DailyEntryLike[] = [];
    const combined: DailyEntryLike[] = [];
    let cumT = 0,
      cumM = 0,
      cumC = 0;
    for (const d of summary.daily_pnl) {
      cumT += d.trading_pnl;
      cumM += d.manufacturing_pnl;
      cumC += d.combined_pnl;
      trading.push({
        date: d.date,
        buy_total: d.buy_isk,
        sell_total: d.sell_isk,
        net_pnl: d.trading_pnl,
        cumulative_pnl: cumT,
        drawdown_pct: 0,
        transactions: d.transactions,
      });
      mfg.push({
        date: d.date,
        buy_total: d.buy_isk,
        sell_total: d.sell_isk,
        net_pnl: d.manufacturing_pnl,
        cumulative_pnl: cumM,
        drawdown_pct: 0,
        transactions: d.transactions,
      });
      combined.push({
        date: d.date,
        buy_total: d.buy_isk,
        sell_total: d.sell_isk,
        net_pnl: d.combined_pnl,
        cumulative_pnl: cumC,
        drawdown_pct: 0,
        transactions: d.transactions,
      });
    }
    return { trading, mfg, combined };
  }, [summary]);

  if (!isLoggedIn) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-eve-dim text-sm space-y-2">
        <div>{t("journalNotLoggedIn")}</div>
      </div>
    );
  }

  const staleSyncs = summary?.stale_syncs ?? [];
  const trackingSince = summary?.tracking_since ?? {};
  const trackingKeys = Object.keys(trackingSince);
  const earliestTrackingSince =
    trackingKeys.length > 0
      ? trackingKeys.reduce<string>((acc, key) => {
          const v = trackingSince[key];
          if (!acc) return v;
          return v && v < acc ? v : acc;
        }, "")
      : "";

  return (
    <div className="flex flex-col h-full space-y-3 p-3">
      {/* Header: period + fifo + sync */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-xs text-eve-dim uppercase tracking-wider">
            {t("journalPeriodLabel")}
          </span>
          {([7, 30, 90, "all"] as PeriodPreset[]).map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={`px-2.5 py-1 text-[11px] rounded-sm border transition-colors ${
                period === p
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
              }`}
            >
              {p === "all" ? t("journalRangeAll") : t(`journalRange${p}d` as TranslationKey)}
            </button>
          ))}
          <div className="ml-3 flex items-center gap-1 text-[11px]">
            <span className="text-eve-dim">{t("journalFifoModeLabel")}</span>
            <select
              value={fifoMode}
              onChange={(e) => setPersistedFifoMode(e.target.value as JournalFIFOMode)}
              className="bg-eve-dark border border-eve-border rounded-sm px-1 py-0.5 text-eve-text"
            >
              <option value="strict_date">{t("journalFifoModeStrict")}</option>
              <option value="trade_first">{t("journalFifoModeTradeFirst")}</option>
              <option value="manufacture_first">{t("journalFifoModeMfgFirst")}</option>
            </select>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {earliestTrackingSince && (
            <span className="text-[11px] text-eve-dim" title={t("journalTrackingSinceHint")}>
              {t("journalTrackingSince", { date: earliestTrackingSince.slice(0, 10) })}
            </span>
          )}
          <button
            onClick={() => void doSync(false)}
            disabled={syncing}
            className="px-3 py-1 text-xs rounded-sm border border-eve-accent/60 bg-eve-accent/10 text-eve-accent hover:bg-eve-accent/20 disabled:opacity-50"
          >
            {syncing ? t("journalSyncing") : t("journalSyncBtn")}
          </button>
        </div>
      </div>

      {/* Stale sync warning */}
      {staleSyncs.length > 0 && (
        <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {t("journalStaleSyncWarning", {
            count: staleSyncs.length,
            days: Math.max(...staleSyncs.map((s) => s.days_ago)),
          })}
        </div>
      )}

      {error && (
        <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {error}
        </div>
      )}

      {/* KPI tiles */}
      {summary && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <KPITile
            label={t("journalKpiTradingPL")}
            value={summary.totals.trading_pnl}
            emphasis={false}
          />
          <KPITile
            label={t("journalKpiManufacturingPL")}
            value={summary.totals.manufacturing_pnl}
            emphasis={false}
          />
          <KPITile
            label={t("journalKpiCombinedPL")}
            value={summary.totals.combined_pnl}
            emphasis={true}
          />
        </div>
      )}

      {/* Secondary stat strip */}
      {summary && (
        <div className="flex flex-wrap gap-3 text-[11px] text-eve-dim">
          <span>
            {t("journalKpiBuyISK")}:{" "}
            <span className="text-eve-text font-mono">{formatIsk(summary.totals.buy_isk)}</span>
          </span>
          <span>
            {t("journalKpiSellISK")}:{" "}
            <span className="text-eve-text font-mono">{formatIsk(summary.totals.sell_isk)}</span>
          </span>
          <span>
            {t("journalKpiFees")}:{" "}
            <span className="text-eve-text font-mono">{formatIsk(summary.totals.fees_isk)}</span>
          </span>
          {summary.totals.unattributed_isk > 0 && (
            <span title={t("journalKpiUnattributedHint")} className="cursor-help">
              {t("journalKpiUnattributed")}:{" "}
              <span className="text-yellow-400 font-mono">
                {formatIsk(summary.totals.unattributed_isk)}
              </span>
            </span>
          )}
          {summary.totals.est_material_cost_isk > 0 && (
            <span title={t("journalKpiEstMaterialCostHint")} className="cursor-help">
              {t("journalKpiEstMaterialCost")}:{" "}
              <span className="text-eve-dim font-mono italic">
                {formatIsk(summary.totals.est_material_cost_isk)}
              </span>
            </span>
          )}
        </div>
      )}

      {/* Chart */}
      {summary && summary.daily_pnl.length > 0 && (
        <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
          <div className="flex items-center justify-between mb-2">
            <div className="text-[10px] text-eve-dim uppercase tracking-wider">
              {t("journalChartTitle")}
            </div>
            <div className="flex items-center gap-1 text-[10px]">
              <ChartLegendChip
                active={showTrading}
                color="bg-sky-500"
                label={t("journalChartSeriesTrading")}
                onClick={() => setShowTrading(!showTrading)}
              />
              <ChartLegendChip
                active={showMfg}
                color="bg-amber-500"
                label={t("journalChartSeriesMfg")}
                onClick={() => setShowMfg(!showMfg)}
              />
              <ChartLegendChip
                active={showCombined}
                color="bg-white"
                label={t("journalChartSeriesCombined")}
                onClick={() => setShowCombined(!showCombined)}
              />
            </div>
          </div>
          <div className="space-y-2">
            {showCombined && (
              <SeriesRow label={t("journalChartSeriesCombined")}>
                <PnLChart data={chartData.combined} mode="cumulative" formatIsk={formatIsk} />
              </SeriesRow>
            )}
            {showTrading && (
              <SeriesRow label={t("journalChartSeriesTrading")}>
                <PnLChart data={chartData.trading} mode="cumulative" formatIsk={formatIsk} />
              </SeriesRow>
            )}
            {showMfg && (
              <SeriesRow label={t("journalChartSeriesMfg")}>
                <PnLChart data={chartData.mfg} mode="cumulative" formatIsk={formatIsk} />
              </SeriesRow>
            )}
          </div>
        </div>
      )}

      {/* Per-item table */}
      <div className="flex-1 min-h-0 overflow-auto border border-eve-border rounded-sm bg-eve-panel">
        {loading && (
          <div className="p-4 text-center text-eve-dim text-xs">
            {t("journalLoading")}
          </div>
        )}
        {!loading && sortedRows.length === 0 && (
          <div className="p-4 text-center text-eve-dim text-xs">
            {t("journalNoData")}
          </div>
        )}
        {!loading && sortedRows.length > 0 && (
          <table className="w-full text-xs">
            <thead className="bg-eve-dark sticky top-0 z-10">
              <tr className="text-eve-dim">
                <SortableTH label={t("colItem")} k="type_name" curKey={sortKey} curDir={sortDir} onClick={toggleSort} align="left" />
                <SortableTH label={t("journalTableColBuysQty")} k="buys_qty" curKey={sortKey} curDir={sortDir} onClick={toggleSort} align="right" />
                <SortableTH label={t("journalTableColSellsQty")} k="sells_qty" curKey={sortKey} curDir={sortDir} onClick={toggleSort} align="right" />
                <th className="px-2 py-1.5 text-right">{t("journalTableColAvgBuy")}</th>
                <th className="px-2 py-1.5 text-right">{t("journalTableColAvgSell")}</th>
                <SortableTH label={t("journalTableColTradingPL")} k="trading_profit" curKey={sortKey} curDir={sortDir} onClick={toggleSort} align="right" />
                <SortableTH label={t("journalTableColMfgPL")} k="manufacturing_profit" curKey={sortKey} curDir={sortDir} onClick={toggleSort} align="right" />
                <SortableTH label={t("journalTableColCombinedPL")} k="combined_profit" curKey={sortKey} curDir={sortDir} onClick={toggleSort} align="right" />
                <th className="px-2 py-1.5 text-right">{t("journalTableColHeld")}</th>
              </tr>
            </thead>
            <tbody>
              {sortedRows.map((row) => {
                const combinedTone = row.combined_profit >= 0 ? "text-eve-profit" : "text-eve-error";
                return (
                  <tr
                    key={row.type_id}
                    onClick={() => void openDrawer(row)}
                    className="border-t border-eve-border/50 hover:bg-eve-accent/5 cursor-pointer"
                  >
                    <td className="px-2 py-1 text-eve-text">
                      <div className="flex items-center gap-2">
                        <img
                          src={`https://images.evetech.net/types/${row.type_id}/icon?size=32`}
                          alt=""
                          className="w-4 h-4"
                        />
                        <span className="truncate max-w-[220px]">{row.type_name || `Type #${row.type_id}`}</span>
                      </div>
                    </td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">{row.buys_qty}</td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">{row.sells_qty}</td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">{row.avg_buy_price ? formatIsk(row.avg_buy_price) : "—"}</td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">{row.avg_sell_price ? formatIsk(row.avg_sell_price) : "—"}</td>
                    <td className={`px-2 py-1 text-right font-mono ${row.trading_profit >= 0 ? "text-eve-profit" : "text-eve-error"}`}>
                      {formatIskSigned(row.trading_profit)}
                    </td>
                    <td className={`px-2 py-1 text-right font-mono ${row.manufacturing_profit >= 0 ? "text-eve-profit" : "text-eve-error"}`}>
                      {formatIskSigned(row.manufacturing_profit)}
                    </td>
                    <td className={`px-2 py-1 text-right font-mono font-semibold ${combinedTone}`}>
                      {formatIskSigned(row.combined_profit)}
                    </td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">
                      {row.held_qty_trade || 0}/{row.held_qty_manufacture || 0}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {lastSyncResp && (
        <div className="text-[10px] text-eve-dim">
          {t("journalLastSyncSummary", {
            wallets: lastSyncResp.wallets.length,
            linked: lastSyncResp.industry_jobs_auto_linked,
            ambiguous: lastSyncResp.industry_jobs_still_unlinked_ambiguous,
          })}
        </div>
      )}

      {/* Drawer */}
      {drawerTypeID != null && (
        <LotsDrawer
          typeID={drawerTypeID}
          typeName={drawerTypeName}
          lots={drawerLots}
          mfgLots={drawerMfg}
          loading={drawerLoading}
          onClose={closeDrawer}
          onLinked={() => void loadAll()}
        />
      )}

      {/* Debug: recent sync stats */}
      {import.meta.env?.DEV && lastSyncResp && (
        <details className="text-[9px] text-eve-dim">
          <summary>sync debug</summary>
          <pre className="whitespace-pre-wrap">{JSON.stringify(lastSyncResp, null, 2)}</pre>
        </details>
      )}
    </div>
  );

  // Suppress unused-var warning for humanTimeSince in fallback builds.
  void humanTimeSince;
}

// --- helpers ---

function KPITile({ label, value, emphasis }: { label: string; value: number; emphasis: boolean }) {
  const tone = value >= 0 ? "text-eve-profit" : "text-eve-error";
  return (
    <div className={`rounded-sm border ${emphasis ? "border-eve-accent bg-eve-accent/5" : "border-eve-border bg-eve-panel"} p-3`}>
      <div className="text-[10px] text-eve-dim uppercase tracking-wider">{label}</div>
      <div className={`font-mono ${tone} ${emphasis ? "text-2xl font-bold" : "text-lg font-semibold"}`}>
        {formatIskSigned(value)} ISK
      </div>
    </div>
  );
}

function ChartLegendChip({
  active,
  color,
  label,
  onClick,
}: {
  active: boolean;
  color: string;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-sm border transition-opacity ${
        active ? "border-eve-border bg-eve-dark opacity-100" : "border-eve-border/40 opacity-50"
      }`}
    >
      <span className={`inline-block w-2 h-2 rounded-full ${color}`} />
      <span className="text-eve-dim">{label}</span>
    </button>
  );
}

function SeriesRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[9px] text-eve-dim mb-1">{label}</div>
      {children}
    </div>
  );
}

function SortableTH<K extends string>({
  label,
  k,
  curKey,
  curDir,
  onClick,
  align,
}: {
  label: string;
  k: K;
  curKey: K;
  curDir: "asc" | "desc";
  onClick: (k: K) => void;
  align: "left" | "right";
}) {
  const active = curKey === k;
  return (
    <th
      className={`px-2 py-1.5 text-${align} cursor-pointer hover:text-eve-text select-none`}
      onClick={() => onClick(k)}
    >
      {label}
      {active && <span className="ml-1">{curDir === "asc" ? "▲" : "▼"}</span>}
    </th>
  );
}

// --- Drawer ---

function LotsDrawer({
  typeID,
  typeName,
  lots,
  mfgLots,
  loading,
  onClose,
  onLinked,
}: {
  typeID: number;
  typeName: string;
  lots: JournalLot[];
  mfgLots: JournalManufacturingLot[];
  loading: boolean;
  onClose: () => void;
  onLinked: () => void;
}) {
  const { t } = useI18n();
  const [linkingLot, setLinkingLot] = useState<number | null>(null);
  const [candidates, setCandidates] = useState<JournalLinkCandidate[]>([]);
  const [linkError, setLinkError] = useState<string | null>(null);

  const openLinkPicker = async (esiJobID: number) => {
    setLinkingLot(esiJobID);
    setLinkError(null);
    try {
      const list = await getJournalLinkCandidates(esiJobID);
      setCandidates(list);
    } catch (e) {
      setLinkError(e instanceof Error ? e.message : String(e));
    }
  };

  const confirmLink = async (esiJobID: number, ledgerJobID: number) => {
    try {
      await linkJournalJob(esiJobID, ledgerJobID);
      setLinkingLot(null);
      setCandidates([]);
      onLinked();
    } catch (e) {
      setLinkError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/40" onClick={onClose}>
      <div
        className="w-full max-w-[720px] h-full bg-eve-dark border-l border-eve-border overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="sticky top-0 bg-eve-dark border-b border-eve-border p-3 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <img
              src={`https://images.evetech.net/types/${typeID}/icon?size=32`}
              alt=""
              className="w-6 h-6"
            />
            <span className="text-sm text-eve-text">{typeName}</span>
          </div>
          <button
            onClick={onClose}
            className="text-eve-dim hover:text-eve-text text-lg"
            title={t("close")}
          >
            ✕
          </button>
        </div>
        <div className="p-3 space-y-4">
          {loading ? (
            <div className="text-xs text-eve-dim">{t("journalLoading")}</div>
          ) : (
            <>
              <div>
                <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-1">
                  {t("journalDrawerLotsSection")} ({lots.length})
                </div>
                <table className="w-full text-[11px]">
                  <thead className="text-eve-dim">
                    <tr>
                      <th className="px-2 py-1 text-left">{t("journalDrawerSource")}</th>
                      <th className="px-2 py-1 text-left">{t("journalDrawerSellDate")}</th>
                      <th className="px-2 py-1 text-right">{t("journalDrawerQty")}</th>
                      <th className="px-2 py-1 text-right">{t("journalDrawerCost")}</th>
                      <th className="px-2 py-1 text-right">{t("journalDrawerSell")}</th>
                      <th className="px-2 py-1 text-right">{t("journalDrawerNetPnl")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {lots.map((l, i) => {
                      const cost =
                        l.source === "trade"
                          ? (l.buy_unit_price ?? 0) * l.matched_qty
                          : l.source === "manufacture"
                            ? l.sell_gross - l.net_profit - l.sell_fees
                            : 0;
                      return (
                        <tr key={i} className="border-t border-eve-border/50">
                          <td className="px-2 py-1">
                            <span
                              className={`inline-flex px-1.5 py-0.5 rounded-sm text-[9px] uppercase tracking-wider border ${
                                l.source === "trade"
                                  ? "border-sky-500/50 bg-sky-500/10 text-sky-300"
                                  : l.source === "manufacture"
                                    ? "border-amber-500/50 bg-amber-500/10 text-amber-300"
                                    : "border-eve-dim/50 bg-eve-dim/10 text-eve-dim"
                              }`}
                            >
                              {l.source === "trade"
                                ? t("journalLotBadgeTrade")
                                : l.source === "manufacture"
                                  ? t("journalLotBadgeMfg")
                                  : t("journalLotBadgeOrphan")}
                            </span>
                          </td>
                          <td className="px-2 py-1 text-eve-dim">{(l.sell_date ?? "").slice(0, 10)}</td>
                          <td className="px-2 py-1 text-right text-eve-dim">{l.matched_qty}</td>
                          <td className="px-2 py-1 text-right text-eve-dim">{formatIsk(cost)}</td>
                          <td className="px-2 py-1 text-right text-eve-dim">{formatIsk(l.sell_gross)}</td>
                          <td className={`px-2 py-1 text-right font-mono ${l.net_profit >= 0 ? "text-eve-profit" : "text-eve-error"}`}>
                            {formatIskSigned(l.net_profit)}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {mfgLots.length > 0 && (
                <div>
                  <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-1">
                    {t("journalDrawerMfgSection")} ({mfgLots.length})
                  </div>
                  <div className="space-y-2">
                    {mfgLots.map((m) => (
                      <div key={m.job_id} className="rounded-sm border border-eve-border p-2 text-[11px]">
                        <div className="flex items-center justify-between mb-1">
                          <div>
                            <span className="text-eve-text">
                              {m.produced_qty}× @ {formatIsk(m.unit_cost)} ISK/u
                            </span>
                            <span className="ml-2 text-eve-dim">
                              {t("journalDrawerCompleted")}: {m.completed_date.slice(0, 10)}
                            </span>
                          </div>
                          <div className="flex items-center gap-1">
                            <span
                              className="inline-flex px-1.5 py-0.5 rounded-sm text-[9px] uppercase border border-eve-border bg-eve-panel text-eve-dim"
                              title={t("journalMEAssumedHint")}
                            >
                              ME {m.me} ({m.me_tag})
                            </span>
                            {m.me_tag !== "planner" && (
                              <button
                                type="button"
                                onClick={() => void openLinkPicker(m.job_id)}
                                className="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded-sm border border-eve-accent/50 bg-eve-accent/10 text-eve-accent hover:bg-eve-accent/20"
                              >
                                {t("journalLinkToPlanner")}
                              </button>
                            )}
                          </div>
                        </div>
                        <div className="text-[10px] text-eve-dim">
                          {t("journalDrawerInstall")}: {formatIsk(m.install_cost)} · {t("journalDrawerMaterials")}: {formatIsk(m.material_cost)}
                          {m.materials_estimated && (
                            <span className="ml-1 text-yellow-400" title={t("journalMaterialEstimatedHint")}>
                              ⚠ {t("journalMaterialEstimatedBadge")}
                            </span>
                          )}
                        </div>
                        {m.materials && m.materials.length > 0 && (
                          <details className="mt-1">
                            <summary className="cursor-pointer text-[10px] text-eve-dim hover:text-eve-text">
                              {t("journalDrawerMaterialBreakdown")}
                            </summary>
                            <table className="w-full mt-1 text-[10px]">
                              <tbody>
                                {m.materials.map((mat, idx) => (
                                  <tr key={idx} className="text-eve-dim">
                                    <td>{mat.type_name || `Type #${mat.type_id}`}</td>
                                    <td className="text-right">{mat.qty}</td>
                                    <td className="text-right">{formatIsk(mat.total_cost)}</td>
                                    <td className="text-right italic">
                                      {mat.source === "avg" ? t("journalMaterialSourceAvg") : t("journalMaterialSourceFifo")}
                                    </td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          </details>
                        )}
                        {linkingLot === m.job_id && (
                          <div className="mt-2 border-t border-eve-border pt-2">
                            {linkError && <div className="text-[10px] text-red-400 mb-1">{linkError}</div>}
                            {candidates.length === 0 ? (
                              <div className="text-[10px] text-eve-dim">{t("journalNoLinkCandidates")}</div>
                            ) : (
                              <ul className="space-y-1">
                                {candidates.map((c) => (
                                  <li key={c.ledger_job_id} className="flex items-center justify-between text-[10px]">
                                    <span className="text-eve-dim">
                                      Ledger job #{c.ledger_job_id} · {c.runs} runs · start {c.started_at.slice(0, 10)}
                                    </span>
                                    <button
                                      type="button"
                                      onClick={() => void confirmLink(m.job_id, c.ledger_job_id)}
                                      className="text-[9px] px-1.5 py-0.5 rounded-sm border border-eve-accent bg-eve-accent/20 text-eve-accent hover:bg-eve-accent/30"
                                    >
                                      {t("journalLinkConfirm")}
                                    </button>
                                  </li>
                                ))}
                              </ul>
                            )}
                            <button
                              type="button"
                              onClick={() => {
                                setLinkingLot(null);
                                setCandidates([]);
                              }}
                              className="mt-1 text-[9px] text-eve-dim hover:text-eve-text"
                            >
                              {t("dialogCancel")}
                            </button>
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
