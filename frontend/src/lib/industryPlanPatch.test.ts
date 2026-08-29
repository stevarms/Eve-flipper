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

describe("buildIndustryPlanPatch dependency edges", () => {
  // Refs are the negative placeholders the backend remaps to rowids at
  // insert: -(index + 1).
  const T1_BP = 12007;
  const T2_BP = 12006;
  const T2_ITEM = 12005;

  it("chains copy → invention → manufacturing", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          activity_plan: [
            { activity: "copy", product_type_id: T1_BP, blueprint_type_id: T1_BP, product_name: "Ishtar BPC copy", runs: 1 },
            { activity: "invention", product_type_id: T2_BP, blueprint_type_id: T1_BP, product_name: "Ishtar BPC", runs: 1 },
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Ishtar", runs: 4 },
          ],
        }),
      ),
    );
    const tasks = patch.tasks ?? [];
    expect(tasks[0].parent_task_id ?? 0).toBe(0); // copy is the root
    expect(tasks[1].parent_task_id).toBe(-1); // invention waits on the copy
    expect(tasks[2].parent_task_id).toBe(-2); // mfg waits on the invention
  });

  it("prefers the component edge over the invention edge", () => {
    // Only one prerequisite fits in the scalar column, and the component edge
    // is the one nothing can reconstruct after the fact — flat_materials
    // never mentions the intermediate.
    const COMPONENT = 11540;
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          material_tree: {
            type_id: T2_ITEM,
            is_base: false,
            should_build: true,
            blueprint: { blueprint_type_id: T2_BP },
            children: [
              { type_id: COMPONENT, is_base: false, should_build: true, blueprint: { blueprint_type_id: 99 }, children: [] },
              { type_id: 34, is_base: true, should_build: false, blueprint: null, children: [] },
            ],
          },
          activity_plan: [
            { activity: "invention", product_type_id: T2_BP, blueprint_type_id: T1_BP, product_name: "Ishtar BPC", runs: 1 },
            { activity: "manufacturing", product_type_id: COMPONENT, blueprint_type_id: 99, product_name: "Component", runs: 10 },
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Ishtar", runs: 4 },
          ],
        }),
      ),
    );
    const tasks = patch.tasks ?? [];
    expect(tasks[2].parent_task_id).toBe(-2); // the component, not the invention
  });

  it("leaves a base-material-only build with no edges", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          material_tree: {
            type_id: T2_ITEM,
            is_base: false,
            should_build: true,
            blueprint: { blueprint_type_id: T2_BP },
            children: [{ type_id: 34, is_base: true, should_build: false, blueprint: null, children: [] }],
          },
          activity_plan: [
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Ishtar", runs: 4 },
          ],
        }),
      ),
    );
    expect((patch.tasks ?? [])[0].parent_task_id ?? 0).toBe(0);
  });

  it("re-bases parent refs onto merged indices", () => {
    // Each patch numbers its refs from -1. Folding two rows into one plan
    // must renumber parents the same way it renumbers job refs, or the
    // second row's edges silently point at the first row's tasks.
    const rowA = buildIndustryPlanPatch(
      patchInput(
        analysis({
          activity_plan: [
            { activity: "invention", product_type_id: T2_BP, blueprint_type_id: T1_BP, product_name: "Ishtar BPC", runs: 1 },
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Ishtar", runs: 4 },
          ],
        }),
      ),
    );
    const rowB = buildIndustryPlanPatch(
      patchInput(
        analysis({
          activity_plan: [
            { activity: "invention", product_type_id: 22006, blueprint_type_id: 22007, product_name: "Eagle BPC", runs: 1 },
            { activity: "manufacturing", product_type_id: 22005, blueprint_type_id: 22006, product_name: "Eagle", runs: 2 },
          ],
        }),
      ),
    );
    const merged = mergeIndustryPlanPatches([rowA, rowB]);
    const tasks = merged.tasks ?? [];
    expect(tasks).toHaveLength(4);
    expect(tasks[1].parent_task_id).toBe(-1); // Ishtar mfg → Ishtar invention
    expect(tasks[3].parent_task_id).toBe(-3); // Eagle mfg → Eagle invention, not -1
  });
});

