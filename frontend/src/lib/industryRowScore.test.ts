import { describe, expect, it } from "vitest";
import {
  ASSUMED_SLOTS,
  buildScoreTooltip,
  calcRowScore,
  computeScoreBands,
  PRICE_CRASHING_MULT,
  PRICE_FALLING_MULT,
  scoreBandFor,
} from "./industryRowScore";
import { suggestRunsForRow } from "./industryBatchCommit";
import { en } from "./locale/en";
import type { TranslationKey } from "./i18n";
import type { ProfitableScanRow } from "./types";

// Same substitution the I18nProvider does, so the assertions below are made
// against the strings a user actually reads rather than a stand-in.
const t = (key: TranslationKey, params?: Record<string, string | number>) => {
  let str: string = en[key] ?? key;
  if (params) {
    for (const [k, v] of Object.entries(params)) str = str.replace("{" + k + "}", String(v));
  }
  return str;
};

/** The verdict paragraph, without the Makes/Batch/Profit/Cycle/Trust block. */
const verdictOf = (tip: string) => tip.split("\n\n")[0];

// ProfitableScanRow has ~60 columns; spelling out only the ones the score
// reads keeps each row's shape readable as a market scenario.
function row(parts: Partial<ProfitableScanRow>): ProfitableScanRow {
  return {
    type_id: 1,
    type_name: "BP",
    product_type_id: 2,
    product_name: "Product",
    owned_quantity: 1,
    is_bpo: true,
    available_runs: 0,
    runs: 1,
    manufacturing_time: 3600,
    optimal_build_cost: 0,
    output_qty_per_run: 1,
    scan_mode: "t1_mfg",
    profit: 1_000_000,
    profit_percent: 25,
    period_margin: 15,
    ask_depth_units: 0,
    ask_orders_count: 2,
    ...parts,
  } as unknown as ProfitableScanRow;
}

// --- Scenarios, each hand-computable against the formula in the module -----

/**
 * Drone-shaped: invents in 10-run copies, trades steadily, clears its batch
 * long before the build queue would be the problem.
 *   demand 50/day x 10% x 90d = 450 runs, capital 3b / 500k = 6,000 runs
 *   -> tier 400, 400 units
 *   fill 400 / (50 x 10%) = 80d, build 1,200s x 400 / 86,400 / 10 = 0.56d
 *   gross 850k x 400 = 340m -> 4.25m ISK/day, no penalties
 */
const steadyMover = row({
  scan_mode: "t2_invention",
  output_bpc_runs: 10,
  runs: 100,
  manufacturing_time: 100 * 1200,
  optimal_build_cost: 500_000 * 100,
  product_daily_volume: 50,
  regional_avg_price_30d: 1_000_000,
  unit_ask_price: 880_000,
  unit_profit_30d: 850_000,
});

/**
 * Same 400-unit batch, but the market swallows it in under a day and the job
 * queue is what you actually wait on: 8,640s/run x 400 / 10 slots = 4 days.
 */
const buildBound = row({
  runs: 1,
  manufacturing_time: 8640,
  optimal_build_cost: 500_000,
  product_daily_volume: 5_000,
  regional_avg_price_30d: 400_000,
  unit_ask_price: 400_000,
  unit_profit_30d: 100_000,
});

/**
 * The failure mode the column exists to fix: one optimistic seller 3.1x above
 * what the item has actually traded at, on something that moves once every
 * two days. Headline profit is enormous, real earnings are not.
 */
const moonPrice = row({
  runs: 1,
  manufacturing_time: 3600,
  optimal_build_cost: 1_000_000,
  product_daily_volume: 0.5,
  regional_avg_price_30d: 1_000_000,
  unit_ask_price: 3_100_000,
  unit_profit_30d: 200_000,
  profit: 50_000_000,
  profit_percent: 300,
});

/**
 * Widow shape: ~6b of capital committed to make ~75m. Real money in absolute
 * terms, but a 1.3% return means an ordinary move in the price turns the batch
 * into a loss, and it is 6b that cannot be spent on anything else meanwhile.
 * Nothing else about the row is wrong — it isolates the capital factor.
 *   demand 5/day x 10% x 90d = 45 runs, capital 3b / 6b = 0.5 runs -> 1 run
 *   fill 1 / (5 x 10%) = 2d -> 37.5m ISK/day before capital
 */
