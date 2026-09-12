import { useMemo, useState } from "react";
import { CopyButton } from "@/components/ui/CopyButton";
import { OpenMarketButton } from "@/components/ui/OpenMarketButton";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { type TranslationKey } from "../../lib/i18n";
import {
  NEGLIGIBLE_NET_PNL,
  ROI_MIN_COST_BASIS,
  leaderboardRowSource,
  rankLeaderboard,
  type LeaderboardMetric,
} from "../../lib/journalLeaderboard";
import type { JournalLeaderboardRow } from "../../lib/types";

// PnLLeaderboard — winners and losers of the period, side by side.
//
// It replaces a flat top-20 table that showed one side at a time, which made
// the obvious question ("what am I losing money on?") a mode switch away, and
// hid the thing the journal engine knows and the old P&L engine did not:
// whether an item earned its ISK by being flipped or by being built.

const PAGE_SIZES = [10, 25, Number.MAX_SAFE_INTEGER];

type Props = {
  rows: JournalLeaderboardRow[];
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
};

export function PnLLeaderboard({ rows, formatIsk, t }: Props) {
  const [metric, setMetric] = useState<LeaderboardMetric>("isk");
  const [pageIdx, setPageIdx] = useState(0);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  // On by default: a flat row answers neither question the board asks, and
  // the footer says how many are being held back.
  const [hideNegligible, setHideNegligible] = useState(true);

  const { winners, losers, setAside, negligible } = useMemo(
    () => rankLeaderboard(rows, metric, { hideNegligible }),
    [rows, metric, hideNegligible],
  );

  const toggle = (typeID: number) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(typeID)) next.add(typeID);
      return next;
    });

  if (rows.length === 0) {
    return <div className="text-center text-eve-dim text-xs py-4">{t("pnlNoData")}</div>;
  }

  const limit = PAGE_SIZES[pageIdx];
  const hasMore = winners.length > limit || losers.length > limit;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-[10px] text-eve-dim uppercase tracking-wider">
          {t("journalLeaderboard")}
        </div>
        <div className="flex gap-1">
          <MetricBtn
            active={metric === "isk"}
            label={t("leaderboardByIsk")}
            onClick={() => setMetric("isk")}
          />
          <MetricBtn
            active={metric === "roi"}
            label={t("leaderboardByRoi")}
            onClick={() => setMetric("roi")}
          />
          <span className="w-px self-stretch bg-eve-border mx-1" />
          <MetricBtn
            active={hideNegligible}
            label={t("leaderboardHideFlat")}
            title={t("leaderboardHideFlatHint", { amount: formatIsk(NEGLIGIBLE_NET_PNL) })}
            onClick={() => setHideNegligible((v) => !v)}
          />
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <LeaderboardColumn
          title={`${t("leaderboardWinners")} (${winners.length})`}
          rows={winners.slice(0, limit)}
          expanded={expanded}
          onToggle={toggle}
          formatIsk={formatIsk}
          t={t}
        />
        <LeaderboardColumn
          title={`${t("leaderboardLosers")} (${losers.length})`}
          rows={losers.slice(0, limit)}
          expanded={expanded}
          onToggle={toggle}
          formatIsk={formatIsk}
          t={t}
        />
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 text-[10px] text-eve-dim">
        <div className="flex flex-wrap gap-x-3">
          {metric === "roi" && setAside > 0 && (
            <span>
              {t("leaderboardSetAside", {
                count: setAside,
                basis: formatIsk(ROI_MIN_COST_BASIS),
              })}
            </span>
          )}
          {negligible > 0 && (
            <span>{t("leaderboardFlatHidden", { count: negligible })}</span>
          )}
        </div>
        {hasMore && pageIdx < PAGE_SIZES.length - 1 && (
          <button
            type="button"
            onClick={() => setPageIdx((i) => i + 1)}
            className="px-2 py-0.5 rounded-sm border border-eve-border bg-eve-dark text-eve-dim hover:text-eve-text"
          >
            {pageIdx === 0
              ? t("leaderboardShowMore", { count: PAGE_SIZES[1] })
              : t("leaderboardShowAll")}
          </button>
        )}
      </div>
    </div>
  );
}

