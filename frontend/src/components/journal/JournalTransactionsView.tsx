import { useEffect, useMemo, useRef, useState } from "react";
import {
  getJournalTransactions,
  type JournalAnalyticsSource,
  type JournalFIFOMode,
  type JournalLot,
  type WalletScope,
} from "../../lib/api";
import { useI18n, type TranslationKey } from "../../lib/i18n";
import {
  sortTransactions,
  toTransactionRows,
  type SortDir,
  type TransactionRow,
  type TxnSortKey,
} from "../../lib/journalTransactions";
import { SortableTH } from "./PnLPrimitives";
import { LoadingBlock } from "@/components/ui/LoadingBlock";

// JournalTransactionsView — one row per sale, with what it cost, what the
// fees were, and what was kept.
//
// The Transactions tab in the character popup is a raw ESI wallet dump: order
// type, item, price, qty. It says nothing the game client doesn't, because it
// has no idea what any of those items cost to acquire. The FIFO matcher does,
// and has all along — every column here except margin is a field the engine
// already emits on TradeJournalLot. What was missing was a way to ask for more
// than one item's worth at a time.
//
// Same endpoint as the per-item drawer in Summary, minus the type_id, so a
// profit here and a profit there are the same matcher's answer by construction.

/**
 * How many sales to pull per query.
 *
 * A busy 90-day window is tens of thousands of matched sells. The server sorts
 * by sell date before capping, so this always keeps the most recent slice, and
 * it reports the pre-cap count — which the footer states rather than quietly
 * presenting a truncated list as the whole picture.
 */
const FETCH_LIMIT = 2000;

const PAGE_SIZES = [40, 100, Number.MAX_SAFE_INTEGER] as const;

/** Debounce for the text and number filters: they refetch, so keystrokes cost. */
const FILTER_DEBOUNCE_MS = 350;

interface Props {
  scope: WalletScope;
  period: number | "all";
  fifoMode: JournalFIFOMode;
  /** The tab's Source selector. Drives the server-side filter. */
  source: JournalAnalyticsSource;
  feeOverride: { salesTax: number; brokerFee: number } | null;
  /** Bumped by the parent after a sync so this refetches too. */
  reloadToken?: number;
  formatIsk: (v: number) => string;
  formatIskSigned: (v: number) => string;
}

