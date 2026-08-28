import { describe, expect, it } from "vitest";
import {
  applyJobSplitToIndustryPlanPatch,
  buildIndustryPlanPatch,
  mergeIndustryPlanPatches,
} from "./industryPlanPatch";
import type { IndustryAnalysis } from "./types";

// Only the fields buildIndustryPlanPatch reads are populated; IndustryAnalysis
// has ~40 columns and spelling them all out obscures the assertion.
function analysis(parts: Record<string, unknown>): IndustryAnalysis {
  return {
    target_type_id: 12005,
    target_type_name: "Ishtar",
    runs: 1,
    total_quantity: 1,
    sell_revenue: 0,
    optimal_build_cost: 0,
    material_tree: {},
    flat_materials: [],
    ...parts,
  } as unknown as IndustryAnalysis;
}

function patchInput(result: IndustryAnalysis) {
  return {
    result,
    productTypeID: 12005,
    productName: "Ishtar",
    runs: 1,
    me: 10,
    te: 20,
    systemName: "Jita",
    stationID: 60003760,
    ownBlueprint: true,
    replace: false,
  };
}

describe("buildIndustryPlanPatch expected-value capture", () => {
  it("puts per-unit revenue and cost on the output task only", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          total_quantity: 4,
          sell_revenue: 400_000_000,
          optimal_build_cost: 300_000_000,
          activity_plan: [
            { activity: "invention", product_type_id: 12006, product_name: "Ishtar BPC", runs: 1 },
            { activity: "manufacturing", product_type_id: 12005, product_name: "Ishtar", runs: 4 },
          ],
        }),
      ),
    );
    const tasks = patch.tasks ?? [];
    expect(tasks).toHaveLength(2);
    // Intermediate invention task stays unvalued so a project-wide sum can't
    // double-count it against the hull it feeds.
    expect(tasks[0].expected_output_qty ?? 0).toBe(0);
    expect(tasks[0].expected_unit_revenue ?? 0).toBe(0);
    // Output task carries the whole chain's economics.
    expect(tasks[1].expected_output_qty).toBe(4);
    expect(tasks[1].expected_unit_revenue).toBe(100_000_000);
    expect(tasks[1].expected_unit_cost).toBe(75_000_000);
  });

  it("leaves expected value unset when the analysis produces nothing", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(analysis({ total_quantity: 0, sell_revenue: 123, optimal_build_cost: 456 })),
    );
    const tasks = patch.tasks ?? [];
    expect(tasks[tasks.length - 1].expected_output_qty).toBeUndefined();
  });
});

describe("mergeIndustryPlanPatches expected-value blending", () => {
  it("sums output quantity and quantity-weights the unit economics", () => {
    const rowA = buildIndustryPlanPatch(
      patchInput(
        analysis({
          total_quantity: 1,
          sell_revenue: 100,
          optimal_build_cost: 60,
          activity_plan: [{ activity: "manufacturing", product_type_id: 12005, product_name: "Ishtar", runs: 1 }],
        }),
      ),
    );
    const rowB = buildIndustryPlanPatch(
      patchInput(
        analysis({
          total_quantity: 3,
          sell_revenue: 900,
          optimal_build_cost: 600,
          activity_plan: [{ activity: "manufacturing", product_type_id: 12005, product_name: "Ishtar", runs: 3 }],
        }),
      ),
    );
    const merged = mergeIndustryPlanPatches([rowA, rowB]);
    const tasks = merged.tasks ?? [];
    // Same activity + product + name → one surviving task.
    expect(tasks).toHaveLength(1);
    expect(tasks[0].expected_output_qty).toBe(4);
    // (100×1 + 300×3) / 4 = 250
    expect(tasks[0].expected_unit_revenue).toBe(250);
    // (60×1 + 200×3) / 4 = 165
    expect(tasks[0].expected_unit_cost).toBe(165);
  });
});