const capitalHog = row({
  runs: 1,
  manufacturing_time: 3600,
  optimal_build_cost: 6_000_000_000,
  product_daily_volume: 5,
  regional_avg_price_30d: 8_000_000_000,
  unit_ask_price: 8_000_000_000,
  unit_profit_30d: 75_000_000,
});

/** 40m earned on 400m committed — a 10% return, exactly half the target. */
const thinReturn = row({
  runs: 1,
  manufacturing_time: 3600,
  optimal_build_cost: 1_000_000,
  product_daily_volume: 50,
  regional_avg_price_30d: 2_000_000,
  unit_ask_price: 2_000_000,
  unit_profit_30d: 100_000,
});

/** No cost data at all, so there is no return on capital to judge. */
const unknownCapital = row({
  product_daily_volume: 50,
  regional_avg_price_30d: 1_000_000,
  unit_ask_price: 1_000_000,
  unit_profit_30d: 100_000,
  optimal_build_cost: 0,
});

describe("calcRowScore", () => {
  it("scores the batch the Planned column would commit, not one run", () => {
    const s = calcRowScore(steadyMover)!;
    expect(s.runs).toBe(suggestRunsForRow(steadyMover).runs);
    expect(s.runs).toBe(400);
    expect(s.units).toBe(400);
    expect(s.grossProfit).toBe(340_000_000);
    expect(s.capital).toBe(200_000_000);
  });

  it("binds a fast-building, slow-selling row on sell-through", () => {
    const s = calcRowScore(steadyMover)!;
    expect(s.fillDays).toBeCloseTo(80, 6);
    expect(s.buildDays).toBeCloseTo(0.5556, 3);
    expect(s.boundBy).toBe("fill");
    expect(s.cycleDays).toBeCloseTo(80, 6);
    expect(s.iskPerDay).toBeCloseTo(4_250_000, 3);
    expect(s.reliability).toBe(1);
    expect(s.score).toBeCloseTo(4_250_000, 3);
  });

  it("binds a fast-selling, slow-building row on build time", () => {
    const s = calcRowScore(buildBound)!;
    expect(s.units).toBe(400);
    expect(s.fillDays).toBeCloseTo(0.8, 6);
    expect(s.buildDays).toBeCloseTo(4, 6);
    expect(s.boundBy).toBe("build");
    expect(s.cycleDays).toBeCloseTo(4, 6);
    expect(s.score).toBeCloseTo(10_000_000, 3);
  });

  it("never lets a cycle come out shorter than a day", () => {
    // Build and fill both clear in hours; a 0.1-day cycle would imply ten
    // batches a day, which no amount of slots makes true.
    const s = calcRowScore(
      row({
        manufacturing_time: 60,
        product_daily_volume: 100_000,
        regional_avg_price_30d: 1_000,
        unit_ask_price: 1_000,
        unit_profit_30d: 100,
        optimal_build_cost: 900,
      }),
    )!;
    expect(s.fillDays).toBeLessThan(1);
    expect(s.buildDays).toBeLessThan(1);
    expect(s.cycleDays).toBe(1);
  });

  it("returns null when there is no 30d trade history to rank on", () => {
    expect(
      calcRowScore(row({ product_daily_volume: 0, regional_avg_price_30d: 1_000_000, unit_profit_30d: 5 })),
    ).toBeNull();
    expect(
      calcRowScore(row({ product_daily_volume: 50, regional_avg_price_30d: 0, unit_profit_30d: 5 })),
    ).toBeNull();
    expect(calcRowScore(row({ product_daily_volume: 50, regional_avg_price_30d: 1_000_000 }))).toBeNull();
  });

  it("applies each market-quality penalty exactly once", () => {
    const s = calcRowScore(
      row({
        product_daily_volume: 0.5,
        regional_avg_price_30d: 1_000_000,
        unit_ask_price: 2_000_000,
        unit_profit_30d: 100_000,
        optimal_build_cost: 1_000_000,
        ask_depth_units: 100,
        ask_orders_count: 30,
        profit: -1_000,
        profit_percent: -3,
        period_margin: -5,
      }),
    )!;
    const keys = s.penalties.map((p) => p.key);
    expect(keys).toHaveLength(new Set(keys).size);
    expect(new Set(keys)).toEqual(
      new Set(["askDepth", "crowdedBook", "askAboveAvg", "subDailyChurn", "negProfit", "negPeriodMargin"]),
    );
    // 0.85 x 0.85 x 0.8 x 0.85 x 0.5 x 0.6
    expect(s.reliability).toBeCloseTo(0.1474, 4);
  });

  it("treats a thin margin as an alternative to an outright loss, not an extra", () => {
    const s = calcRowScore(
      row({
        product_daily_volume: 50,
        regional_avg_price_30d: 1_000_000,
        unit_ask_price: 1_000_000,
        unit_profit_30d: 10_000,
        optimal_build_cost: 500_000,
        profit: 100,
        profit_percent: 1.5,
      }),
    )!;
    expect(s.penalties.map((p) => p.key)).toEqual(["thinMargin"]);
  });

  it("does not let reliability flatter a loss-making row", () => {
    // Multiplying a negative by 0.5 makes it a smaller negative, which would
    // move the worst rows UP the table.
    const s = calcRowScore(
      row({
        product_daily_volume: 0.5,
        regional_avg_price_30d: 1_000_000,
        unit_ask_price: 2_000_000,
        unit_profit_30d: -50_000,
        optimal_build_cost: 1_000_000,
        profit: -1_000,
        profit_percent: -3,
      }),
    )!;
    expect(s.reliability).toBeLessThan(1);
    expect(s.score).toBeLessThan(0);
    expect(s.score).toBe(s.iskPerDay);
  });

  it("ranks a moon-priced row below steady movers with lower headline profit", () => {
    const scored = [steadyMover, buildBound, moonPrice].map((r) => ({ row: r, s: calcRowScore(r)! }));
    // The moon-price row has by far the biggest per-blueprint profit...
    expect(moonPrice.profit).toBeGreaterThan(steadyMover.profit);
    expect(moonPrice.profit).toBeGreaterThan(buildBound.profit);
    // ...and lands dead last once demand and price basis are priced in.
    const order = [...scored].sort((a, b) => b.s.score - a.s.score).map((x) => x.row);
    expect(order).toEqual([buildBound, steadyMover, moonPrice]);
    expect(scored[2].s.reliability).toBeCloseTo(0.68, 6); // askAboveAvg x subDailyChurn
    expect(scored[2].s.score).toBeCloseTo(6_800, 3);
  });
});

