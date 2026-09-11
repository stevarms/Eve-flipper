// journalLeaderboard.ts — ranking for the Trade Journal's item leaderboard.
//
// Rows arrive from the journal matcher already priced; all that is left is
// deciding who is on which side and in what order. That lives here rather than
// in the component so the one judgement call it makes — the ROI cost-basis
// floor — is testable.

import type { JournalLeaderboardRow } from "./types";

export type LeaderboardMetric = "isk" | "roi";

/**
 * Minimum cost basis for a row to be eligible for ROI ranking.
 *
 * A 900% return on a 40k ISK flip is arithmetic, not a finding, and a
 * leaderboard that lets it outrank a 30% return on a billion is worse than no
 * leaderboard. Rows below the floor are set aside and counted, never silently
 * dropped.
 */
export const ROI_MIN_COST_BASIS = 1_000_000;

/**
 * Net P&L below which a row is treated as flat.
 *
 * Exact zeros never reach the board, but an item bought and sold at the same
 * price still nets a few ISK of fee residue, and float arithmetic leaves rows
 * whose profit renders as a bare "0". At 1K ISK the cutoff cannot swallow a
 * finding for anyone the leaderboard is for, and rows it removes are counted
 * rather than silently dropped.
 */
export const NEGLIGIBLE_NET_PNL = 1_000;

export interface RankedLeaderboard {
  winners: JournalLeaderboardRow[];
  losers: JournalLeaderboardRow[];
  /** Rows excluded from an ROI ranking for a thin or unknown cost basis. */
  setAside: number;
  /** Rows hidden as flat by `hideNegligible`. */
  negligible: number;
}

export interface RankOptions {
  /** Drop rows whose net P&L is under NEGLIGIBLE_NET_PNL either way. */
  hideNegligible?: boolean;
}

/** Which halves of a row's profit are non-zero. */
export function leaderboardRowSource(row: JournalLeaderboardRow): "trade" | "build" | "both" {
  const hasTrade = row.trade_pnl !== 0 || row.trade_cost > 0;
  const hasBuild = row.manufacture_pnl !== 0 || row.manufacture_cost > 0;
  if (hasTrade && hasBuild) return "both";
  if (hasBuild) return "build";
  return "trade";
}

/**
 * Splits rows into winners and losers and orders each side by `metric`.
 *
 * Zero-P&L rows appear on neither side: they are noise on a leaderboard whose
 * whole question is what made or lost money.
 */
export function rankLeaderboard(
  rows: JournalLeaderboardRow[],
  metric: LeaderboardMetric,
  opts: RankOptions = {},
): RankedLeaderboard {
  const eligible: JournalLeaderboardRow[] = [];
  let setAside = 0;
  let negligible = 0;

  for (const row of rows) {
    if (row.net_pnl === 0) continue;
    // Counted before the ROI floor so the two footers describe disjoint sets
    // and cannot double-count the same row.
    if (opts.hideNegligible && Math.abs(row.net_pnl) < NEGLIGIBLE_NET_PNL) {
      negligible++;
      continue;
    }
    if (metric === "roi" && (row.roi_percent == null || row.cost_basis < ROI_MIN_COST_BASIS)) {
      setAside++;
      continue;
    }
    eligible.push(row);
  }

  const key = (row: JournalLeaderboardRow) =>
    metric === "roi" ? (row.roi_percent ?? 0) : row.net_pnl;

  const winners = eligible.filter((r) => r.net_pnl > 0).sort((a, b) => key(b) - key(a));
  // Losers ascending, so the worst loss is rank 1 on its own side.
  const losers = eligible.filter((r) => r.net_pnl < 0).sort((a, b) => key(a) - key(b));

  return { winners, losers, setAside, negligible };
}
