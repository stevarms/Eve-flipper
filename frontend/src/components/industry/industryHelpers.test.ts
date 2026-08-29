import { describe, expect, it } from "vitest";
import type { IndustryProjectSnapshot } from "@/lib/types";
import {
  deriveProjectValuation,
  deriveTaskBlockStatus,
  inferTaskPrerequisites,
  sortOperationsTasks,
  splitTaskDecryptorSuffix,
} from "./industryHelpers";

// Fixtures are partial-by-design: deriveProjectValuation only reads the
// handful of fields asserted below, and spelling out every column of four
// record types would bury the thing under test.
function snapshot(parts: Partial<IndustryProjectSnapshot>): IndustryProjectSnapshot {
  return {
    project: {} as IndustryProjectSnapshot["project"],
    tasks: [],
    jobs: [],
    materials: [],
    blueprints: [],
    material_diff: [],
    ...parts,
  };
}

const task = (p: Record<string, unknown>) => p as unknown as IndustryProjectSnapshot["tasks"][number];
const job = (p: Record<string, unknown>) => p as unknown as IndustryProjectSnapshot["jobs"][number];
const mat = (p: Record<string, unknown>) => p as unknown as IndustryProjectSnapshot["materials"][number];
const diff = (p: Record<string, unknown>) => p as unknown as IndustryProjectSnapshot["material_diff"][number];

describe("deriveProjectValuation", () => {
  it("returns an empty, un-estimated rollup for no snapshot", () => {
    const v = deriveProjectValuation(null);
    expect(v.hasRevenueEstimate).toBe(false);
    expect(v.expectedRevenue).toBe(0);
    expect(v.totalCost).toBe(0);
  });

  it("sums revenue only from tasks carrying expected value", () => {
    const v = deriveProjectValuation(
      snapshot({
        tasks: [
          // Intermediate component — zeros, must not contribute.
          task({ id: 1, status: "planned", expected_unit_revenue: 0, expected_output_qty: 0 }),
          task({ id: 2, status: "planned", expected_unit_revenue: 1_000_000, expected_output_qty: 5 }),
          task({ id: 3, status: "planned", expected_unit_revenue: 250_000, expected_output_qty: 2 }),
        ],
      }),
    );
    expect(v.expectedRevenue).toBe(5_500_000);
    expect(v.hasRevenueEstimate).toBe(true);
  });

  it("ignores cancelled tasks and cancelled/failed jobs", () => {
    const v = deriveProjectValuation(
      snapshot({
        tasks: [
          task({ id: 1, status: "cancelled", expected_unit_revenue: 9_000_000, expected_output_qty: 10 }),
          task({ id: 2, status: "planned", expected_unit_revenue: 1_000, expected_output_qty: 1 }),
        ],
        jobs: [
          job({ id: 1, status: "planned", cost_isk: 100 }),
          job({ id: 2, status: "cancelled", cost_isk: 5_000 }),
          job({ id: 3, status: "failed", cost_isk: 7_000 }),
        ],
      }),
    );
    expect(v.expectedRevenue).toBe(1_000);
    expect(v.jobCost).toBe(100);
  });

  it("computes material cost, total cost, profit and margin", () => {
    const v = deriveProjectValuation(
      snapshot({
        tasks: [task({ id: 1, status: "planned", expected_unit_revenue: 100, expected_output_qty: 100 })],
        materials: [
          mat({ task_id: 1, type_id: 34, required_qty: 1_000, unit_cost_isk: 5 }),
          mat({ task_id: 1, type_id: 35, required_qty: 200, unit_cost_isk: 10 }),
        ],
        jobs: [job({ id: 1, status: "planned", cost_isk: 1_000 })],
      }),
    );
    expect(v.materialCost).toBe(7_000);
    expect(v.jobCost).toBe(1_000);
    expect(v.totalCost).toBe(8_000);
    expect(v.expectedProfit).toBe(2_000);
    expect(v.marginPct).toBeCloseTo(0.2, 10);
  });

  it("prices the shortfall using the highest quoted unit cost per type", () => {
    const v = deriveProjectValuation(
      snapshot({
        materials: [
          mat({ task_id: 1, type_id: 34, required_qty: 100, unit_cost_isk: 5 }),
          mat({ task_id: 2, type_id: 34, required_qty: 100, unit_cost_isk: 8 }),
        ],
        material_diff: [
          diff({ type_id: 34, required_qty: 200, available_qty: 50, missing_qty: 150 }),
          // Covered material contributes nothing.
          diff({ type_id: 35, required_qty: 10, available_qty: 10, missing_qty: 0 }),
        ],
      }),
    );
    expect(v.remainingBuyCost).toBe(150 * 8);
  });

  it("reports costs but no revenue estimate for legacy pre-capture projects", () => {
    const v = deriveProjectValuation(
      snapshot({
        tasks: [task({ id: 1, status: "planned", expected_unit_revenue: 0, expected_output_qty: 0 })],
        materials: [mat({ task_id: 1, type_id: 34, required_qty: 10, unit_cost_isk: 3 })],
      }),
    );
    expect(v.hasRevenueEstimate).toBe(false);
    expect(v.materialCost).toBe(30);
    // Profit is meaningless without revenue; the UI hides it on this flag.
    expect(v.marginPct).toBe(0);
  });
});

