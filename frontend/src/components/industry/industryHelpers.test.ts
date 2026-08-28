import { describe, expect, it } from "vitest";
import type { IndustryProjectSnapshot } from "@/lib/types";
import {
  deriveProjectValuation,
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
