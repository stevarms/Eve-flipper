import type { ProfitableScanRow } from "@/lib/types";
import type { TranslationKey } from "@/lib/i18n";
import { formatISK } from "@/lib/format";
import { suggestRunsForRow } from "@/lib/industryBatchCommit";

/** Share of a product's daily traded volume one builder can realistically
 *  capture. Matches profitableScanMarketShare in
 *  internal/api/industry_blueprint_scan.go — the backend caps 30d realized
 *  sales the same way, so the frontend's fill-time math and the server's
 *  period_profit agree about how much of the market is ours. */
export const INDUSTRY_MARKET_SHARE_CAP = 0.1;

/** Manufacturing slots assumed to run in parallel when turning a batch's job
 *  time into calendar days. One character has ten — the same reasoning that
 *  makes the batch tiers in suggestRunsForRow multiples of ten. */
export const ASSUMED_SLOTS = 10;

/** Return on the ISK a batch ties up, at or above which capital is not held
 *  against the row at all. Below it the row is scaled down: a batch returning
 *  1% of what it locks up has no margin of safety — ordinary price movement
 *  eats the whole profit, and you cannot build anything else meanwhile. */
export const TARGET_BATCH_ROI = 0.2;
/** Floor on that scaling. A thin-ROI row is demoted, not annihilated: it can
 *  still be worth building if it churns fast, and leaving a floor keeps the
 *  bottom of the table ordered by real ISK instead of by rounding noise. */
export const CAPITAL_FACTOR_FLOOR = 0.15;

// ---------------------------------------------------------------------------
// Buildability flag
// ---------------------------------------------------------------------------

// Buildability flag heuristic. Mirrors the calcConfidence pattern in
// ScanResultsTable.tsx: start at 100, subtract for each risk signal, and
// map the final score into High/Medium/Low with a hover-hint listing the
// specific reasons. All inputs come off the row, and fillTimeDays is derived
// the same way the engine derives sellable from producible.
//
// calcRowScore below reuses these thresholds rather than restating them, so
// the pill and the Score column can never disagree about what counts as a
// crowded book or a moon price.
// Market-quality thresholds, shared by the flag pill and the Score column so
// the two can never disagree about what counts as a crowded book, a glutted
// order book, or a moon-price ask.
/** Units already listed ahead of you, as a multiple of your batch. */
export const ASK_DEPTH_GLUT_MULT = 5;
/** Distinct sell orders at which the book counts as crowded. */
export const CROWDED_BOOK_ORDERS = 20;
/** Current ask over the 30d traded average, above which the ask is a bait
 *  price rather than the price the item changes hands at. */
export const ASK_ABOVE_AVG_MULT = 1.2;
/** Per-run margin below which profit sits inside the fee/drift noise. */
export const THIN_MARGIN_PCT = 2;
/** 7d traded average over the 30d one, at or below which the price is
 *  sliding. A build takes one to two weeks to deliver, so a price already
 *  8% under its month average is unlikely to still be there on delivery. */
export const PRICE_FALLING_MULT = 0.92;
/** The same ratio at which it is not drift any more. */
export const PRICE_CRASHING_MULT = 0.8;