describe("sortOperationsTasks", () => {
  it("orders by depth, then prerequisite activity, then priority, then id", () => {
    const tasks = [
      task({ id: 4, activity: "manufacturing", priority: 100 }),
      task({ id: 1, activity: "invention", priority: 100 }),
      task({ id: 2, activity: "copy", priority: 100 }),
      task({ id: 3, activity: "reaction", priority: 100 }),
    ];
    // No depth data — the scanner-committed norm, where prerequisite order
    // is the only thing keeping copy above invention above manufacturing.
    expect(sortOperationsTasks(tasks, {}).map((t) => t.id)).toEqual([2, 1, 3, 4]);
  });

  it("puts shallower dependency depth first regardless of activity", () => {
    const tasks = [
      task({ id: 1, activity: "manufacturing", priority: 0 }),
      task({ id: 2, activity: "copy", priority: 0 }),
    ];
    expect(sortOperationsTasks(tasks, { 1: 1, 2: 2 }).map((t) => t.id)).toEqual([1, 2]);
  });

  it("breaks activity ties on priority desc then id asc", () => {
    const tasks = [
      task({ id: 9, activity: "manufacturing", priority: 10 }),
      task({ id: 3, activity: "manufacturing", priority: 50 }),
      task({ id: 1, activity: "manufacturing", priority: 50 }),
    ];
    expect(sortOperationsTasks(tasks, {}).map((t) => t.id)).toEqual([1, 3, 9]);
  });

  it("does not mutate the input array", () => {
    const tasks = [task({ id: 2, activity: "manufacturing" }), task({ id: 1, activity: "copy" })];
    sortOperationsTasks(tasks, {});
    expect(tasks.map((t) => t.id)).toEqual([2, 1]);
  });
});

describe("splitTaskDecryptorSuffix", () => {
  it("splits the decryptor off an invention label", () => {
    expect(splitTaskDecryptorSuffix("invention Zealot Blueprint · Symmetry Decryptor", "invention")).toEqual({
      base: "invention Zealot Blueprint",
      decryptor: "Symmetry Decryptor",
    });
  });

  it("leaves non-invention labels alone even when they contain the separator", () => {
    expect(splitTaskDecryptorSuffix("manufacturing Widget · Mk II", "manufacturing")).toEqual({
      base: "manufacturing Widget · Mk II",
      decryptor: "",
    });
  });

  it("returns no decryptor when an invention label has no suffix", () => {
    expect(splitTaskDecryptorSuffix("invention Zealot Blueprint", "invention")).toEqual({
      base: "invention Zealot Blueprint",
      decryptor: "",
    });
  });
});

describe("inferTaskPrerequisites", () => {
  // Type IDs standing in for a T2 build: the T1 BPO (copied), the T2 BP
  // (invented), and the T2 item (manufactured).
  const T1_BP = 1001;
  const T2_BP = 2002;
  const T2_ITEM = 3003;

  const t2Chain = () => [
    task({ id: 10, status: "planned", activity: "copy", product_type_id: T1_BP, constraints: { blueprint_type_id: T1_BP } }),
    task({ id: 11, status: "planned", activity: "invention", product_type_id: T2_BP, constraints: { blueprint_type_id: T1_BP } }),
    task({ id: 12, status: "planned", activity: "manufacturing", product_type_id: T2_ITEM, constraints: { blueprint_type_id: T2_BP } }),
  ];

  it("returns nothing for a null snapshot", () => {
    expect(inferTaskPrerequisites(null)).toEqual({});
  });

  it("links manufacturing to the invention that produces its blueprint", () => {
    const edges = inferTaskPrerequisites(snapshot({ tasks: t2Chain() }));
    expect(edges[12]).toEqual([11]);
  });

  it("links invention to the copy that produces its source blueprint", () => {
    const edges = inferTaskPrerequisites(snapshot({ tasks: t2Chain() }));
    expect(edges[11]).toEqual([10]);
  });

  it("links manufacturing to research done on the same blueprint", () => {
    const edges = inferTaskPrerequisites(
      snapshot({
        tasks: [
          task({ id: 20, status: "planned", activity: "research_material", product_type_id: T2_BP, constraints: { blueprint_type_id: T2_BP } }),
          task({ id: 21, status: "planned", activity: "manufacturing", product_type_id: T2_ITEM, constraints: { blueprint_type_id: T2_BP } }),
        ],
      }),
    );
    expect(edges[21]).toEqual([20]);
  });

  it("never treats a cancelled task as a prerequisite", () => {
    // Cancelling the invention step is how the user says "I already have
    // that blueprint" — the mfg step must not stay blocked on it.
    const tasks = t2Chain();
    tasks[1] = task({ ...(tasks[1] as unknown as Record<string, unknown>), status: "cancelled" });
    const edges = inferTaskPrerequisites(snapshot({ tasks }));
    expect(edges[12]).toBeUndefined();
  });

  it("does not link tasks that share no blueprint", () => {
    const edges = inferTaskPrerequisites(
      snapshot({
        tasks: [
          task({ id: 30, status: "planned", activity: "invention", product_type_id: 9999, constraints: { blueprint_type_id: T1_BP } }),
          task({ id: 31, status: "planned", activity: "manufacturing", product_type_id: T2_ITEM, constraints: { blueprint_type_id: T2_BP } }),
        ],
      }),
    );
    expect(edges[31]).toBeUndefined();
  });
});

