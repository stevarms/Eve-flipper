import { describe, expect, it } from "vitest";
import { DEFAULT_RUNS_PREFS, suggestRunsForRow, type RunsSuggestionPrefs } from "./industryBatchCommit";
import type { ProfitableScanRow } from "./types";

// ProfitableScanRow has ~60 columns; only the ones suggestRunsForRow reads
// are worth spelling out, the rest would bury the assertion.
function row(parts: Partial<ProfitableScanRow>): ProfitableScanRow {
  return {
    type_id: 1,
    type_name: "BP",
    product_type_id: 2,
    product_name: "Product",
    owned_quantity: 1,
    is_bpo: false,
    available_runs: 0,
    runs: 1,
    optimal_build_cost: 0,
    output_qty_per_run: 1,
    ...parts,
  } as unknown as ProfitableScanRow;
}

/** Build cost per run, expressed the way a row carries it. */
function atCostPerRun(cost: number, parts: Partial<ProfitableScanRow>): ProfitableScanRow {
  return row({ runs: 100, optimal_build_cost: cost * 100, ...parts });
}

describe("suggestRunsForRow", () => {
  it("plans one whole blueprint when there is no volume signal", () => {
    // Nothing to size against. One 10-run BPC beats guessing a batch, and
    // beats the old behaviour of falling back to a single run.
    const s = suggestRunsForRow(
      row({ scan_mode: "t2_invention", output_bpc_runs: 10, product_daily_volume: 0 }),
    );
    expect(s.runs).toBe(10);
    expect(s.reason).toContain("No 30d volume signal");
  });

  it("reaches the top tier for a cheap high-volume item", () => {
    // Warrior II shape: moves in bulk, costs little, so neither ceiling bites.
    const s = suggestRunsForRow(
      atCostPerRun(500_000, {
        scan_mode: "t2_invention",
        output_bpc_runs: 10,
        product_daily_volume: 5_000,
      }),
    );
    expect(s.runs).toBe(400);
  });

  it("holds a large-ticket item at the bottom tier on capital, not demand", () => {
    // 25m/run against a 3b cap allows 120 runs, while demand would allow far
    // more. This is what makes a large smartbomb a 100-batch and a drone a
    // 400-batch without either being special-cased.
    const r = atCostPerRun(25_000_000, {
      scan_mode: "t2_invention",
      output_bpc_runs: 10,
      product_daily_volume: 500,
    });
    const s = suggestRunsForRow(r);
    expect(s.runs).toBe(100);
    expect(s.reason).toContain("Capital");
  });

  it("uses a whole blueprint rather than a single run on a thin mover", () => {
    // The user's example: 3/day means the demand ceiling is ~27 runs, which
    // snaps to two whole 10-run BPCs. Inventing a copy to build one item off
    // it is how a hangar fills up with part-used blueprints.
    const s = suggestRunsForRow(
      atCostPerRun(30_000_000, {
        scan_mode: "t2_invention",
        output_bpc_runs: 10,
        product_daily_volume: 3,
      }),
    );
    expect(s.runs).toBe(20);
    expect(s.runs % 10).toBe(0);
  });

  it("respects a decryptor-widened blueprint step", () => {
    // Parity decryptor: +3 runs per copy, so batches come in 13s.
    const s = suggestRunsForRow(
      atCostPerRun(500_000, {
        scan_mode: "t2_invention",
        output_bpc_runs: 10,
        best_decryptor_key: "parity",
        product_daily_volume: 5_000,
      }),
    );
    expect(s.runs % 13).toBe(0);
    expect(s.reason).toContain("13-run BPC");
  });

  it("caps an owned BPC at the runs actually on it", () => {
    const s = suggestRunsForRow(
      atCostPerRun(500_000, {
        scan_mode: "t1_mfg",
        owned: true,
        is_bpo: false,
        available_runs: 60,
        product_daily_volume: 5_000,
      }),
    );
    expect(s.runs).toBe(60);
    expect(s.reason).toContain("capped");
  });

  it("rounds up to finish owned copies that are nearly used", () => {
    // 110 available against a 100 target: leaving 10 runs on the copies is
    // worse than the extra tenth of a batch.
    const s = suggestRunsForRow(
      atCostPerRun(25_000_000, {
        scan_mode: "t1_mfg",
        owned: true,
        is_bpo: false,
        available_runs: 110,
        product_daily_volume: 500,
      }),
    );
    expect(s.runs).toBe(110);
    expect(s.reason).toContain("use the copies up");
  });

  it("leaves a BPO uncapped by its (infinite) run count", () => {
    const s = suggestRunsForRow(
      atCostPerRun(500_000, {
        scan_mode: "t1_mfg",
        owned: true,
        is_bpo: true,
        available_runs: 1,
        product_daily_volume: 5_000,
      }),
    );
    expect(s.runs).toBe(400);
  });

  it("divides multi-unit output into the demand ceiling", () => {
    // Ammo builds 100 per run, so the same daily volume buys far fewer runs.
    const perRun = atCostPerRun(200_000, {
      scan_mode: "t1_mfg",
      output_qty_per_run: 100,
      product_daily_volume: 5_000,
    });
    // 5000/day x 10% x 90d = 45,000 units = 450 runs -> top tier.
    expect(suggestRunsForRow(perRun).runs).toBe(400);
    // A tenth of the volume leaves 45 runs, below the smallest tier.
    expect(suggestRunsForRow({ ...perRun, product_daily_volume: 500 }).runs).toBe(40);
  });

  it("never suggests less than one run", () => {
    const s = suggestRunsForRow(
      atCostPerRun(500_000, { scan_mode: "t1_mfg", product_daily_volume: 0.01 }),
    );
    expect(s.runs).toBeGreaterThanOrEqual(1);
  });

  it("honours overridden preferences", () => {
    const prefs: RunsSuggestionPrefs = { ...DEFAULT_RUNS_PREFS, tiers: [50], maxFillDays: 30 };
    const s = suggestRunsForRow(
      atCostPerRun(500_000, { scan_mode: "t1_mfg", product_daily_volume: 5_000 }),
      prefs,
    );
    expect(s.runs).toBe(50);
  });
});