export interface IndustryFlag {
  score: number;
  label: "high" | "medium" | "low" | "unknown";
  color: string;
  reasons: string[];
  fillTimeDays: number | null;
  sellable30d: number | null;
}
export function calcIndustryFlag(row: ProfitableScanRow): IndustryFlag {
  const totalUnits = row.total_quantity ?? row.runs * (row.output_qty_per_run ?? 1);
  const daily = row.product_daily_volume ?? 0;
  const askDepth = row.ask_depth_units ?? 0;
  const askOrders = row.ask_orders_count ?? 0;
  const unitAsk = row.unit_ask_price ?? 0;
  const avg30d = row.regional_avg_price_30d ?? 0;

  const share = daily * 30 * INDUSTRY_MARKET_SHARE_CAP;
  const sellable30d = share > 0 ? Math.min(totalUnits, share) : null;
  // "Days to move THIS batch at your realistic share." A run of 128 units
  // against 3/30d of share = ~1280 days. The single most useful reality
  // check for a scanner row and the primary driver of the flag score.
  const fillTimeDays = daily > 0 && totalUnits > 0
    ? totalUnits / (daily * INDUSTRY_MARKET_SHARE_CAP)
    : null;

  let score = 100;
  const reasons: string[] = [];

  if (fillTimeDays === null) {
    if (avg30d <= 0) {
      score -= 20;
      reasons.push("no 30d history — flying blind on demand");
    } else {
      score -= 10;
      reasons.push("no volume signal");
    }
  } else if (fillTimeDays > 365) {
    score -= 40;
    reasons.push(`fill time ~${Math.round(fillTimeDays)}d — batch would sit unsold for over a year`);
  } else if (fillTimeDays > 90) {
    score -= 30;
    reasons.push(`fill time ~${Math.round(fillTimeDays)}d — heavy inventory drag`);
  } else if (fillTimeDays > 30) {
    score -= 15;
    reasons.push(`fill time ~${Math.round(fillTimeDays)}d — slow mover`);
  } else if (fillTimeDays > 14) {
    score -= 5;
    reasons.push(`fill time ~${Math.round(fillTimeDays)}d — moderate`);
  }

  if (daily > 0 && daily < 1) {
    score -= 10;
    reasons.push(`sub-daily churn (${daily.toFixed(2)}/day)`);
  }

  if (totalUnits > 0 && askDepth >= totalUnits * ASK_DEPTH_GLUT_MULT) {
    score -= 15;
    reasons.push(`ask depth ${askDepth.toLocaleString()} units — ${(askDepth / Math.max(1, totalUnits)).toFixed(1)}× your run`);
  }
  if (askOrders >= CROWDED_BOOK_ORDERS) {
    score -= 10;
    reasons.push(`crowded book — ${askOrders} sellers ahead of you`);
  }

  if (unitAsk > 0 && avg30d > 0 && unitAsk >= avg30d * ASK_ABOVE_AVG_MULT) {
    const ratio = unitAsk / avg30d;
    score -= 15;
    reasons.push(`ask ${ratio.toFixed(1)}× the 30d average — price won't hold`);
  }

  // Direction, not level. The 30d average is stable by design, which also
  // means it lags: a price that started sliding two weeks ago still reads
  // healthy in it, and you find out on delivery day.
  const avg7d = row.regional_avg_price_7d ?? 0;
  if (avg7d > 0 && avg30d > 0 && avg7d <= avg30d * PRICE_FALLING_MULT) {
    const down = (1 - avg7d / avg30d) * 100;
    if (avg7d <= avg30d * PRICE_CRASHING_MULT) {
      score -= 25;
      reasons.push(`price crashing — last 7d traded ${down.toFixed(0)}% under the 30d average`);
    } else {
      score -= 10;
      reasons.push(`price sliding — last 7d traded ${down.toFixed(0)}% under the 30d average`);
    }
  }

  // Profitability guardrails. The market-side penalties above answer "can I
  // sell what I build?" — but a row can still be pathological (Zealot BPC:
  // profits fine at velocity, loses money per unit). These stop the pill
  // from showing OK on a build that would burn ISK regardless of market fit.
  if (row.profit < 0) {
    score -= 40;
    reasons.push(`build unprofitable — loss ${row.profit.toLocaleString(undefined, { maximumFractionDigits: 0 })} ISK per full BP`);
  } else if (row.profit_percent >= 0 && row.profit_percent < THIN_MARGIN_PCT) {
    // Positive but thin — sits inside the noise of fees, price drift, and
    // taxes. Not fatal, but shouldn't get a green pill on its own.
    score -= 15;
    reasons.push(`margin ${row.profit_percent.toFixed(1)}% — inside the noise floor`);
  }
  if (row.period_margin !== undefined && row.period_margin < 0) {
    // 30d ROI negative means even the market-share-capped realistic view
    // loses money — orthogonal to per-run profit and worth calling out
    // separately (an item can be per-run positive but 30d negative if
    // idle-capital drag on unsellable inventory outweighs realized profit).
    score -= 25;
    reasons.push(`30d ROI ${row.period_margin.toFixed(1)}% — market saturation eats the run`);
  }

  score = Math.max(0, Math.min(100, score));
  let label: IndustryFlag["label"];
  let color: string;
  if (fillTimeDays === null && avg30d <= 0) {
    label = "unknown";
    color = "text-slate-300 border-slate-500/60 bg-slate-800/40";
  } else if (score >= 75) {
    label = "high";
    color = "text-green-300 border-green-500/60 bg-green-900/20";
  } else if (score >= 45) {
    label = "medium";
    color = "text-yellow-300 border-yellow-500/60 bg-yellow-900/20";
  } else {
    label = "low";
    color = "text-red-300 border-red-500/60 bg-red-900/20";
  }
  return { score, label, color, reasons, fillTimeDays, sellable30d };
}

