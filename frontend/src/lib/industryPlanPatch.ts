// Pure helpers for constructing and merging IndustryPlanPatch payloads.
//
// buildIndustryPlanPatch is the extracted body of buildAutoPlanPatch from
// IndustryTab.tsx — same emission rules, but with every closed-over React
// value promoted to an explicit input. Callers include the Analysis tab's
// "Seed draft" flow AND the Scanner's batch "Add to project" flow, so this
// module needs to stay side-effect-free.
//
// mergeIndustryPlanPatches folds several patches (e.g. one per Scanner row)
// into a single patch that the planner can apply as one plan.

import type {
  IndustryActivityStep,
  IndustryAnalysis,
  IndustryBlueprintPoolInput,
  IndustryCoverageResult,
  IndustryJobPlanInput,
  IndustryMaterialPlanInput,
  IndustryPlanPatch,
  IndustryTaskPlanInput,
} from "./types";

export interface BuildIndustryPlanPatchInput {
  result: IndustryAnalysis;
  coverage: IndustryCoverageResult | null;
  productTypeID: number;
  productName: string;
  runs: number;
  me: number;
  te: number;
  systemName: string;
  stationID: number;
  ownBlueprint: boolean;
  replace: boolean;
}

function stepRuns(step: IndustryActivityStep): number {
  if (step.activity === "invention" && step.expected_attempts) {
    return Math.max(1, Math.ceil(step.expected_attempts));
  }
  return Math.max(1, Math.ceil(step.runs || 1));
}

function stepLabel(step: IndustryActivityStep): string {
  const activity = step.activity || "industry";
  const product = step.product_name || `Type ${step.product_type_id}`;
  return `${activity} ${product}`;
}