function LeaderboardColumn({
  title,
  rows,
  expanded,
  onToggle,
  formatIsk,
  t,
}: {
  title: string;
  rows: JournalLeaderboardRow[];
  expanded: Set<number>;
  onToggle: (typeID: number) => void;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  // Scaled within the column: a loss column beside a much larger win column
  // would otherwise render as a row of empty bars.
  const maxAbs = Math.max(...rows.map((r) => Math.abs(r.net_pnl)), 1);

  return (
    <div className="border border-eve-border rounded-sm overflow-hidden">
      <div className="bg-eve-panel px-3 py-1.5 text-[10px] text-eve-dim uppercase tracking-wider">
        {title}
      </div>
      {rows.length === 0 ? (
        <div className="text-center text-eve-dim text-xs py-4">{t("pnlNoData")}</div>
      ) : (
        <ul>
          {rows.map((row, idx) => (
            <LeaderboardRow
              key={row.type_id}
              rank={idx + 1}
              row={row}
              maxAbs={maxAbs}
              open={expanded.has(row.type_id)}
              onToggle={() => onToggle(row.type_id)}
              formatIsk={formatIsk}
              t={t}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function LeaderboardRow({
  rank,
  row,
  maxAbs,
  open,
  onToggle,
  formatIsk,
  t,
}: {
  rank: number;
  row: JournalLeaderboardRow;
  maxAbs: number;
  open: boolean;
  onToggle: () => void;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  const isProfit = row.net_pnl >= 0;
  const barPct = (Math.abs(row.net_pnl) / maxAbs) * 100;
  const source = leaderboardRowSource(row);

  // On a mixed row the bar is split by where the ISK came from, so "half of
  // this came from building" is readable without opening anything.
  const splitDenom = Math.abs(row.trade_pnl) + Math.abs(row.manufacture_pnl);
  const tradeShare =
    source === "both" && splitDenom > 0
      ? Math.abs(row.trade_pnl) / splitDenom
      : source === "trade"
        ? 1
        : 0;

  return (
    <li className="group border-t border-eve-border/50 first:border-t-0">
      <div className="flex items-start">
      <button
        type="button"
        onClick={onToggle}
        className="min-w-0 flex-1 px-3 py-2 text-left hover:bg-eve-panel/50 transition-colors"
      >
        <div className="flex items-center gap-2">
          <span className="w-5 text-right text-[10px] text-eve-dim tabular-nums">{rank}</span>
          <TypeIcon typeId={row.type_id} size={20} />
          <span className="flex-1 truncate text-xs text-eve-text">
            {row.type_name || `Type #${row.type_id}`}
          </span>
          <SourceChip source={source} t={t} />
          <span
            className={`text-xs tabular-nums ${isProfit ? "text-eve-profit" : "text-eve-error"}`}
          >
            {isProfit ? "+" : ""}
            {formatIsk(row.net_pnl)}
          </span>
          <span className="text-[10px] text-eve-dim">{open ? "▾" : "▸"}</span>
        </div>
        <div className="mt-1 flex items-center gap-2 pl-7">
          <div className="flex-1 h-1.5 bg-eve-dark rounded-full overflow-hidden flex">
            <div
              className={`h-full ${isProfit ? "bg-emerald-500" : "bg-red-500"}`}
              style={{ width: `${barPct * tradeShare}%` }}
            />
            <div
              className={`h-full ${isProfit ? "bg-sky-500" : "bg-orange-500"}`}
              style={{ width: `${barPct * (1 - tradeShare)}%` }}
            />
          </div>
          <span className="text-[10px] text-eve-dim tabular-nums w-16 text-right">
            {row.roi_percent == null ? "—" : `${row.roi_percent.toFixed(1)}%`}
          </span>
        </div>
      </button>
        {/* Outside the toggle button: a button inside a button is invalid
            HTML, and any click would have expanded the row instead. */}
        <span className="flex shrink-0 items-center gap-1 py-2 pr-2">
          <OpenMarketButton typeId={row.type_id} reveal="hover" label={t("openMarketHint")} />
          {row.type_name && (
            <CopyButton text={row.type_name} label={t("copyItem")} reveal="hover" />
          )}
        </span>
      </div>

      {open && (
        <div className="px-3 pb-2 pl-10 text-[10px] text-eve-dim space-y-0.5">
          <div className="flex flex-wrap gap-x-4">
            <span>
              {t("leaderboardCostBasis")}: {formatIsk(row.cost_basis)}
            </span>
            <span>
              {t("leaderboardRevenue")}: {formatIsk(row.revenue)}
            </span>
            <span>
              {t("pnlItemSold")}: {row.qty_sold.toLocaleString()}
            </span>
            <span>
              {t("pnlItemTxns")}: {row.transactions}
            </span>
          </div>
          {(source === "trade" || source === "both") && (
            <SplitLine
              label={t("leaderboardSourceTrade")}
              pnl={row.trade_pnl}
              cost={row.trade_cost}
              formatIsk={formatIsk}
              t={t}
            />
          )}
          {(source === "build" || source === "both") && (
            <SplitLine
              label={t("leaderboardSourceBuild")}
              pnl={row.manufacture_pnl}
              cost={row.manufacture_cost}
              formatIsk={formatIsk}
              t={t}
            />
          )}
        </div>
      )}
    </li>
  );
}

function SplitLine({
  label,
  pnl,
  cost,
  formatIsk,
  t,
}: {
  label: string;
  pnl: number;
  cost: number;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  const roi = cost > 0 ? (pnl / cost) * 100 : null;
  return (
    <div>
      <span className="text-eve-text">{label}</span>{" "}
      <span className={pnl >= 0 ? "text-eve-profit" : "text-eve-error"}>
        {pnl >= 0 ? "+" : ""}
        {formatIsk(pnl)}
      </span>{" "}
      <span>
        {t("leaderboardOnBasis", {
          basis: formatIsk(cost),
          roi: roi == null ? "—" : `${roi.toFixed(1)}%`,
        })}
      </span>
    </div>
  );
}

function SourceChip({
  source,
  t,
}: {
  source: "trade" | "build" | "both";
  t: (key: TranslationKey) => string;
}) {
  const cls =
    source === "trade"
      ? "border-emerald-500/40 text-emerald-400"
      : source === "build"
        ? "border-sky-500/40 text-sky-400"
        : "border-eve-border text-eve-dim";
  const label =
    source === "trade"
      ? t("leaderboardSourceTrade")
      : source === "build"
        ? t("leaderboardSourceBuild")
        : t("leaderboardSourceBoth");
  return (
    <span className={`px-1 py-px rounded-sm border text-[9px] uppercase tracking-wider ${cls}`}>
      {label}
    </span>
  );
}

function MetricBtn({
  active,
  label,
  title,
  onClick,
}: {
  active: boolean;
  label: string;
  title?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
        active
          ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
          : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
      }`}
    >
      {label}
    </button>
  );
}