// ---------------------------------------------------------------------------
// Score
// ---------------------------------------------------------------------------

/** One market-quality penalty applied to a row's reliability multiplier.
 *  `value` is the number that earned it, so the tooltip can name the reason
 *  with a figure instead of an adjective. */
export interface ScorePenalty {
  key:
    | "askDepth"
    | "crowdedBook"
    | "askAboveAvg"
    | "subDailyChurn"
    | "fallingPrice"
    | "crashingPrice"
    | "negProfit"
    | "thinMargin"
    | "negPeriodMargin";
  /** Multiplier applied to reliability (always < 1). */
  factor: number;
  /** The measured figure behind the penalty (depth ratio, seller count, ...). */
  value: number;
}

export interface RowScore {
  /** Risk-adjusted ISK per day of the batch we would actually plan. The
   *  ranking number. */
  score: number;
  /** Before the reliability multiplier. */
  iskPerDay: number;
  /** Product of every applied penalty, 0..1. */
  reliability: number;
  penalties: ScorePenalty[];
  /** The batch this score is about — identical to the Planned column. */
  runs: number;
  units: number;
  unitProfit: number;
  grossProfit: number;
  /** Build cost of the batch: capital tied up until it sells. */
  capital: number;
  /** grossProfit / capital — return on the ISK locked up for one cycle, and
   *  equally the margin of safety: how far the price can move against you
   *  before the batch loses money. Null when the row carries no cost data. */
  batchRoi: number | null;
  /** Scaling applied for a thin batchRoi, 0..1. Kept separate from
   *  reliability: that one answers "will the book let me realise this",
   *  this one answers "is this a sane use of my ISK". */
  capitalFactor: number;
  cycleDays: number;
  buildDays: number;
  fillDays: number;
  /** Which of the two set cycleDays — the thing you'd have to change to
   *  make this row better. */
  boundBy: "fill" | "build";
  dailyVolume: number;
  /** Current best ask over the 30d traded average, or null when either
   *  side is missing. */
  askVsAvg: number | null;
  avg30d: number;
}

/** Penalty weights. Deliberately gentler than the flag's point deductions:
 *  the flag answers "should I worry?", the score has to stay a readable
 *  ISK/day figure, so a penalized row is demoted rather than annihilated. */
const PENALTY_FACTORS: Record<ScorePenalty["key"], number> = {
  askDepth: 0.85,
  crowdedBook: 0.85,
  askAboveAvg: 0.8,
  subDailyChurn: 0.85,
  fallingPrice: 0.75,
  crashingPrice: 0.55,
  negProfit: 0.5,
  thinMargin: 0.85,
  negPeriodMargin: 0.6,
};