export function buildIndustryPlanPatch(input: BuildIndustryPlanPatchInput): IndustryPlanPatch {
  const {
    result,
    coverage,
    productTypeID,
    productName,
    runs,
    me,
    te,
    systemName,
    stationID,
    ownBlueprint,
    replace,
  } = input;

  const topBlueprintTypeID = result.material_tree?.blueprint?.blueprint_type_id ?? 0;
  const activitySteps = result.activity_plan ?? [];
  const tasks: IndustryTaskPlanInput[] = [];
  const jobs: IndustryJobPlanInput[] = [];

  if (activitySteps.length > 0) {
    activitySteps.forEach((step, index) => {
      const targetRuns = stepRuns(step);
      const taskRef = -(index + 1);
      tasks.push({
        name: stepLabel(step),
        activity: step.activity || "manufacturing",
        product_type_id: step.product_type_id,
        target_runs: targetRuns,
        priority: 100 + index,
        status: "planned",
        constraints: {
          me,
          te,
          system_name: systemName,
          station_id: stationID || 0,
          blueprint_type_id: step.blueprint_type_id || 0,
          blueprint_location_id: stationID || 0,
          duration_seconds_per_run: targetRuns > 0 ? Math.round((step.time_seconds || 0) / targetRuns) : 0,
          cost_isk_per_run: targetRuns > 0 ? (step.job_cost || 0) / targetRuns : 0,
        },
      });
      jobs.push({
        task_id: taskRef,
        facility_id: stationID || 0,
        activity: step.activity || "manufacturing",
        runs: targetRuns,
        duration_seconds: step.time_seconds ?? 0,
        cost_isk: step.job_cost ?? 0,
        status: "planned",
        started_at: "",
        finished_at: "",
        notes: coverage ? "Coverage-aware draft from Industry analyzer" : "Draft from Industry analyzer activity plan",
      });
    });
  } else {
    const taskName = `Build ${productName}`;
    tasks.push({
      name: taskName,
      activity: "manufacturing",
      product_type_id: productTypeID,
      target_runs: runs,
      priority: 100,
      status: "planned",
      constraints: {
        me,
        te,
        system_name: systemName,
        station_id: stationID || 0,
        blueprint_type_id: topBlueprintTypeID || 0,
        blueprint_location_id: stationID || 0,
        duration_seconds_per_run: runs > 0 ? Math.round((result.manufacturing_time ?? 0) / runs) : 0,
        cost_isk_per_run: runs > 0 ? (result.total_job_cost ?? 0) / runs : 0,
      },
    });
    jobs.push({
      task_id: -1,
      facility_id: stationID || 0,
      activity: "manufacturing",
      runs,
      duration_seconds: result.manufacturing_time ?? 0,
      cost_isk: result.total_job_cost ?? 0,
      status: "planned",
      started_at: "",
      finished_at: "",
      notes: coverage ? "Coverage-aware draft from Industry analyzer" : "Auto-seeded from Industry analyzer",
    });
  }

  const flatByType = new Map((result.flat_materials ?? []).map((m) => [m.type_id, m]));
  const materialSourceRows = coverage?.materials?.length
    ? coverage.materials
    : (result.flat_materials ?? []).map((m) => ({
        type_id: m.type_id,
        type_name: m.type_name,
        required_qty: m.quantity,
        available_qty: 0,
        missing_qty: m.quantity,
        coverage_pct: 0,
        status: "missing" as const,
      }));
  const materials: IndustryMaterialPlanInput[] = materialSourceRows.map((m) => {
    const flat = flatByType.get(m.type_id);
    const requiredQty = Math.max(0, Math.ceil(m.required_qty ?? 0));
    const availableQty = Math.max(0, Math.min(requiredQty, Math.ceil(m.available_qty ?? 0)));
    const buyQty = Math.max(0, Math.ceil(m.missing_qty ?? Math.max(0, requiredQty - availableQty)));
    return {
      type_id: m.type_id,
      type_name: m.type_name || flat?.type_name || "",
      required_qty: requiredQty,
      available_qty: availableQty,
      buy_qty: buyQty,
      build_qty: 0,
      unit_cost_isk: flat?.unit_price ?? 0,
      source: buyQty > 0 ? ("market" as const) : ("stock" as const),
    };
  });

  const blueprintsFromCoverage: IndustryBlueprintPoolInput[] = (coverage?.blueprints ?? [])
    .filter((bp) => (bp.owned_qty ?? 0) > 0 && ((bp.bpo_qty ?? 0) > 0 || (bp.available_runs ?? 0) > 0))
    .map((bp) => {
      const isBPO = (bp.bpo_qty ?? 0) > 0;
      return {
        blueprint_type_id: bp.blueprint_type_id,
        blueprint_name: bp.blueprint_name || "",
        location_id: stationID || 0,
        quantity: isBPO ? Math.max(1, bp.bpo_qty || 1) : Math.max(1, bp.bpc_qty || 1),
        me: bp.best_me || me,
        te: bp.best_te || te,
        is_bpo: isBPO,
        available_runs: isBPO ? 0 : Math.max(0, bp.available_runs || 0),
      };
    });

  const fallbackBlueprintMap = new Map<number, IndustryBlueprintPoolInput>();
  if (blueprintsFromCoverage.length === 0) {
    for (const step of activitySteps) {
      if (!step.blueprint_type_id || step.blueprint_type_id <= 0) continue;
      const requiredRuns = stepRuns(step);
      const existing = fallbackBlueprintMap.get(step.blueprint_type_id);
      fallbackBlueprintMap.set(step.blueprint_type_id, {
        blueprint_type_id: step.blueprint_type_id,
        blueprint_name: step.blueprint_name || existing?.blueprint_name || "",
        location_id: stationID || 0,
        quantity: 1,
        me,
        te,
        is_bpo: ownBlueprint,
        available_runs: ownBlueprint ? 0 : (existing?.available_runs ?? 0) + requiredRuns,
      });
    }
    if (fallbackBlueprintMap.size === 0 && topBlueprintTypeID > 0) {
      fallbackBlueprintMap.set(topBlueprintTypeID, {
        blueprint_type_id: topBlueprintTypeID,
        blueprint_name: `${productName} Blueprint`,
        location_id: stationID || 0,
        quantity: 1,
        me,
        te,
        is_bpo: ownBlueprint,
        available_runs: ownBlueprint ? 0 : runs,
      });
    }
  }
  const blueprints = blueprintsFromCoverage.length > 0
    ? blueprintsFromCoverage
    : Array.from(fallbackBlueprintMap.values());

  return {
    replace,
    project_status: "planned",
    tasks,
    jobs,
    materials,
    blueprints,
  };
}

