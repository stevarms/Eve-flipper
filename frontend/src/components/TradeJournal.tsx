import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CopyPrice } from "@/components/ui/CopyPrice";
import { ItemRef } from "@/components/ui/ItemRef";
import {
  getAuthStatus,
  getJournalByType,
  getJournalLinkCandidates,
  getJournalLots,
  getJournalSummary,
  linkJournalJob,
  syncTradeJournal,
  type JournalAnalyticsSource,
  type JournalByTypeRow,
  type JournalFIFOMode,
  type JournalLinkCandidate,
  type JournalLot,
  type JournalManufacturingLot,
  type JournalSummaryResponse,
  type JournalSyncResponse,
  type WalletScope,
} from "../lib/api";
import type { AuthCharacter } from "../lib/types";
import { useI18n, type TranslationKey } from "../lib/i18n";
import { formatIsk as formatIskLib, formatIskSigned as formatIskSignedLib } from "../lib/format";
import { PnLLineChart, SortableTH, type PnLLineSeries } from "./journal/PnLPrimitives";
import { JournalAnalyticsView } from "./journal/JournalAnalyticsView";
import { JournalTransactionsView } from "./journal/JournalTransactionsView";
import { JournalFeeStrip } from "./journal/JournalFeeStrip";

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
  /** Jumps to Assets → Positions, which owns open positions. */
  onOpenPositions?: () => void;
}

type PeriodPreset = 7 | 30 | 90 | "all";

type JournalView = "summary" | "analytics" | "transactions";

const VIEW_LABEL_KEY: Record<JournalView, TranslationKey> = {
  summary: "journalViewSummary",
  analytics: "journalViewAnalytics",
  transactions: "journalViewTransactions",
};

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
const CHART_LAYOUT_STORAGE_KEY = "trade_journal.chart_layout";

// Overlay puts all three series on one set of axes, which is the layout that
// answers "did manufacturing carry a losing trading week". Split gives each
// series its own y-scale, so a small line stays readable next to a large one
// at the cost of being no longer directly comparable.
type ChartLayout = "overlay" | "split";

/** Local presentation of the shared formatter (lib/format.ts). */
const ISK_FMT = {
  maxTier: "T",
  space: false,
  decimals: { t: 2, b: 2, m: 2, k: 1, unit: 0 },
} as const;

/** The unfiltered scope, hoisted so its identity is stable across renders. */
const SCOPE_ALL: WalletScope = { include_all: true };

function formatIsk(v: number): string {
  return formatIskLib(v, undefined, ISK_FMT);
}