/**
 * Risk-adjusted ISK per day of the batch this row would actually commit.
 *
 * Scoring the planned batch rather than one run is the whole point: the
 * ranking answers "if I tick this box, what happens", using the same
 * suggestRunsForRow that seeds the Planned column.
 *
 *   gross     = unit_profit_30d × units
 *   cycleDays = max(build days, sell-through days, 1)
 *   score     = gross / cycleDays × reliability
 *
 * cycleDays takes whichever of build and sell-through actually binds — you
 * cannot start the next batch of an item until the last one has cleared, or
 * you are just accumulating inventory. Invention time is not counted; on
 * every row where it would have mattered, fill time dominates anyway.
 *
 * Returns null when the product has no 30d trade history. Guessing a rank
 * from nothing is exactly what makes the current top-of-table untrustworthy,
 * so those rows render "—" and sort to the bottom instead.
 */
export function calcRowScore(row: ProfitableScanRow): RowScore | null {
  const daily = row.product_daily_volume ?? 0;
  const avg30d = row.regional_avg_price_30d ?? 0;
  const unitProfit = row.unit_profit_30d ?? 0;
  if (daily <= 0 || avg30d <= 0 || !Number.isFinite(unitProfit) || unitProfit === 0) {
    return null;
  }

  const runs = suggestRunsForRow(row).runs;
  const outputQty = row.output_qty_per_run && row.output_qty_per_run > 0 ? row.output_qty_per_run : 1;
  const units = runs * outputQty;
  const grossProfit = unitProfit * units;

  // row.manufacturing_time covers all of row.runs, so divide before scaling
  // to the batch we actually plan.
  const perRunSecs = row.runs > 0 ? row.manufacturing_time / row.runs : row.manufacturing_time;
  const buildDays = (perRunSecs * runs) / 86400 / ASSUMED_SLOTS;
  const fillDays = units / (daily * INDUSTRY_MARKET_SHARE_CAP);
  const cycleDays = Math.max(buildDays, fillDays, 1);
  const boundBy: RowScore["boundBy"] = fillDays >= buildDays ? "fill" : "build";

  const costPerRun = row.runs > 0 && row.optimal_build_cost > 0 ? row.optimal_build_cost / row.runs : 0;
  const capital = costPerRun * runs;

  const penalties: ScorePenalty[] = [];
  const askDepth = row.ask_depth_units ?? 0;
  const askOrders = row.ask_orders_count ?? 0;
  const unitAsk = row.unit_ask_price ?? 0;

  if (units > 0 && askDepth >= units * ASK_DEPTH_GLUT_MULT) {
    penalties.push({ key: "askDepth", factor: PENALTY_FACTORS.askDepth, value: askDepth / units });
  }
  if (askOrders >= CROWDED_BOOK_ORDERS) {
    penalties.push({ key: "crowdedBook", factor: PENALTY_FACTORS.crowdedBook, value: askOrders });
  }
  if (unitAsk > 0 && unitAsk >= avg30d * ASK_ABOVE_AVG_MULT) {
    penalties.push({ key: "askAboveAvg", factor: PENALTY_FACTORS.askAboveAvg, value: unitAsk / avg30d });
  }
  if (daily < 1) {
    penalties.push({ key: "subDailyChurn", factor: PENALTY_FACTORS.subDailyChurn, value: daily });
  }
  // Direction of travel. Deliberately one-sided: a price on the way up is not
  // a reason to score a row higher, because you still only get paid what it
  // trades at on delivery day.
  const avg7d = row.regional_avg_price_7d ?? 0;
  if (avg7d > 0 && avg7d <= avg30d * PRICE_FALLING_MULT) {
    const downPct = (1 - avg7d / avg30d) * 100;
    const key = avg7d <= avg30d * PRICE_CRASHING_MULT ? "crashingPrice" : "fallingPrice";
    penalties.push({ key, factor: PENALTY_FACTORS[key], value: downPct });
  }
  if (row.profit < 0) {
    penalties.push({ key: "negProfit", factor: PENALTY_FACTORS.negProfit, value: row.profit });
  } else if (row.profit_percent >= 0 && row.profit_percent < THIN_MARGIN_PCT) {
    penalties.push({ key: "thinMargin", factor: PENALTY_FACTORS.thinMargin, value: row.profit_percent });
  }
  if (row.period_margin !== undefined && row.period_margin < 0) {
    penalties.push({
      key: "negPeriodMargin",
      factor: PENALTY_FACTORS.negPeriodMargin,
      value: row.period_margin,
    });
  }

  // Fill time is deliberately absent from reliability: cycleDays already
  // prices it in, and double-counting it would make the ISK/day figure
  // unreadable as ISK/day.
  const reliability = penalties.reduce((acc, p) => acc * p.factor, 1);
  const iskPerDay = grossProfit / cycleDays;

  // Margin of safety on the ISK committed. A 6b batch returning 75m is a 1.3%
  // margin: real money in absolute terms, but a 2% move in the price turns it
  // into a loss, and it is 6b you cannot spend on anything else. Rows above
  // the target are untouched, so this only ever demotes.
  const batchRoi = capital > 0 ? grossProfit / capital : null;
  const capitalFactor =
    batchRoi === null || batchRoi >= TARGET_BATCH_ROI
      ? 1
      : Math.max(CAPITAL_FACTOR_FLOOR, batchRoi / TARGET_BATCH_ROI);

  // Penalizing a losing row would move it UP the table (a smaller negative),
  // so the multipliers only ever demote rows that make money.
  const score = iskPerDay > 0 ? iskPerDay * reliability * capitalFactor : iskPerDay;

  return {
    score,
    iskPerDay,
    reliability,
    penalties,
    runs,
    units,
    unitProfit,
    grossProfit,
    capital,
    batchRoi,
    capitalFactor,
    cycleDays,
    buildDays,
    fillDays,
    boundBy,
    dailyVolume: daily,
    askVsAvg: unitAsk > 0 ? unitAsk / avg30d : null,
    avg30d,
  };
}