describe("applyJobSplitToIndustryPlanPatch", () => {
  const patchWith = (jobs: unknown[]) =>
    ({ jobs } as unknown as Parameters<typeof applyJobSplitToIndustryPlanPatch>[0]);

  it("leaves jobs inside both limits untouched, with no batch note added", () => {
    const patch = patchWith([
      { task_id: -1, activity: "manufacturing", runs: 10, duration_seconds: 3600, cost_isk: 500, notes: "" },
    ]);
    const out = applyJobSplitToIndustryPlanPatch(patch, { maxRunsPerJob: 200, maxDurationHours: 24 });
    // Untouched means the same object identity, not merely equal contents.
    expect(out).toBe(patch);
    expect(out.jobs?.[0].notes).toBe("");
  });

  it("splits on the runs cap and distributes runs evenly", () => {
    const out = applyJobSplitToIndustryPlanPatch(
      patchWith([{ task_id: -1, activity: "invention", runs: 141, duration_seconds: 1410, cost_isk: 1410 }]),
      { maxRunsPerJob: 100, maxDurationHours: 0 },
    );
    const jobs = out.jobs ?? [];
    expect(jobs.map((j) => j.runs)).toEqual([71, 70]);
    // Even split, not [100, 41] — both installs finish together.
    expect(jobs.map((j) => j.notes)).toEqual(["batch 1/2", "batch 2/2"]);
  });

  it("splits on the duration cap", () => {
    // 3600s per run, 4h ceiling → 4 runs per install.
    const out = applyJobSplitToIndustryPlanPatch(
      patchWith([{ task_id: -1, activity: "manufacturing", runs: 10, duration_seconds: 36_000, cost_isk: 1000 }]),
      { maxRunsPerJob: 0, maxDurationHours: 4 },
    );
    const jobs = out.jobs ?? [];
    expect(jobs).toHaveLength(3);
    expect(jobs.map((j) => j.runs)).toEqual([4, 3, 3]);
    expect(jobs.every((j) => (j.duration_seconds ?? 0) <= 4 * 3600)).toBe(true);
  });

  it("conserves total runs, duration and cost across the split", () => {
    const out = applyJobSplitToIndustryPlanPatch(
      patchWith([{ task_id: -1, activity: "manufacturing", runs: 141, duration_seconds: 14_100, cost_isk: 987 }]),
      { maxRunsPerJob: 20, maxDurationHours: 24 },
    );
    const jobs = out.jobs ?? [];
    expect(jobs.reduce((a, j) => a + (j.runs ?? 0), 0)).toBe(141);
    expect(jobs.reduce((a, j) => a + (j.duration_seconds ?? 0), 0)).toBe(14_100);
    expect(jobs.reduce((a, j) => a + (j.cost_isk ?? 0), 0)).toBeCloseTo(987, 6);
  });

  it("never splits below one run even when a single run blows the cap", () => {
    // One run takes 10h against a 4h ceiling — indivisible.
    const out = applyJobSplitToIndustryPlanPatch(
      patchWith([{ task_id: -1, activity: "manufacturing", runs: 3, duration_seconds: 108_000, cost_isk: 300 }]),
      { maxRunsPerJob: 0, maxDurationHours: 4 },
    );
    expect(out.jobs?.map((j) => j.runs)).toEqual([1, 1, 1]);
  });

  it("keeps existing notes and appends the batch tag", () => {
    const out = applyJobSplitToIndustryPlanPatch(
      patchWith([{ task_id: -1, activity: "copy", runs: 4, duration_seconds: 400, cost_isk: 40, notes: "hangar 3" }]),
      { maxRunsPerJob: 2, maxDurationHours: 0 },
    );
    expect(out.jobs?.map((j) => j.notes)).toEqual(["hangar 3 · batch 1/2", "hangar 3 · batch 2/2"]);
  });

  it("is a no-op when both limits are disabled", () => {
    const patch = patchWith([{ task_id: -1, activity: "manufacturing", runs: 5000, duration_seconds: 1, cost_isk: 1 }]);
    expect(applyJobSplitToIndustryPlanPatch(patch, { maxRunsPerJob: 0, maxDurationHours: 0 })).toBe(patch);
  });
});