describe("deriveTaskBlockStatus prerequisites", () => {
  // Materials all in stock, so materials never colour the result — the only
  // thing under test is what a pending prerequisite does.
  const stocked = (taskID: number) => mat({ task_id: taskID, type_id: 34, required_qty: 10, available_qty: 10 });

  const twoStep = (inventionStatus: string) =>
    snapshot({
      tasks: [
        task({ id: 1, name: "Invent Quake XL BP", status: inventionStatus, activity: "invention", product_type_id: 2002, constraints: {} }),
        task({ id: 2, name: "Manufacturing Quake XL", status: "planned", activity: "manufacturing", product_type_id: 3003, constraints: {} }),
      ],
      materials: [stocked(1), stocked(2)],
    });

  it("is amber, not blocked, while a prerequisite is outstanding", () => {
    // The user's call: an inferred edge shouldn't refuse to offer the job.
    const status = deriveTaskBlockStatus(2, twoStep("planned"), { 2: [1] });
    expect(status.level).toBe("soft");
    expect(status.prereqPending).toBe(1);
    expect(status.parentBlocking).toEqual([1]);
  });

  it("names the blocking task so the reason is actionable", () => {
    const status = deriveTaskBlockStatus(2, twoStep("planned"), { 2: [1] });
    expect(status.reason).toContain("Invent Quake XL BP");
  });

  it("goes ready once the prerequisite completes", () => {
    const status = deriveTaskBlockStatus(2, twoStep("completed"), { 2: [1] });
    expect(status.level).toBe("ready");
    expect(status.prereqPending).toBe(0);
  });

  it("treats a cancelled prerequisite as satisfied", () => {
    const status = deriveTaskBlockStatus(2, twoStep("cancelled"), { 2: [1] });
    expect(status.level).toBe("ready");
  });

  it("walks transitively — a pending copy blocks the manufacturing step", () => {
    const snap = snapshot({
      tasks: [
        task({ id: 1, name: "Copy Quake BPO", status: "planned", activity: "copy", product_type_id: 1001, constraints: {} }),
        task({ id: 2, name: "Invent Quake XL BP", status: "planned", activity: "invention", product_type_id: 2002, constraints: {} }),
        task({ id: 3, name: "Manufacturing Quake XL", status: "planned", activity: "manufacturing", product_type_id: 3003, constraints: {} }),
      ],
      materials: [stocked(3)],
    });
    const status = deriveTaskBlockStatus(3, snap, { 3: [2], 2: [1] });
    expect(status.level).toBe("soft");
    expect(status.parentBlocking.sort()).toEqual([1, 2]);
  });

  it("still reports hard when a material is genuinely absent", () => {
    // A pending prerequisite softens nothing — an empty hangar is still hard.
    const snap = snapshot({
      tasks: [
        task({ id: 1, name: "Invent Quake XL BP", status: "planned", activity: "invention", product_type_id: 2002, constraints: {} }),
        task({ id: 2, name: "Manufacturing Quake XL", status: "planned", activity: "manufacturing", product_type_id: 3003, constraints: {} }),
      ],
      materials: [mat({ task_id: 2, type_id: 34, required_qty: 10, available_qty: 0 })],
    });
    const status = deriveTaskBlockStatus(2, snap, { 2: [1] });
    expect(status.level).toBe("hard");
    expect(status.prereqPending).toBe(1);
  });

  it("does not block a copy step on its own downstream consumers", () => {
    const snap = snapshot({
      tasks: [task({ id: 1, name: "Copy Quake BPO", status: "planned", activity: "copy", product_type_id: 1001, constraints: {} })],
      materials: [],
    });
    expect(deriveTaskBlockStatus(1, snap, {}).level).toBe("ready");
  });
});