export type ScoreBand = "strong" | "solid" | "thin" | "weak";

export interface ScoreBands {
  strong: number;
  solid: number;
  thin: number;
}

/**
 * Band cutoffs from the current result set rather than absolute ISK.
 * What counts as a good row depends on what else you could build with the
 * same ten slots — 20m/day is excellent in a batch of frigate ammo and
 * mediocre next to capital components.
 */
export function computeScoreBands(scores: number[]): ScoreBands | null {
  const sorted = scores.filter((s) => Number.isFinite(s)).sort((a, b) => b - a);
  if (sorted.length === 0) return null;
  const at = (pct: number) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * pct))];
  return { strong: at(0.2), solid: at(0.5), thin: at(0.8) };
}

export function scoreBandFor(score: number, bands: ScoreBands | null): ScoreBand {
  if (!bands) return "solid";
  if (score >= bands.strong) return "strong";
  if (score >= bands.solid) return "solid";
  if (score >= bands.thin) return "thin";
  return "weak";
}

// ---------------------------------------------------------------------------
// Tooltip
// ---------------------------------------------------------------------------

type Translate = (key: TranslationKey, params?: Record<string, string | number>) => string;

const BAND_LABEL_KEYS: Record<ScoreBand, TranslationKey> = {
  strong: "industryScannerScoreBandStrong",
  solid: "industryScannerScoreBandSolid",
  thin: "industryScannerScoreBandThin",
  weak: "industryScannerScoreBandWeak",
};

