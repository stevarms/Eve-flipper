import { describe, it, expect } from "vitest";
import {
  NEGLIGIBLE_NET_PNL,
  ROI_MIN_COST_BASIS,
  leaderboardRowSource,
  rankLeaderboard,
} from "./journalLeaderboard";
import type { JournalLeaderboardRow } from "./types";

function row(over: Partial<JournalLeaderboardRow>): JournalLeaderboardRow {
  const base: JournalLeaderboardRow = {
    type_id: 1,
    type_name: "Thing",
    net_pnl: 0,
    cost_basis: 0,
    revenue: 0,
    qty_sold: 0,
    transactions: 0,
    trade_pnl: 0,
    trade_cost: 0,
    manufacture_pnl: 0,
    manufacture_cost: 0,
    roi_percent: null,
  };
  const merged = { ...base, ...over };
  if (merged.roi_percent == null && merged.cost_basis > 0) {
    merged.roi_percent = (merged.net_pnl / merged.cost_basis) * 100;
  }
  return merged;
}

describe("rankLeaderboard", () => {
  const rows = [
    row({ type_id: 1, net_pnl: 100_000_000, cost_basis: 1_000_000_000, trade_pnl: 100_000_000, trade_cost: 1_000_000_000 }),
    row({ type_id: 2, net_pnl: 400_000_000, cost_basis: 8_000_000_000, manufacture_pnl: 400_000_000, manufacture_cost: 8_000_000_000 }),
    row({ type_id: 3, net_pnl: -50_000_000, cost_basis: 200_000_000, trade_pnl: -50_000_000, trade_cost: 200_000_000 }),
    row({ type_id: 4, net_pnl: -5_000_000, cost_basis: 10_000_000, trade_pnl: -5_000_000, trade_cost: 10_000_000 }),
    row({ type_id: 5, net_pnl: 0, cost_basis: 700_000_000 }),
  ];

  it("ranks by ISK with the biggest win and the worst loss first", () => {
    const { winners, losers, setAside } = rankLeaderboard(rows, "isk");
    expect(winners.map((r) => r.type_id)).toEqual([2, 1]);
    expect(losers.map((r) => r.type_id)).toEqual([3, 4]);
    expect(setAside).toBe(0);
  });

  it("ranks by ROI, which reorders both sides", () => {
    // #1 returns 10% on a billion; #2 returns 5% on eight. ISK says 2, ROI says 1.
    const { winners, losers } = rankLeaderboard(rows, "roi");
    expect(winners.map((r) => r.type_id)).toEqual([1, 2]);
    // On the loss side #3 is -50M on 200M (-25%) and #4 is -5M on 10M (-50%),
    // so the small loss leads once the ranking is a ratio.
    expect(losers.map((r) => r.type_id)).toEqual([4, 3]);
  });

  it("leaves zero-P&L rows off both sides", () => {
    const { winners, losers } = rankLeaderboard(rows, "isk");
    expect([...winners, ...losers].map((r) => r.type_id)).not.toContain(5);
  });

  it("sets aside micro-basis rows rather than letting them win on ROI", () => {
    const flip = row({ type_id: 9, net_pnl: 360_000, cost_basis: 40_000 }); // +900%
    const { winners, setAside } = rankLeaderboard([...rows, flip], "roi");
    expect(winners.map((r) => r.type_id)).not.toContain(9);
    expect(setAside).toBe(1);
    // The same row is a legitimate ISK-ranked entry — it is only the ratio
    // that is meaningless.
    expect(rankLeaderboard([...rows, flip], "isk").winners.map((r) => r.type_id)).toContain(9);
  });

  it("keeps flat rows until hideNegligible asks for them to go", () => {
    // Bought and sold at the same price, less a few ISK of broker residue --
    // renders as "0" but is not exactly zero, so the zero check misses it.
    const fee = row({ type_id: 7, net_pnl: -12.4, cost_basis: 50_000_000 });
    const shown = rankLeaderboard([...rows, fee], "isk");
    expect(shown.losers.map((r) => r.type_id)).toContain(7);
    expect(shown.negligible).toBe(0);

    const hidden = rankLeaderboard([...rows, fee], "isk", { hideNegligible: true });
    expect(hidden.losers.map((r) => r.type_id)).not.toContain(7);
    expect(hidden.negligible).toBe(1);
    // Everything that was a real finding is still on the board.
    expect(hidden.winners.map((r) => r.type_id)).toEqual([2, 1]);
    expect(hidden.losers.map((r) => r.type_id)).toEqual([3, 4]);
  });

  it("counts a flat row once, as flat rather than as set aside", () => {
    // A flat row also has a thin ROI denominator by any sane reading, so the
    // two footers would double-count it if the checks were the other way round.
    const flat = row({ type_id: 6, net_pnl: NEGLIGIBLE_NET_PNL - 1, cost_basis: 400 });
    const { negligible, setAside } = rankLeaderboard([flat], "roi", { hideNegligible: true });
    expect(negligible).toBe(1);
    expect(setAside).toBe(0);
  });

  it("never ranks a row whose ROI is unknown", () => {
    const orphan = row({ type_id: 8, net_pnl: 700_000_000, cost_basis: 0, roi_percent: null });
    const { winners, setAside } = rankLeaderboard([orphan], "roi");
    expect(winners).toHaveLength(0);
    expect(setAside).toBe(1);
    // …and its cost basis is genuinely below the floor, not merely small.
    expect(orphan.cost_basis).toBeLessThan(ROI_MIN_COST_BASIS);
  });
});

describe("leaderboardRowSource", () => {
  it("labels a bought-and-flipped row as trade", () => {
    expect(leaderboardRowSource(row({ trade_pnl: 5, trade_cost: 10 }))).toBe("trade");
  });

  it("labels a built row as build", () => {
    expect(leaderboardRowSource(row({ manufacture_pnl: 5, manufacture_cost: 10 }))).toBe("build");
  });

  it("labels a row with both halves as both", () => {
    expect(
      leaderboardRowSource(row({ trade_pnl: 5, trade_cost: 10, manufacture_pnl: 7, manufacture_cost: 12 })),
    ).toBe("both");
  });

  it("still labels a break-even build as build, on its cost basis alone", () => {
    expect(leaderboardRowSource(row({ manufacture_pnl: 0, manufacture_cost: 10 }))).toBe("build");
  });
});