describe("calcRowScore capital margin of safety", () => {
  it("leaves a healthy return on capital alone", () => {
    // 340m earned on 200m committed, well past the 20% target.
    const s = calcRowScore(steadyMover)!;
    expect(s.batchRoi).toBeCloseTo(1.7, 6);
    expect(s.capitalFactor).toBe(1);
  });

  it("does not punish a row sitting exactly on the target", () => {
    const s = calcRowScore(buildBound)!;
    expect(s.batchRoi).toBeCloseTo(0.2, 6);
    expect(s.capitalFactor).toBe(1);
  });

  it("scales linearly between the floor and the target", () => {
    const s = calcRowScore(thinReturn)!;
    expect(s.capital).toBe(400_000_000);
    expect(s.grossProfit).toBe(40_000_000);
    expect(s.batchRoi).toBeCloseTo(0.1, 6);
    expect(s.capitalFactor).toBeCloseTo(0.5, 6);
    expect(s.iskPerDay).toBeCloseTo(500_000, 6);
    expect(s.score).toBeCloseTo(250_000, 6);
  });

  it("floors a capital hog rather than annihilating it", () => {
    const s = calcRowScore(capitalHog)!;
    expect(s.capital).toBe(6_000_000_000);
    expect(s.grossProfit).toBe(75_000_000);
    expect(s.batchRoi).toBeCloseTo(0.0125, 6);
    // 0.0125 / 0.2 = 0.0625, below the floor.
    expect(s.capitalFactor).toBe(0.15);
    expect(s.iskPerDay).toBeCloseTo(37_500_000, 3);
    expect(s.score).toBeCloseTo(5_625_000, 3);
  });

  it("does not punish a row with no cost data to judge capital on", () => {
    const s = calcRowScore(unknownCapital)!;
    expect(s.capital).toBe(0);
    expect(s.batchRoi).toBeNull();
    expect(s.capitalFactor).toBe(1);
  });

  it("ranks a capital hog below a row that earns less raw ISK per day", () => {
    const hog = calcRowScore(capitalHog)!;
    const cheap = calcRowScore(buildBound)!;
    // The hog genuinely earns more per day...
    expect(hog.iskPerDay).toBeGreaterThan(cheap.iskPerDay);
    // ...on thirty times the capital, so it ranks below.
    expect(hog.capital).toBeGreaterThan(cheap.capital * 25);
    expect(hog.score).toBeLessThan(cheap.score);
  });
});