const PENALTY_KEYS: Record<ScorePenalty["key"], TranslationKey> = {
  askDepth: "industryScannerScorePenaltyDepth",
  crowdedBook: "industryScannerScorePenaltyCrowded",
  askAboveAvg: "industryScannerScorePenaltyAskGap",
  subDailyChurn: "industryScannerScorePenaltyChurn",
  fallingPrice: "industryScannerScorePenaltyFalling",
  crashingPrice: "industryScannerScorePenaltyCrashing",
  negProfit: "industryScannerScorePenaltyNegProfit",
  thinMargin: "industryScannerScorePenaltyThinMargin",
  negPeriodMargin: "industryScannerScorePenaltyNegMargin",
};

function penaltyText(t: Translate, p: ScorePenalty): string {
  switch (p.key) {
    case "askDepth":
      return t(PENALTY_KEYS.askDepth, { ratio: p.value.toFixed(1) });
    case "crowdedBook":
      return t(PENALTY_KEYS.crowdedBook, { count: Math.round(p.value).toLocaleString() });
    case "askAboveAvg":
      return t(PENALTY_KEYS.askAboveAvg, { ratio: p.value.toFixed(1) });
    case "subDailyChurn":
      return t(PENALTY_KEYS.subDailyChurn, { volume: p.value.toFixed(2) });
    case "fallingPrice":
      return t(PENALTY_KEYS.fallingPrice, { pct: p.value.toFixed(0) });
    case "crashingPrice":
      return t(PENALTY_KEYS.crashingPrice, { pct: p.value.toFixed(0) });
    case "negProfit":
      return t(PENALTY_KEYS.negProfit, { isk: formatISK(Math.abs(p.value)) });
    case "thinMargin":
      return t(PENALTY_KEYS.thinMargin, { pct: p.value.toFixed(1) });
    case "negPeriodMargin":
      return t(PENALTY_KEYS.negPeriodMargin, { pct: p.value.toFixed(1) });
  }
}

const days = (d: number) => Math.max(1, Math.round(d)).toLocaleString();

/**
 * Plain-English justification for a row's rank, with the arithmetic
 * underneath as backup.
 *
 * The Planned column's tooltip is a derivation, which is right for a number
 * you might want to override. Score is different: its job is to convince you
 * to click the checkbox, so it leads with a verdict. The rules that keep the
 * verdict honest rather than flavour text:
 *
 *   - Lead with the binding constraint — the thing you'd have to change to
 *     make this row better.
 *   - Name a market penalty only when one actually applied. A clean row says
 *     the book looks clean instead of manufacturing a caveat.
 *   - State the price basis whenever the ask diverges from the traded
 *     average. That one sentence is what stops a moon-price row from looking
 *     like a missed opportunity.
 *   - No adjective without the number that earned it.
 */