function formatIskSigned(v: number): string {
  return formatIskSignedLib(v, undefined, ISK_FMT);
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

export function TradeJournal({ isLoggedIn, visitToken, onOpenPositions }: Props) {
  const { t } = useI18n();
  const [period, setPeriod] = useState<PeriodPreset>(DEFAULT_PERIOD);
  // Summary answers "how am I doing"; Analytics answers "how reliably, and
  // where from". They used to be two tabs computing profit two different ways.
  const [view, setView] = useState<JournalView>("summary");
  // Which slice of the ledger every figure on the tab describes. It replaced
  // the chart's three legend chips: a per-series show/hide only dimmed lines,
  // while this drives the statistics too.
  const [source, setSource] = useState<JournalAnalyticsSource>("");
  const [feeOverride, setFeeOverride] = useState<{ salesTax: number; brokerFee: number } | null>(
    null,
  );
  const [fifoMode, setFifoMode] = useState<JournalFIFOMode>(() => {
    if (typeof window === "undefined") return "strict_date";
    const raw = window.localStorage.getItem(FIFO_STORAGE_KEY);
    if (raw === "trade_first" || raw === "manufacture_first" || raw === "strict_date") return raw;
    return "strict_date";
  });
  const [chartLayout, setChartLayout] = useState<ChartLayout>(() => {
    if (typeof window === "undefined") return "overlay";
    return window.localStorage.getItem(CHART_LAYOUT_STORAGE_KEY) === "split"
      ? "split"
      : "overlay";
  });
  const setPersistedChartLayout = (l: ChartLayout) => {
    setChartLayout(l);
    if (typeof window !== "undefined") window.localStorage.setItem(CHART_LAYOUT_STORAGE_KEY, l);
  };
  const setPersistedFifoMode = (m: JournalFIFOMode) => {
    setFifoMode(m);
    if (typeof window !== "undefined") window.localStorage.setItem(FIFO_STORAGE_KEY, m);
  };

  // Wallet scope — start "all", let the user narrow via the picker.
  // Explicit include_characters + include_corp_divisions arrays are only
  // populated when the user unticks at least one chip; otherwise the
  // scope stays {include_all: true} so newly-authorized wallets are
  // pooled automatically.
  const [excludedCharacters, setExcludedCharacters] = useState<Set<number>>(new Set());
  const [excludedCorpDivs, setExcludedCorpDivs] = useState<Set<string>>(new Set());
  const [authCharacters, setAuthCharacters] = useState<AuthCharacter[]>([]);
  useEffect(() => {
    if (!isLoggedIn) return;
    void getAuthStatus()
      .then((s) => setAuthCharacters(s.characters ?? []))
      .catch(() => setAuthCharacters([]));
  }, [isLoggedIn]);

  const [summary, setSummary] = useState<JournalSummaryResponse | null>(null);
  // The summary response's tracking_since keys enumerate every wallet with
  // archive rows. Corp divisions surface as "corp:{id}:{div}"; we parse
  // them out to build the corp side of the chip picker.
  //
  // Memoize on the *keys*, joined into a string, not on `summary` itself.
  //
  // `summary` is a freshly-parsed response object, so its identity changes on
  // every fetch even when the wallets are identical. Keying this memo off it
  // closed a render loop: new summary -> new knownCorpDivs array -> new
  // `scope` object -> new `loadAll` callback -> the [loadAll] effect refires
  // -> fetch -> new summary. The table repainted continuously and the two
  // journal endpoints were called in a tight cycle. A joined key string is
  // stable by value, so a refetch that returns the same wallets ends the
  // chain here.
  const trackingKey = useMemo(
    () => Object.keys(summary?.tracking_since ?? {}).sort().join(","),
    [summary],
  );
  // What the period paid to *place* orders, as opposed to fill them. Kept out
  // of combined P&L (which is the sum of per-row profits, and no row owns
  // these) and shown as its own subtraction underneath it.
  const orderCostsIsk =
    (summary?.totals.actual_broker_fee_isk ?? 0) +
    (summary?.totals.actual_provider_tax_isk ?? 0);
  const knownCorpDivs = useMemo(() => {
    const out: { key: string; corpID: number; div: number }[] = [];
    for (const key of trackingKey ? trackingKey.split(",") : []) {
      if (!key.startsWith("corp:")) continue;
      const parts = key.split(":");
      if (parts.length !== 3) continue;
      const corpID = Number(parts[1]);
      const div = Number(parts[2]);
      if (Number.isFinite(corpID) && Number.isFinite(div)) {
        out.push({ key, corpID, div });
      }
    }
    return out;
  }, [trackingKey]);
  const scope = useMemo<WalletScope>(() => {
    // A shared constant, not a fresh literal: with no exclusions -- the
    // default, and what most users stay on -- this keeps `scope` identical
    // across every recompute, so `loadAll` is never rebuilt and the mount
    // settles in one fetch instead of two.
    if (excludedCharacters.size === 0 && excludedCorpDivs.size === 0) {
      return SCOPE_ALL;
    }
    const include_characters = authCharacters
      .filter((c) => !excludedCharacters.has(c.character_id))
      .map((c) => c.character_id);
    const include_corp_divisions = knownCorpDivs
      .filter((d) => !excludedCorpDivs.has(d.key))
      .map((d) => ({ corporation_id: d.corpID, division: d.div }));
    return { include_all: false, include_characters, include_corp_divisions };
  }, [excludedCharacters, excludedCorpDivs, authCharacters, knownCorpDivs]);

  const [byType, setByType] = useState<JournalByTypeRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastSyncResp, setLastSyncResp] = useState<JournalSyncResponse | null>(null);
  const [analyticsToken, setAnalyticsToken] = useState(0);

  // Drawer state
  const [drawerTypeID, setDrawerTypeID] = useState<number | null>(null);
  const [drawerTypeName, setDrawerTypeName] = useState<string>("");
  const [drawerLots, setDrawerLots] = useState<JournalLot[]>([]);
  const [drawerMfg, setDrawerMfg] = useState<JournalManufacturingLot[]>([]);
  const [drawerLoading, setDrawerLoading] = useState(false);

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
      const read = {
        scope,
        days: period,
        fifoMode,
        salesTax: feeOverride?.salesTax,
        brokerFee: feeOverride?.brokerFee,
      };
      const [s, bt] = await Promise.all([getJournalSummary(read), getJournalByType(read)]);
      if (c.signal.aborted) return;
      setSummary(s);
      setByType(bt.rows ?? []);
    } catch (e) {
      if (!c.signal.aborted) setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (!c.signal.aborted) setLoading(false);
    }
  }, [isLoggedIn, scope, period, fifoMode, feeOverride]);

  // Refetch when period / mode / login / visitToken changes.
  useEffect(() => {
    void loadAll();
  }, [loadAll, visitToken]);

  // Silent catch-up sync when a wallet has gone a long time without one.
  //
  // Fires at most once per scope per mount, tracked in a ref. It cannot key
  // off `summary` alone: doSync ends with loadAll(), which replaces `summary`,
  // which re-runs this effect. If the sync can't actually advance a wallet's
  // last_sync_at -- an expired refresh token, a corp division whose role was
  // revoked, a character that left the account but still has archive rows --
  // then stale_syncs never empties and the two spin against ESI forever.
  //
  // The old `days_ago >= 1` test was also dead: walletMetaForFilter only
  // reports a wallet once it is 20 days stale, so every row it returns
  // already passes. The backend owns the threshold; don't restate it here.
  const autoSyncedScopeRef = useRef<string | null>(null);
  useEffect(() => {
    if (!isLoggedIn || !summary || syncing) return;
    if ((summary.stale_syncs ?? []).length === 0) return;
    const key = JSON.stringify(scope);
    if (autoSyncedScopeRef.current === key) return;
    autoSyncedScopeRef.current = key;
    void doSync(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [summary, isLoggedIn, syncing, scope]);

  const doSync = async (silent = false) => {
    setSyncing(true);
    if (!silent) setError(null);
    try {
      const resp = await syncTradeJournal(scope);
      setLastSyncResp(resp);
      // After a sync the compute cache is invalidated server-side; reload.
      // The token pushes the same reload into the analytics view, which fetches
      // its own endpoint and would otherwise keep showing pre-sync figures.
      setAnalyticsToken((n) => n + 1);
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
      // Same rate pair as the table: a drawer computed at different fees would
      // show a different profit for the row the user just clicked.
      const data = await getJournalLots(row.type_id, {
        scope,
        days: period,
        fifoMode,
        salesTax: feeOverride?.salesTax,
        brokerFee: feeOverride?.brokerFee,
      });
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

  // exportCSV dumps the currently visible per-item table (with the active
  // sort applied) as a CSV file so users can pivot the numbers in a
  // spreadsheet without another API call.
  const exportCSV = () => {
    const esc = (v: string) => (/[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
    const header = [
      t("colItem"),
      t("journalTableColBuysQty"),
      t("journalTableColSellsQty"),
      t("journalTableColAvgBuy"),
      t("journalTableColAvgSell"),
      t("journalTableColTradingPL"),
      t("journalTableColMfgPL"),
      t("journalTableColCombinedPL"),
      t("journalTableColHeld"),
    ]
      .map(esc)
      .join(",");
    const rows = sortedRows.map((r) =>
      [
        esc(r.type_name || `Type #${r.type_id}`),
        String(r.buys_qty),
        String(r.sells_qty),
        String(r.avg_buy_price ?? 0),
        String(r.avg_sell_price ?? 0),
        String(r.trading_profit),
        String(r.manufacturing_profit),
        String(r.combined_profit),
        `${r.held_qty_trade}/${r.held_qty_manufacture}`,
      ].join(","),
    );
    const blob = new Blob(["﻿", header, "\n", rows.join("\n")], {
      type: "text/csv;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    const periodTag = period === "all" ? "all" : `${period}d`;
    link.download = `eve-trade-journal-${periodTag}-${new Date().toISOString().slice(0, 10)}.csv`;
    link.click();
    URL.revokeObjectURL(url);
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
    <div className="flex flex-col h-full space-y-3 p-3 overflow-y-auto">
      {/* Header: period + fifo + sync + wallet scope */}
      <WalletScopePicker
        authCharacters={authCharacters}
        knownCorpDivs={knownCorpDivs}
        excludedCharacters={excludedCharacters}
        excludedCorpDivs={excludedCorpDivs}
        onToggleCharacter={(id) => {
          setExcludedCharacters((prev) => {
            const next = new Set(prev);
            if (next.has(id)) next.delete(id);
            else next.add(id);
            return next;
          });
        }}
        onToggleCorpDiv={(key) => {
          setExcludedCorpDivs((prev) => {
            const next = new Set(prev);
            if (next.has(key)) next.delete(key);
            else next.add(key);
            return next;
          });
        }}
        onSelectAll={() => {
          setExcludedCharacters(new Set());
          setExcludedCorpDivs(new Set());
        }}
      />
      {/* View + source: what depth, over which slice of the ledger. */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1">
          {(["summary", "transactions", "analytics"] as JournalView[]).map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setView(v)}
              className={`px-3 py-1 text-[11px] rounded-sm border transition-colors ${
                view === v
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
              }`}
            >
              {t(VIEW_LABEL_KEY[v])}
            </button>
          ))}
          <span className="ml-3 text-[11px] text-eve-dim uppercase tracking-wider">
            {t("journalSourceLabel")}
          </span>
          {(
            [
              ["", t("journalChartSeriesCombined")],
              ["trade", t("journalChartSeriesTrading")],
              ["manufacture", t("journalChartSeriesMfg")],
            ] as [JournalAnalyticsSource, string][]
          ).map(([value, label]) => (
            <button
              key={value || "combined"}
              type="button"
              onClick={() => setSource(value)}
              className={`px-2.5 py-1 text-[11px] rounded-sm border transition-colors ${
                source === value
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
        <JournalFeeStrip
          profile={summary?.fees ?? null}
          override={feeOverride}
          onChange={setFeeOverride}
        />
      </div>
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
          {/* Exports the per-item table, so it only belongs to the view that
              shows one. The transactions list carries its own export. */}
          {view === "summary" && (
            <button
              type="button"
              onClick={exportCSV}
              disabled={sortedRows.length === 0}
              className="px-2 py-1 text-xs rounded-sm border border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text hover:border-eve-accent/50 disabled:opacity-40"
              title={t("journalExportCsvHint")}
            >
              {t("journalExportCsv")}
            </button>
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

      {/* Stale sync warning. A never-synced wallet has days_ago = 0, so the
          "haven't synced in 0+ days" phrasing would be nonsense — it gets its
          own line. Aged wallets win when both are present, since losing
          history is the more urgent of the two. */}
      {staleSyncs.length > 0 && (
        <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {staleSyncs.some((s) => s.days_ago > 0)
            ? t("journalStaleSyncWarning", {
                count: staleSyncs.filter((s) => s.days_ago > 0).length,
                days: Math.max(...staleSyncs.map((s) => s.days_ago)),
              })
            : t("journalNeverSyncedWarning", { count: staleSyncs.length })}
        </div>
      )}

      {error && (
        <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {error}
        </div>
      )}

      {/* Transactions: one row per sale. Same matcher, same rates, same lots
          the Summary drawer shows — just not restricted to one item. */}
      {view === "transactions" && (
        <JournalTransactionsView
          scope={scope}
          period={period}
          fifoMode={fifoMode}
          source={source}
          feeOverride={feeOverride}
          reloadToken={analyticsToken}
          formatIsk={formatIsk}
          formatIskSigned={formatIskSigned}
        />
      )}

      {/* Analytics: the deep-dive half, fetched from its own endpoint over the
          same matcher, so it cannot report a different profit than the tiles. */}
      {view === "analytics" && (
        <div>
          <JournalAnalyticsView
            scope={scope}
            period={period}
            fifoMode={fifoMode}
            source={source}
            feeOverride={feeOverride}
            reloadToken={analyticsToken}
            formatIsk={formatIsk}
            onOpenPositions={onOpenPositions}
          />
        </div>
      )}

      {/* KPI tiles. The selected source is the emphasised one. */}
      {view === "summary" && summary && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <KPITile
            label={t("journalKpiTradingPL")}
            value={summary.totals.trading_pnl}
            emphasis={source === "trade"}
          />
          <KPITile
            label={t("journalKpiManufacturingPL")}
            value={summary.totals.manufacturing_pnl}
            emphasis={source === "manufacture"}
          />
          <KPITile
            label={t("journalKpiCombinedPL")}
            value={summary.totals.combined_pnl}
            emphasis={source === ""}
          />
        </div>
      )}

      {/* Secondary stat strip */}
      {view === "summary" && summary && (
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
          {/* Order-placement costs, from the wallet journal.
              These are real ISK the app used to show nowhere: a broker fee is
              charged when an order is placed, so it is owed whether or not the
              order ever fills, and cannot be attributed to a sale. They are
              reported beside the P&L rather than folded into it — hence the
              separate net-after figure below. */}
          {(summary.totals.actual_broker_fee_isk ?? 0) > 0 && (
            <span title={t("journalKpiActualBrokerHint")} className="cursor-help">
              {t("journalKpiActualBroker")}:{" "}
              <span className="text-eve-text font-mono">
                {formatIsk(summary.totals.actual_broker_fee_isk ?? 0)}
              </span>
            </span>
          )}
          {(summary.totals.actual_provider_tax_isk ?? 0) > 0 && (
            <span title={t("journalKpiProviderTaxHint")} className="cursor-help">
              {t("journalKpiProviderTax")}:{" "}
              <span className="text-eve-text font-mono">
                {formatIsk(summary.totals.actual_provider_tax_isk ?? 0)}
              </span>
            </span>
          )}
          {orderCostsIsk > 0 && (
            <span title={t("journalKpiNetAfterOrderCostsHint")} className="cursor-help">
              {t("journalKpiNetAfterOrderCosts")}:{" "}
              <span
                className={`font-mono ${
                  summary.totals.combined_pnl - orderCostsIsk >= 0
                    ? "text-eve-accent"
                    : "text-red-400"
                }`}
              >
                {formatIsk(summary.totals.combined_pnl - orderCostsIsk)}
              </span>
            </span>
          )}
        </div>
      )}

      {/* Chart. Combined stacks all three series so they can be read against
          each other; a single source shows only its own line. */}
      {view === "summary" &&
        summary &&
        summary.daily_pnl.length > 0 &&
        (() => {
          // Only the series the source selector admits. Emerald for trading and
          // sky for manufacturing matches the leaderboard's two-tone bars, so
          // one colour means one thing across the tab.
          const series: PnLLineSeries[] = [];
          if (source === "")
            series.push({
              key: "combined",
              label: t("journalChartSeriesCombined"),
              colorClass: "text-eve-accent",
              data: chartData.combined,
            });
          if (source === "" || source === "trade")
            series.push({
              key: "trading",
              label: t("journalChartSeriesTrading"),
              colorClass: "text-emerald-400",
              data: chartData.trading,
            });
          if (source === "" || source === "manufacture")
            series.push({
              key: "mfg",
              label: t("journalChartSeriesMfg"),
              colorClass: "text-sky-400",
              data: chartData.mfg,
            });
          // A single series has nothing to overlay, so the toggle stays hidden
          // and the layout is the same either way.
          const overlay = chartLayout === "overlay" && series.length > 1;

          return (
            <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
              <div className="flex items-center justify-between gap-3 mb-2 flex-wrap">
                <div className="text-[10px] text-eve-dim uppercase tracking-wider">
                  {t("journalChartTitle")}
                </div>
                <div className="flex items-center gap-3 flex-wrap">
                  {overlay && (
                    <div className="flex items-center gap-2">
                      {series.map((s) => (
                        <span
                          key={s.key}
                          className="flex items-center gap-1 text-[9px] text-eve-dim"
                        >
                          <span className={`w-2 h-2 rounded-full bg-current ${s.colorClass}`} />
                          {s.label}
                        </span>
                      ))}
                    </div>
                  )}
                  {series.length > 1 && (
                    <div className="flex items-center gap-1">
                      <ChartLayoutBtn
                        active={chartLayout === "overlay"}
                        onClick={() => setPersistedChartLayout("overlay")}
                      >
                        {t("journalChartOverlay")}
                      </ChartLayoutBtn>
                      <ChartLayoutBtn
                        active={chartLayout === "split"}
                        onClick={() => setPersistedChartLayout("split")}
                      >
                        {t("journalChartSplit")}
                      </ChartLayoutBtn>
                    </div>
                  )}
                </div>
              </div>
              {overlay ? (
                <PnLLineChart series={series} formatIsk={formatIsk} height={180} />
              ) : (
                <div
                  className={
                    series.length > 1 ? "grid grid-cols-1 lg:grid-cols-3 gap-3" : "space-y-2"
                  }
                >
                  {series.map((s) => (
                    <SeriesRow key={s.key} label={s.label}>
                      {/* Label blanked: SeriesRow already prints it, and
                          repeating it in every tooltip is noise. */}
                      <PnLLineChart series={[{ ...s, label: "" }]} formatIsk={formatIsk} />
                    </SeriesRow>
                  ))}
                </div>
              )}
            </div>
          );
        })()}

      {/* Per-item table */}
      <div
        className={`border border-eve-border rounded-sm bg-eve-panel ${
          view === "summary" ? "" : "hidden"
        }`}
      >
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
                    className="group border-t border-eve-border/50 hover:bg-eve-accent/5 cursor-pointer"
                  >
                    <td className="max-w-[260px] px-2 py-1 text-eve-text">
                      <ItemRef
                        typeId={row.type_id}
                        name={row.type_name}
                        iconSize={16}
                        market
                        copyName
                        reveal="hover"
                        marketLabel={t("openMarketHint")}
                      />
                    </td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">{row.buys_qty}</td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">{row.sells_qty}</td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">
                      {row.avg_buy_price ? (
                        <span className="inline-flex items-center justify-end gap-1">
                          {formatIsk(row.avg_buy_price)}
                          <CopyPrice value={row.avg_buy_price} label={t("copyPrice")} reveal="hover" />
                        </span>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="px-2 py-1 text-right font-mono text-eve-dim">
                      {row.avg_sell_price ? (
                        <span className="inline-flex items-center justify-end gap-1">
                          {formatIsk(row.avg_sell_price)}
                          <CopyPrice value={row.avg_sell_price} label={t("copyPrice")} reveal="hover" />
                        </span>
                      ) : (
                        "—"
                      )}
                    </td>
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

// WalletScopePicker — collapsible grouped chip picker. Character chips come
// from AuthStatus; corp-division chips come from the summary response's
// tracking_since keys (any wallet with archive rows). Clicking a chip
// toggles its inclusion; the parent computes the resulting WalletScope
// and refetches. Empty exclusion sets → include_all so newly-authorized
// wallets are pooled automatically.
function WalletScopePicker({
  authCharacters,
  knownCorpDivs,
  excludedCharacters,
  excludedCorpDivs,
  onToggleCharacter,
  onToggleCorpDiv,
  onSelectAll,
}: {
  authCharacters: AuthCharacter[];
  knownCorpDivs: { key: string; corpID: number; div: number }[];
  excludedCharacters: Set<number>;
  excludedCorpDivs: Set<string>;
  onToggleCharacter: (id: number) => void;
  onToggleCorpDiv: (key: string) => void;
  onSelectAll: () => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const totalExcluded = excludedCharacters.size + excludedCorpDivs.size;
  const summary =
    totalExcluded === 0
      ? t("journalScopeAll")
      : t("journalScopeExcluding", { count: totalExcluded });

  if (authCharacters.length === 0 && knownCorpDivs.length === 0) {
    return null;
  }

  return (
    <div className="bg-eve-panel border border-eve-border rounded-sm px-3 py-1.5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 text-xs w-full text-left"
      >
        <span className="text-eve-dim uppercase tracking-wider">
          {t("journalScopeLabel")}
        </span>
        <span className="text-eve-text">{summary}</span>
        <span className="ml-auto text-eve-dim">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="mt-2 pt-2 border-t border-eve-border/40 space-y-2">
          {totalExcluded > 0 && (
            <button
              type="button"
              onClick={onSelectAll}
              className="text-[10px] text-eve-accent hover:underline"
            >
              {t("journalScopeSelectAll")}
            </button>
          )}
          {authCharacters.length > 0 && (
            <div>
              <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-1">
                {t("journalScopeCharacters")}
              </div>
              <div className="flex flex-wrap gap-1">
                {authCharacters.map((c) => {
                  const excluded = excludedCharacters.has(c.character_id);
                  return (
                    <button
                      key={c.character_id}
                      type="button"
                      onClick={() => onToggleCharacter(c.character_id)}
                      className={`inline-flex items-center gap-1 px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
                        excluded
                          ? "border-eve-border/40 bg-eve-dark text-eve-dim opacity-60"
                          : "border-eve-accent/50 bg-eve-accent/10 text-eve-accent"
                      }`}
                      title={excluded ? t("journalScopeInclude") : t("journalScopeExclude")}
                    >
                      <span>{c.character_name}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}
          {knownCorpDivs.length > 0 && (
            <div>
              <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-1">
                {t("journalScopeCorpDivisions")}
              </div>
              <div className="flex flex-wrap gap-1">
                {knownCorpDivs.map((d) => {
                  const excluded = excludedCorpDivs.has(d.key);
                  return (
                    <button
                      key={d.key}
                      type="button"
                      onClick={() => onToggleCorpDiv(d.key)}
                      className={`inline-flex items-center gap-1 px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
                        excluded
                          ? "border-eve-border/40 bg-eve-dark text-eve-dim opacity-60"
                          : "border-eve-accent/50 bg-eve-accent/10 text-eve-accent"
                      }`}
                      title={excluded ? t("journalScopeInclude") : t("journalScopeExclude")}
                    >
                      <span>{t("journalScopeCorpDivLabel", { corp: d.corpID, div: d.div })}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

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

function ChartLayoutBtn({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
        active
          ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
          : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
      }`}
    >
      {children}
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
          <ItemRef
            typeId={typeID}
            name={typeName}
            iconSize={24}
            market
            copyName
            marketLabel={t("openMarketHint")}
          />
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