export function JournalTransactionsView({
  scope,
  period,
  fifoMode,
  source,
  feeOverride,
  reloadToken,
  formatIsk,
  formatIskSigned,
}: Props) {
  const { t } = useI18n();
  const [lots, setLots] = useState<JournalLot[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Typed values and the values actually queried, kept apart so the input stays
  // responsive while the request behind it lags a third of a second.
  const [itemInput, setItemInput] = useState("");
  const [minProfitInput, setMinProfitInput] = useState("");
  const [query, setQuery] = useState("");
  const [minProfit, setMinProfit] = useState(0);

  const [sortKey, setSortKey] = useState<TxnSortKey>("sell_date");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [pageSizeIdx, setPageSizeIdx] = useState(0);
  const [page, setPage] = useState(0);

  useEffect(() => {
    const id = setTimeout(() => {
      setQuery(itemInput.trim());
      const n = Number.parseFloat(minProfitInput);
      setMinProfit(Number.isFinite(n) && n > 0 ? n : 0);
    }, FILTER_DEBOUNCE_MS);
    return () => clearTimeout(id);
  }, [itemInput, minProfitInput]);

  const controllerRef = useRef<AbortController | null>(null);
  useEffect(() => {
    controllerRef.current?.abort();
    const c = new AbortController();
    controllerRef.current = c;
    setLoading(true);
    setError(null);
    getJournalTransactions({
      scope,
      days: period,
      fifoMode,
      source,
      q: query,
      minProfit,
      limit: FETCH_LIMIT,
      salesTax: feeOverride?.salesTax,
      brokerFee: feeOverride?.brokerFee,
    })
      .then((resp) => {
        if (c.signal.aborted) return;
        setLots(resp.lots ?? []);
        setTotal(resp.total ?? resp.lots?.length ?? 0);
        // A narrower filter with fewer pages must not leave the view parked on
        // a page that no longer exists.
        setPage(0);
      })
      .catch((e) => {
        if (!c.signal.aborted) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!c.signal.aborted) setLoading(false);
      });
    return () => c.abort();
  }, [scope, period, fifoMode, source, query, minProfit, feeOverride, reloadToken]);

  const rows = useMemo(() => toTransactionRows(lots), [lots]);
  const sorted = useMemo(() => sortTransactions(rows, sortKey, sortDir), [rows, sortKey, sortDir]);

  const pageSize = PAGE_SIZES[pageSizeIdx];
  const pageCount = Math.max(1, Math.ceil(sorted.length / pageSize));
  const pageRows = useMemo(
    () => sorted.slice(page * pageSize, page * pageSize + pageSize),
    [sorted, page, pageSize],
  );

  const toggleSort = (k: TxnSortKey) => {
    if (k === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(k);
      // Names read alphabetically; every other column is a magnitude, and the
      // interesting end of a magnitude is the big end.
      setSortDir(k === "type_name" || k === "source" ? "asc" : "desc");
    }
    setPage(0);
  };

  const exportCSV = () => {
    const esc = (v: string) => (/[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
    const header = [
      t("journalDrawerSellDate"),
      t("colItem"),
      t("journalDrawerSource"),
      t("journalTxnColUnitBuy"),
      t("journalTxnColUnitSell"),
      t("journalTxnColUnits"),
      t("journalTxnColTotalBuy"),
      t("journalTxnColTotalSell"),
      t("journalTxnColBrokerBuy"),
      t("journalTxnColBrokerSell"),
      t("journalTxnColTax"),
      t("journalTxnColMargin"),
      t("journalTxnColProfit"),
    ]
      .map(esc)
      .join(",");
    // Every row of the current sort, not just the visible page — a spreadsheet
    // export that stops at 40 rows is a worse spreadsheet.
    const body = sorted.map((r) =>
      [
        esc(r.sell_date ?? ""),
        esc(r.type_name || `Type #${r.type_id}`),
        esc(r.source),
        String(r.buy_unit_price ?? ""),
        String(r.sell_unit_price),
        String(r.matched_qty),
        String(r.cost || ""),
        String(r.sell_gross),
        String(r.buy_fees ?? ""),
        String(r.sell_broker_fee ?? 0),
        String(r.sell_tax ?? 0),
        r.marginPercent == null ? "" : r.marginPercent.toFixed(2),
        String(r.net_profit),
      ].join(","),
    );
    const blob = new Blob(["﻿", header, "\n", body.join("\n")], {
      type: "text/csv;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    const periodTag = period === "all" ? "all" : `${period}d`;
    link.download = `eve-journal-transactions-${periodTag}-${new Date()
      .toISOString()
      .slice(0, 10)}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };

  if (loading && lots.length === 0) return <LoadingBlock label={`${t("loading")}…`} fill />;
  if (error) {
    return (
      <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
        {error}
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <input
            value={itemInput}
            onChange={(e) => setItemInput(e.target.value)}
            placeholder={t("journalTxnFilterItem")}
            className="bg-eve-dark border border-eve-border rounded-sm px-2 py-1 text-[11px] text-eve-text w-44"
          />
          <label className="flex items-center gap-1 text-[11px] text-eve-dim">
            <span title={t("journalTxnFilterMinProfitHint")}>{t("journalTxnFilterMinProfit")}</span>
            <input
              value={minProfitInput}
              onChange={(e) => setMinProfitInput(e.target.value)}
              inputMode="numeric"
              placeholder="0"
              className="bg-eve-dark border border-eve-border rounded-sm px-2 py-1 text-[11px] text-eve-text w-24"
            />
          </label>
          {loading && <span className="text-[10px] text-eve-dim">{t("journalLoading")}</span>}
        </div>
        <div className="flex items-center gap-2">
          <select
            value={pageSizeIdx}
            onChange={(e) => {
              setPageSizeIdx(Number(e.target.value));
              setPage(0);
            }}
            className="bg-eve-dark border border-eve-border rounded-sm px-1 py-1 text-[11px] text-eve-text"
          >
            <option value={0}>40</option>
            <option value={1}>100</option>
            <option value={2}>{t("leaderboardShowAll")}</option>
          </select>
          <button
            type="button"
            onClick={exportCSV}
            disabled={sorted.length === 0}
            className="px-2 py-1 text-xs rounded-sm border border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text hover:border-eve-accent/50 disabled:opacity-40"
            title={t("journalExportCsvHint")}
          >
            {t("journalExportCsv")}
          </button>
        </div>
      </div>

      {sorted.length === 0 ? (
        <div className="text-center text-eve-dim text-xs py-8">{t("pnlNoData")}</div>
      ) : (
        <div className="overflow-x-auto border border-eve-border rounded-sm">
          <table className="w-full text-[11px] whitespace-nowrap">
            <thead className="text-eve-dim bg-eve-panel">
              <tr>
                <SortableTH
                  label={t("journalDrawerSellDate")}
                  k="sell_date"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="left"
                />
                <SortableTH
                  label={t("colItem")}
                  k="type_name"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="left"
                />
                <SortableTH
                  label={t("journalDrawerSource")}
                  k="source"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="left"
                />
                <SortableTH
                  label={t("journalTxnColUnitBuy")}
                  k="buy_unit_price"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColUnitSell")}
                  k="sell_unit_price"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColUnits")}
                  k="matched_qty"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColTotalBuy")}
                  k="cost"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColTotalSell")}
                  k="sell_gross"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColBrokerBuy")}
                  k="buy_fees"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColBrokerSell")}
                  k="sell_broker_fee"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColTax")}
                  k="sell_tax"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("journalTxnColMargin")}
                  k="marginPercent"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                  title={t("journalTxnMarginHint")}
                />
                <SortableTH
                  label={t("journalTxnColProfit")}
                  k="net_profit"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
              </tr>
            </thead>
            <tbody>
              {pageRows.map((r) => (
                <TxnRow
                  key={`${r.sell_txn_id}-${r.buy_txn_id ?? r.manufacture_job_id ?? 0}-${r.sell_date}`}
                  row={r}
                  formatIsk={formatIsk}
                  formatIskSigned={formatIskSigned}
                  t={t}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2 text-[10px] text-eve-dim">
        <span>{t("journalTxnShowingOf", { shown: sorted.length, total })}</span>
        {pageCount > 1 && (
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0}
              className="px-2 py-0.5 rounded-sm border border-eve-border bg-eve-dark hover:text-eve-text disabled:opacity-40"
            >
              ‹
            </button>
            <span className="tabular-nums">
              {page + 1} / {pageCount}
            </span>
            <button
              type="button"
              onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
              disabled={page >= pageCount - 1}
              className="px-2 py-0.5 rounded-sm border border-eve-border bg-eve-dark hover:text-eve-text disabled:opacity-40"
            >
              ›
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function TxnRow({
  row,
  formatIsk,
  formatIskSigned,
  t,
}: {
  row: TransactionRow;
  formatIsk: (v: number) => string;
  formatIskSigned: (v: number) => string;
  t: (key: TranslationKey) => string;
}) {
  // An unmatched sell is not a break-even trade, it is a trade we cannot price.
  // Its cost, margin and profit cells say "—" rather than a confident zero.
  const priced = row.source !== "orphan" && row.cost > 0;
  const tint = !priced
    ? ""
    : row.net_profit >= 0
      ? "bg-emerald-500/[0.06]"
      : "bg-red-500/[0.06]";

  return (
    <tr className={`border-t border-eve-border/50 ${tint}`}>
      <td className="px-2 py-1 text-eve-dim">{(row.sell_date ?? "").slice(0, 10)}</td>
      <td className="px-2 py-1">
        <span className="flex items-center gap-1.5">
          <img
            src={`https://images.evetech.net/types/${row.type_id}/icon?size=32`}
            alt=""
            className="w-4 h-4"
          />
          <span className="text-eve-text">{row.type_name || `Type #${row.type_id}`}</span>
        </span>
      </td>
      <td className="px-2 py-1">
        <SourceBadge source={row.source} t={t} />
      </td>
      <td className="px-2 py-1 text-right text-eve-dim tabular-nums">
        {priced ? formatIsk(row.buy_unit_price ?? 0) : "—"}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim tabular-nums">
        {formatIsk(row.sell_unit_price)}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim tabular-nums">
        {row.matched_qty.toLocaleString()}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim tabular-nums">
        {priced ? formatIsk(row.cost) : "—"}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim tabular-nums">{formatIsk(row.sell_gross)}</td>
      <FeeCell isk={row.buy_fees ?? 0} base={row.cost} formatIsk={formatIsk} />
      <FeeCell isk={row.sell_broker_fee ?? 0} base={row.sell_gross} formatIsk={formatIsk} />
      <FeeCell isk={row.sell_tax ?? 0} base={row.sell_gross} formatIsk={formatIsk} />
      <td
        className={`px-2 py-1 text-right tabular-nums ${
          row.marginPercent == null
            ? "text-eve-dim"
            : row.marginPercent >= 0
              ? "text-eve-profit"
              : "text-eve-error"
        }`}
        title={row.marginPercent == null ? t("journalTxnNoCostBasis") : undefined}
      >
        {row.marginPercent == null ? "—" : `${row.marginPercent.toFixed(1)}%`}
      </td>
      <td
        className={`px-2 py-1 text-right font-mono ${
          priced ? (row.net_profit >= 0 ? "text-eve-profit" : "text-eve-error") : "text-eve-dim"
        }`}
        title={priced ? undefined : t("journalTxnNoCostBasis")}
      >
        {priced ? formatIskSigned(row.net_profit) : "—"}
      </td>
    </tr>
  );
}

/** A fee in ISK with the rate it worked out to underneath. */
function FeeCell({
  isk,
  base,
  formatIsk,
}: {
  isk: number;
  base: number;
  formatIsk: (v: number) => string;
}) {
  const pct = base > 0 ? (isk / base) * 100 : null;
  return (
    <td className="px-2 py-1 text-right text-eve-dim tabular-nums">
      <div>{isk > 0 ? formatIsk(isk) : "—"}</div>
      {pct != null && isk > 0 && (
        <div className="text-[9px] text-eve-dim/70">{pct.toFixed(2)}%</div>
      )}
    </td>
  );
}

function SourceBadge({
  source,
  t,
}: {
  source: TransactionRow["source"];
  t: (key: TranslationKey) => string;
}) {
  return (
    <span
      className={`inline-flex px-1.5 py-0.5 rounded-sm text-[9px] uppercase tracking-wider border ${
        source === "trade"
          ? "border-sky-500/50 bg-sky-500/10 text-sky-300"
          : source === "manufacture"
            ? "border-amber-500/50 bg-amber-500/10 text-amber-300"
            : "border-eve-dim/50 bg-eve-dim/10 text-eve-dim"
      }`}
    >
      {source === "trade"
        ? t("journalLotBadgeTrade")
        : source === "manufacture"
          ? t("journalLotBadgeMfg")
          : t("journalLotBadgeOrphan")}
    </span>
  );
}