export function buildScoreTooltip(s: RowScore, band: ScoreBand, t: Translate): string {
  const verdict: string[] = [t(BAND_LABEL_KEYS[band])];

  if (s.boundBy === "fill") {
    verdict.push(
      t("industryScannerScoreVerdictFill", {
        volume: Math.round(s.dailyVolume).toLocaleString(),
        units: s.units.toLocaleString(),
        days: days(s.fillDays),
      }),
    );
  } else {
    verdict.push(
      t("industryScannerScoreVerdictBuild", {
        units: s.units.toLocaleString(),
        days: days(s.buildDays),
        slots: ASSUMED_SLOTS,
        fill: days(s.fillDays),
      }),
    );
  }

  // The price the profit is based on. Any meaningful divergence gets said out
  // loud, because that is the difference between a real earner and a row
  // whose headline profit is one optimistic seller.
  const gapCalledOut =
    s.askVsAvg !== null && (s.askVsAvg >= ASK_ABOVE_AVG_MULT || s.askVsAvg <= 0.8);
  if (gapCalledOut && s.askVsAvg !== null) {
    verdict.push(
      t("industryScannerScoreVerdictPriceGap", {
        ratio: s.askVsAvg.toFixed(1),
        avg: formatISK(s.avg30d),
      }),
    );
  } else if (s.askVsAvg !== null) {
    verdict.push(
      t("industryScannerScoreVerdictPriceSteady", {
        pct: Math.round(Math.abs(s.askVsAvg - 1) * 100).toString(),
      }),
    );
  }

  if (s.unitProfit < 0) {
    verdict.push(t("industryScannerScoreVerdictLoss", { isk: formatISK(Math.abs(s.unitProfit)) }));
  }

  // A crash gets its own sentence rather than a slot in the penalty list:
  // it is the one problem that gets worse the longer the batch takes, and a
  // batch takes one to two weeks.
  const crash = s.penalties.find((p) => p.key === "crashingPrice");
  if (crash) {
    verdict.push(t("industryScannerScoreVerdictCrashing", { pct: crash.value.toFixed(0) }));
  }

  // Capital before the penalty sentence: when it bites it is usually the
  // larger effect, and it is the one a headline ISK/day figure hides.
  if (s.capitalFactor < 1 && s.batchRoi !== null) {
    verdict.push(
      t("industryScannerScoreVerdictThinCapital", {
        capital: formatISK(s.capital),
        profit: formatISK(s.grossProfit),
        roi: (s.batchRoi * 100).toFixed(1),
      }),
    );
  }

  // Biggest applied penalty, skipping the ask-vs-average one when the price
  // sentence above already said it — repeating it would read like two
  // separate problems.
  const ranked = [...s.penalties]
    .filter((p) => !(gapCalledOut && p.key === "askAboveAvg") && p.key !== "crashingPrice")
    .sort((a, b) => a.factor - b.factor);
  if (ranked.length > 0) {
    verdict.push(t("industryScannerScoreVerdictPenalty", { reason: penaltyText(t, ranked[0]) }));
  } else if (s.penalties.length === 0 && s.capitalFactor === 1) {
    verdict.push(t("industryScannerScoreVerdictClean"));
  }

  const lines = [
    verdict.join(" "),
    "",
    `${t("industryScannerScoreRowMakes")}: ${t("industryScannerScoreMakesValue", {
      isk: formatISK(s.score),
    })}`,
    `${t("industryScannerScoreRowBatch")}: ${t("industryScannerScoreBatchValue", {
      runs: s.runs.toLocaleString(),
      units: s.units.toLocaleString(),
      isk: formatISK(s.capital),
    })}`,
    `${t("industryScannerScoreRowProfit")}: ${t("industryScannerScoreProfitValue", {
      total: formatISK(s.grossProfit),
      unit: formatISK(s.unitProfit),
    })}`,
    `${t("industryScannerScoreRowCapital")}: ${
      s.batchRoi === null
        ? t("industryScannerScoreCapitalUnknown", { capital: formatISK(s.capital) })
        : s.capitalFactor < 1
          ? t("industryScannerScoreCapitalValue", {
              capital: formatISK(s.capital),
              roi: (s.batchRoi * 100).toFixed(1),
              factor: s.capitalFactor.toFixed(2),
            })
          : t("industryScannerScoreCapitalHealthy", {
              capital: formatISK(s.capital),
              roi: (s.batchRoi * 100).toFixed(1),
            })
    }`,
    `${t("industryScannerScoreRowCycle")}: ${
      s.boundBy === "fill"
        ? t("industryScannerScoreCycleFill", {
            days: days(s.cycleDays),
            build: days(s.buildDays),
            slots: ASSUMED_SLOTS,
          })
        : t("industryScannerScoreCycleBuild", {
            days: days(s.cycleDays),
            slots: ASSUMED_SLOTS,
            fill: days(s.fillDays),
          })
    }`,
    `${t("industryScannerScoreRowTrust")}: ${
      s.penalties.length === 0
        ? t("industryScannerScoreTrustClean")
        : t("industryScannerScoreTrustValue", {
            factor: s.reliability.toFixed(2),
            reasons: s.penalties.map((p) => penaltyText(t, p)).join("; "),
          })
    }`,
  ];
  return lines.join("\n");
}