describe("calcRowScore price trend", () => {
  const withTrend = (avg7d: number | undefined) =>
    calcRowScore({ ...steadyMover, regional_avg_price_7d: avg7d })!;

  it("penalises a sliding price once, with the gap that earned it", () => {
    const s = withTrend(880_000); // 12% under the 1m 30d average
    expect(s.penalties.map((p) => p.key)).toEqual(["fallingPrice"]);
    expect(s.penalties[0].value).toBeCloseTo(12, 6);
    expect(s.reliability).toBeCloseTo(0.75, 6);
  });

  it("escalates to the harsher tier for a crash", () => {
    const s = withTrend(700_000); // 30% under
    expect(s.penalties.map((p) => p.key)).toEqual(["crashingPrice"]);
    expect(s.penalties[0].value).toBeCloseTo(30, 6);
    expect(s.reliability).toBeCloseTo(0.55, 6);
  });

  it("puts the tier boundaries where the constants say", () => {
    expect(withTrend(1_000_000 * PRICE_FALLING_MULT).penalties[0].key).toBe("fallingPrice");
    expect(withTrend(1_000_000 * PRICE_CRASHING_MULT).penalties[0].key).toBe("crashingPrice");
  });

  it("is one-directional — a rising price is not a bonus", () => {
    expect(withTrend(1_000_000).penalties).toHaveLength(0);
    expect(withTrend(1_300_000).penalties).toHaveLength(0);
    expect(withTrend(1_300_000).score).toBe(withTrend(1_000_000).score);
  });

  it("reads a silent week as no signal rather than as a crash", () => {
    // 0 means "did not trade in the last 7 days", which is not the same as
    // "traded 100% below the average"; inventing a crash from it would be
    // wrong, and subDailyChurn already covers genuinely dead items.
    expect(withTrend(undefined).penalties).toHaveLength(0);
    expect(withTrend(0).penalties).toHaveLength(0);
  });
});

describe("computeScoreBands", () => {
  it("cuts bands from the current result set rather than absolute ISK", () => {
    const bands = computeScoreBands([100, 90, 80, 70, 60, 50, 40, 30, 20, 10])!;
    expect(bands).toEqual({ strong: 80, solid: 50, thin: 20 });
    expect(scoreBandFor(100, bands)).toBe("strong");
    expect(scoreBandFor(80, bands)).toBe("strong");
    expect(scoreBandFor(70, bands)).toBe("solid");
    expect(scoreBandFor(30, bands)).toBe("thin");
    expect(scoreBandFor(10, bands)).toBe("weak");
  });

  it("has no opinion with nothing to compare against", () => {
    expect(computeScoreBands([])).toBeNull();
    expect(scoreBandFor(1_000_000, null)).toBe("solid");
  });
});