describe("buildIndustryPlanPatch per-task material attribution", () => {
  const T2_ITEM = 47127; // Standup Ametat II
  const T2_BP = 47242;
  const T1_ITEM = 47124; // Standup Ametat I — invention input, built from ore
  const COMPONENT = 11557; // Linear Shield Emitter
  const TRIT = 34;
  const PYERITE = 35;

  // The tree that reproduces the reported bug: a T2 build whose direct bill
  // is a T1 item plus a component, each with their own mineral bill.
  const t2Tree = {
    type_id: T2_ITEM,
    type_name: "Standup Ametat II",
    is_base: false,
    should_build: true,
    blueprint: { blueprint_type_id: T2_BP },
    children: [
      {
        type_id: T1_ITEM,
        type_name: "Standup Ametat I",
        quantity: 1,
        is_base: false,
        should_build: true,
        blueprint: { blueprint_type_id: 47125 },
        children: [
          { type_id: TRIT, type_name: "Tritanium", quantity: 5000, is_base: true, should_build: false, blueprint: null, children: [] },
        ],
      },
      {
        type_id: COMPONENT,
        type_name: "Linear Shield Emitter",
        quantity: 48,
        is_base: false,
        should_build: true,
        blueprint: { blueprint_type_id: 11558 },
        children: [
          { type_id: PYERITE, type_name: "Pyerite", quantity: 900, is_base: true, should_build: false, blueprint: null, children: [] },
        ],
      },
      { type_id: TRIT, type_name: "Tritanium", quantity: 200, is_base: true, should_build: false, blueprint: null, children: [] },
    ],
  };

  const t2Plan = [
    { activity: "manufacturing", product_type_id: T1_ITEM, blueprint_type_id: 47125, product_name: "Standup Ametat I", runs: 1 },
    { activity: "manufacturing", product_type_id: COMPONENT, blueprint_type_id: 11558, product_name: "Linear Shield Emitter", runs: 48 },
    { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Standup Ametat II", runs: 1 },
  ];

  const matsFor = (patch: ReturnType<typeof buildIndustryPlanPatch>, taskRef: number) =>
    (patch.materials ?? []).filter((m) => m.task_id === taskRef);

  it("gives every producing task its own direct bill", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(analysis({ material_tree: t2Tree, activity_plan: t2Plan })),
    );
    // The regression: intermediates used to come back empty because the whole
    // flattened bill landed on the final task.
    const t1Mats = matsFor(patch, -1);
    expect(t1Mats).toHaveLength(1);
    expect(t1Mats[0].type_id).toBe(TRIT);
    expect(t1Mats[0].required_qty).toBe(5000);

    const componentMats = matsFor(patch, -2);
    expect(componentMats).toHaveLength(1);
    expect(componentMats[0].type_id).toBe(PYERITE);
    expect(componentMats[0].required_qty).toBe(900);

    // The output task keeps only what its OWN job consumes.
    const outputMats = matsFor(patch, -3);
    expect(outputMats.map((m) => m.type_id).sort((a, b) => a - b)).toEqual(
      [T1_ITEM, COMPONENT, TRIT].sort((a, b) => a - b),
    );
    expect(outputMats.find((m) => m.type_id === TRIT)?.required_qty).toBe(200);
  });

  it("marks built children as build-sourced so they stay off the shopping list", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(analysis({ material_tree: t2Tree, activity_plan: t2Plan })),
    );
    const outputMats = matsFor(patch, -3);
    const t1Row = outputMats.find((m) => m.type_id === T1_ITEM);
    expect(t1Row?.source).toBe("build");
    expect(t1Row?.build_qty).toBe(1);
    expect(t1Row?.buy_qty).toBe(0);

    // A base mineral on the same task is still a market buy.
    const tritRow = outputMats.find((m) => m.type_id === TRIT);
    expect(tritRow?.source).toBe("market");
    expect(tritRow?.buy_qty).toBe(200);
    expect(tritRow?.build_qty).toBe(0);
  });

  it("splits a part-buy part-build child across buy_qty and build_qty", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          material_tree: {
            type_id: T2_ITEM,
            is_base: false,
            should_build: true,
            blueprint: { blueprint_type_id: T2_BP },
            children: [
              {
                type_id: COMPONENT,
                type_name: "Linear Shield Emitter",
                quantity: 100,
                is_base: false,
                should_build: true,
                should_split: true,
                buy_units: 30,
                build_units: 70,
                blueprint: { blueprint_type_id: 11558 },
                children: [],
              },
            ],
          },
          activity_plan: [
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Standup Ametat II", runs: 1 },
          ],
        }),
      ),
    );
    const row = matsFor(patch, -1)[0];
    expect(row.required_qty).toBe(100);
    expect(row.buy_qty).toBe(30);
    expect(row.build_qty).toBe(70);
    // Partly bought, so it belongs on the shopping list.
    expect(row.source).toBe("market");
  });

  it("sums a component shared by two branches into one merged bill", () => {
    // buildActivityPlan merges a shared component into a single step, so its
    // task must carry the bill for both occurrences.
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          material_tree: {
            type_id: T2_ITEM,
            is_base: false,
            should_build: true,
            blueprint: { blueprint_type_id: T2_BP },
            children: [
              {
                type_id: COMPONENT, quantity: 10, is_base: false, should_build: true,
                blueprint: { blueprint_type_id: 11558 },
                children: [{ type_id: TRIT, quantity: 100, is_base: true, should_build: false, blueprint: null, children: [] }],
              },
              {
                type_id: COMPONENT, quantity: 5, is_base: false, should_build: true,
                blueprint: { blueprint_type_id: 11558 },
                children: [{ type_id: TRIT, quantity: 50, is_base: true, should_build: false, blueprint: null, children: [] }],
              },
            ],
          },
          activity_plan: [
            { activity: "manufacturing", product_type_id: COMPONENT, blueprint_type_id: 11558, product_name: "Linear Shield Emitter", runs: 15 },
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Standup Ametat II", runs: 1 },
          ],
        }),
      ),
    );
    expect(matsFor(patch, -1)[0].required_qty).toBe(150); // 100 + 50
    expect(matsFor(patch, -2)[0].required_qty).toBe(15); // 10 + 5
  });

  it("falls back to the flat bill when the analysis carries no tree", () => {
    const patch = buildIndustryPlanPatch(
      patchInput(
        analysis({
          material_tree: null,
          flat_materials: [{ type_id: TRIT, type_name: "Tritanium", quantity: 900, unit_price: 5 }],
          activity_plan: [
            { activity: "manufacturing", product_type_id: T2_ITEM, blueprint_type_id: T2_BP, product_name: "Standup Ametat II", runs: 1 },
          ],
        }),
      ),
    );
    const mats = matsFor(patch, -1);
    expect(mats).toHaveLength(1);
    expect(mats[0].required_qty).toBe(900);
    expect(mats[0].source).toBe("market");
  });
});