// mergeIndustryPlanPatches folds N patches (e.g. one per Scanner row) into a
// single patch. Job→task refs are local negative-int refs within a source
// patch, so we re-number them per source to preserve links. Materials and
// blueprints dedup by their natural key.
export function mergeIndustryPlanPatches(patches: IndustryPlanPatch[]): IndustryPlanPatch {
  if (patches.length === 0) {
    return { replace: false, project_status: "planned", tasks: [], jobs: [], materials: [], blueprints: [] };
  }
  if (patches.length === 1) {
    return patches[0];
  }

  const outTasks: IndustryTaskPlanInput[] = [];
  const outJobs: IndustryJobPlanInput[] = [];
  const materialByType = new Map<number, IndustryMaterialPlanInput>();
  const blueprintByKey = new Map<string, IndustryBlueprintPoolInput>();

  let taskOffset = 0;
  for (const patch of patches) {
    const tasks = patch.tasks ?? [];
    const jobs = patch.jobs ?? [];
    // Build a translation map from the source patch's local task_id refs
    // (negative ints, indexed by position in tasks) to their new global refs.
    // Tasks in buildIndustryPlanPatch use -(i+1) at index i.
    const taskIdRemap = new Map<number, number>();
    tasks.forEach((_, i) => {
      const sourceRef = -(i + 1);
      const newRef = -(taskOffset + i + 1);
      taskIdRemap.set(sourceRef, newRef);
    });

    for (const task of tasks) {
      outTasks.push(task);
    }
    for (const job of jobs) {
      const originalRef = job.task_id;
      const remapped = originalRef !== undefined ? taskIdRemap.get(originalRef) : undefined;
      outJobs.push({
        ...job,
        task_id: remapped ?? originalRef,
      });
    }
    taskOffset += tasks.length;

    for (const m of patch.materials ?? []) {
      const existing = materialByType.get(m.type_id);
      if (!existing) {
        materialByType.set(m.type_id, { ...m });
        continue;
      }
      const requiredQty = (existing.required_qty ?? 0) + (m.required_qty ?? 0);
      // available_qty is owned-assets snapshot per typeID — identical across
      // dupes in one coverage snapshot. Take max so summing doesn't inflate.
      const availableQty = Math.max(existing.available_qty ?? 0, m.available_qty ?? 0);
      const clampedAvailable = Math.min(requiredQty, availableQty);
      const buyQty = (existing.buy_qty ?? 0) + (m.buy_qty ?? 0);
      const buildQty = (existing.build_qty ?? 0) + (m.build_qty ?? 0);
      // Prefer the non-empty unit cost.
      const unitCost = existing.unit_cost_isk || m.unit_cost_isk || 0;
      materialByType.set(m.type_id, {
        ...existing,
        type_name: existing.type_name || m.type_name || "",
        required_qty: requiredQty,
        available_qty: clampedAvailable,
        buy_qty: buyQty,
        build_qty: buildQty,
        unit_cost_isk: unitCost,
        source: buyQty > 0 ? "market" : existing.source,
      });
    }

    for (const bp of patch.blueprints ?? []) {
      const key = `${bp.blueprint_type_id}-${bp.location_id}-${bp.is_bpo ? "bpo" : "bpc"}`;
      const existing = blueprintByKey.get(key);
      if (!existing) {
        blueprintByKey.set(key, { ...bp });
        continue;
      }
      blueprintByKey.set(key, {
        ...existing,
        blueprint_name: existing.blueprint_name || bp.blueprint_name || "",
        quantity: (existing.quantity ?? 0) + (bp.quantity ?? 0),
        available_runs: (existing.available_runs ?? 0) + (bp.available_runs ?? 0),
        me: Math.max(existing.me ?? 0, bp.me ?? 0),
        te: Math.max(existing.te ?? 0, bp.te ?? 0),
      });
    }
  }

  // Scheduler: first-patch-wins (a batch shares one context, so any patch's
  // scheduler is representative).
  const scheduler = patches.find((p) => p.scheduler)?.scheduler;

  return {
    replace: patches[0].replace ?? false,
    replace_blueprints: patches[0].replace_blueprints,
    project_status: patches[0].project_status ?? "planned",
    tasks: outTasks,
    jobs: outJobs,
    materials: Array.from(materialByType.values()),
    blueprints: Array.from(blueprintByKey.values()),
    ...(scheduler ? { scheduler } : {}),
  };
}