describe("buildScoreTooltip", () => {
  it("leads with sell-through when that is the binding constraint", () => {
    const v = verdictOf(buildScoreTooltip(calcRowScore(steadyMover)!, "strong", t));
    expect(v.startsWith("Strong earner.")).toBe(true);
    expect(v).toContain("About 50 of these trade per day");
    expect(v).toContain("your 400 would take roughly 80 days to clear");
    expect(v).toContain("sell-through, not build time");
    expect(v).not.toContain("Demand is not the limit");
  });

  it("leads with build time when that is the binding constraint", () => {
    const v = verdictOf(buildScoreTooltip(calcRowScore(buildBound)!, "solid", t));
    expect(v).toContain("Demand is not the limit here");
    expect(v).toContain("across " + ASSUMED_SLOTS + " slots");
    expect(v).not.toContain("sell-through, not build time");
  });

  it("says the book is clean rather than manufacturing a caveat", () => {
    const s = calcRowScore(steadyMover)!;
    const v = verdictOf(buildScoreTooltip(s, "strong", t));
    expect(s.penalties).toHaveLength(0);
    expect(v).toContain("Price is steady — the ask sits within 12% of the 30-day traded average.");
    expect(v).toContain("Nothing on the order book is working against it.");
    expect(v).not.toContain("Holding it back");
  });

  it("names a penalty, with the figure that earned it, only when one applied", () => {
    const v = verdictOf(buildScoreTooltip(calcRowScore(moonPrice)!, "weak", t));
    expect(v).toContain("Holding it back: sub-daily churn, 0.50 units a day.");
  });

  it("states the traded-average basis on a moon-price row, and says it once", () => {
    const tip = buildScoreTooltip(calcRowScore(moonPrice)!, "weak", t);
    const v = verdictOf(tip);
    expect(v).toContain("The current ask is 3.1× what the item actually trades at");
    expect(v).toContain("30-day traded average and not from that ask");
    // The price sentence already made the point; repeating it as a separate
    // penalty would read like two different problems.
    expect(v).not.toContain("an ask 3.1× above the traded average");
    expect(v).not.toContain("Price is steady");
    // It still appears in the Trust breakdown, where the arithmetic lives.
    expect(tip).toContain("an ask 3.1× above the traded average");
  });

  it("backs the verdict with a batch matching the Planned column", () => {
    const tip = buildScoreTooltip(calcRowScore(steadyMover)!, "strong", t);
    expect(tip).toContain("Batch: 400 runs → 400 units");
    expect(tip).toContain("Cycle: 80d — bound by sell-through; building takes 1d across 10 slots");
    expect(tip).toContain("Trust: ×1.00 — no market-quality penalties");
  });

  it("names the capital problem with both ISK figures", () => {
    const tip = buildScoreTooltip(calcRowScore(capitalHog)!, "weak", t);
    const v = verdictOf(tip);
    expect(v).toContain("It ties up 6 B to make 75 M — a 1.3% return");
    expect(tip).toContain("Capital: 6 B tied up for a 1.3% return — ×0.15");
    // Nothing is wrong with the order book, but the row is still not clean.
    expect(calcRowScore(capitalHog)!.penalties).toHaveLength(0);
    expect(v).not.toContain("Nothing on the order book is working against it");
  });

  it("reports a healthy return on capital without a multiplier", () => {
    const tip = buildScoreTooltip(calcRowScore(steadyMover)!, "strong", t);
    expect(tip).toContain("Capital: 200 M tied up for a 170.0% return");
    // No trailing multiplier: a healthy return is stated, not scored.
    expect(tip).not.toContain("170.0% return —");
  });

  it("says so plainly when there is no cost data to judge capital on", () => {
    const tip = buildScoreTooltip(calcRowScore(unknownCapital)!, "solid", t);
    expect(tip).toContain("no cost data to judge the return on it");
  });

  it("gives a crashing price its own sentence instead of a penalty slot", () => {
    const s = calcRowScore({ ...steadyMover, regional_avg_price_7d: 700_000 })!;
    const v = verdictOf(buildScoreTooltip(s, "thin", t));
    expect(v).toContain("The price is coming down hard — the last 7 days traded 30% under");
    expect(v).toContain("a batch takes a week or two to reach the market");
    // Already said above; repeating it under "Holding it back" would read as
    // two separate problems.
    expect(v).not.toContain("Holding it back");
  });

  it("routes a merely sliding price through the normal penalty sentence", () => {
    const s = calcRowScore({ ...steadyMover, regional_avg_price_7d: 880_000 })!;
    const v = verdictOf(buildScoreTooltip(s, "solid", t));
    expect(v).toContain("Holding it back: a price sliding 12% under its 30-day average this past week.");
    expect(v).not.toContain("coming down hard");
  });

  it("calls out a loss against the traded average", () => {
    const s = calcRowScore(
      row({
        product_daily_volume: 50,
        regional_avg_price_30d: 1_000_000,
        unit_ask_price: 2_500_000,
        unit_profit_30d: -100_000,
        optimal_build_cost: 500_000,
      }),
    )!;
    const v = verdictOf(buildScoreTooltip(s, "weak", t));
    expect(v).toContain("it only looks profitable against the current ask");
  });
});
