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
        // Prerequisite steps (invention → sub-mfg → final mfg) should sort
        // above later steps under a priority-DESC sort. Assign in reverse
        // so index 0 (usually invention) gets the highest number.
        priority: 100 + (activitySteps.length - index),
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
        notes: "",
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
      notes: "",
    });
  }

  // Materials: strictly this row's own needs. Coverage-derived data
  // (available_qty, buy_qty) is filled in later via
  // applyCoverageToIndustryPlanPatch — either post-merge (Scanner batch
  // flow) or immediately after build (Analyze tab flow). Emitting only
  // per-row needs is what makes multi-row merging correct: the merged
  // required_qty is the true cross-row total instead of N copies of the
  // shared coverage snapshot.
  //
  // Each row's materials are attributed to that row's OUTPUT task —
  // the last activity step, which is the manufacturing step for both
  // T1 direct-mfg rows and T2 invention→mfg chains. flat_materials is
  // a flattened bill (invention decryptors + mfg mats together), so
  // tagging them to the mfg task means the mfg task's block-status
  // pill reflects the full chain's needs. Enables per-task blocking
  // in Operations (task's own materials → available vs required).
  // Uses the same negative-int taskRef convention as jobs above.
  const outputTaskRef = tasks.length > 0 ? -tasks.length : -1;
  const materials: IndustryMaterialPlanInput[] = (result.flat_materials ?? [])
    .filter((m) => m.type_id > 0)
    .map((m) => {
      const requiredQty = Math.max(0, Math.ceil(m.quantity ?? 0));
      return {
        task_id: outputTaskRef,
        type_id: m.type_id,
        type_name: m.type_name || "",
        required_qty: requiredQty,
        available_qty: 0,
        buy_qty: requiredQty,
        build_qty: 0,
        unit_cost_isk: m.unit_price ?? 0,
        source: "market" as const,
      };
    });

  // Blueprints: only the ones this row's activity plan actually needs,
  // with per-row required_runs. Merge accumulates required_runs across
  // patches; owned-inventory fields (quantity, is_bpo, available_runs)
  // are overlaid post-merge by applyCoverageToIndustryPlanPatch so they
  // never inflate.
  const blueprintMap = new Map<number, IndustryBlueprintPoolInput>();
  for (const step of activitySteps) {
    if (!step.blueprint_type_id || step.blueprint_type_id <= 0) continue;
    const requiredRuns = stepRuns(step);
    const existing = blueprintMap.get(step.blueprint_type_id);
    // T2 BPCs (produced via invention) are always copies; T1 BPs default
    // to ownBlueprint (BPO). Without this, every sub-BP in a T2 chain
    // shows as BPO which is wrong for every T2 component.
    const isBpc = Boolean(step.blueprint_is_bpc);
    const isBpo = isBpc ? false : ownBlueprint;
    blueprintMap.set(step.blueprint_type_id, {
      blueprint_type_id: step.blueprint_type_id,
      blueprint_name: step.blueprint_name || existing?.blueprint_name || "",
      location_id: stationID || 0,
      quantity: 1,
      me,
      te,
      is_bpo: isBpo,
      available_runs: isBpo ? 0 : (existing?.available_runs ?? 0) + requiredRuns,
    });
  }
  if (blueprintMap.size === 0 && topBlueprintTypeID > 0) {
    blueprintMap.set(topBlueprintTypeID, {
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
  const blueprints = Array.from(blueprintMap.values());

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
  // Materials dedup key includes task_id now that buildIndustryPlanPatch
  // attributes each material to its owning output task. Two rows for the
  // same output product (rare — usually the scanner dedup collapses them
  // before we get here) can still land on the same (task, type) key.
  // The DB layer uses the same (task_id, type_id) as its ON CONFLICT key.
  const materialByTaskAndType = new Map<string, IndustryMaterialPlanInput>();
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
      const originalTaskRef = m.task_id;
      const remappedTaskRef = originalTaskRef !== undefined
        ? (taskIdRemap.get(originalTaskRef) ?? originalTaskRef)
        : 0;
      const key = `${remappedTaskRef}:${m.type_id}`;
      const existing = materialByTaskAndType.get(key);
      if (!existing) {
        materialByTaskAndType.set(key, { ...m, task_id: remappedTaskRef });
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
      materialByTaskAndType.set(key, {
        ...existing,
        task_id: remappedTaskRef,
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
    materials: Array.from(materialByTaskAndType.values()),
    blueprints: Array.from(blueprintByKey.values()),
    ...(scheduler ? { scheduler } : {}),
  };
}

// applyCoverageToIndustryPlanPatch overlays inventory data from a coverage
// snapshot onto a patch's materials and blueprints. Split from
// buildIndustryPlanPatch so multi-row batches (Scanner → merge → apply)
// don't double-count coverage into each source patch — which would inflate
// merged quantities N-fold, one per row in the batch.
//
// Runs post-merge for batch flows, post-build for single-item flows.
// Idempotent: applying twice with the same coverage produces the same
// result. Passing a null coverage is a no-op (returns the patch unchanged).
export function applyCoverageToIndustryPlanPatch(
  patch: IndustryPlanPatch,
  coverage: IndustryCoverageResult | null,
): IndustryPlanPatch {
  if (!coverage) return patch;

  const availByType = new Map<number, number>(
    (coverage.materials ?? []).map((m) => [m.type_id, Math.max(0, Math.ceil(m.available_qty ?? 0))]),
  );
  const bpByType = new Map<number, (typeof coverage.blueprints)[number]>(
    (coverage.blueprints ?? []).map((bp) => [bp.blueprint_type_id, bp]),
  );

  const nextMaterials: IndustryMaterialPlanInput[] = (patch.materials ?? []).map((m) => {
    const required = Math.max(0, Math.ceil(m.required_qty ?? 0));
    const rawAvailable = availByType.get(m.type_id) ?? 0;
    const available = Math.min(required, rawAvailable);
    const buy = Math.max(0, required - available);
    return {
      ...m,
      required_qty: required,
      available_qty: available,
      buy_qty: buy,
      source: buy > 0 ? ("market" as const) : ("stock" as const),
    };
  });

  const nextBlueprints: IndustryBlueprintPoolInput[] = (patch.blueprints ?? []).map((bp) => {
    const cov = bpByType.get(bp.blueprint_type_id);
    if (!cov || (cov.owned_qty ?? 0) <= 0) {
      return bp;
    }
    const hasBpo = (cov.bpo_qty ?? 0) > 0;
    if (hasBpo) {
      return {
        ...bp,
        blueprint_name: bp.blueprint_name || cov.blueprint_name || "",
        quantity: Math.max(1, cov.bpo_qty || 1),
        me: cov.best_me || bp.me,
        te: cov.best_te || bp.te,
        is_bpo: true,
        available_runs: 0,
      };
    }
    const hasBpc = (cov.bpc_qty ?? 0) > 0 && (cov.available_runs ?? 0) > 0;
    if (hasBpc) {
      return {
        ...bp,
        blueprint_name: bp.blueprint_name || cov.blueprint_name || "",
        quantity: Math.max(1, cov.bpc_qty || 1),
        me: cov.best_me || bp.me,
        te: cov.best_te || bp.te,
        is_bpo: false,
        available_runs: Math.max(0, cov.available_runs || 0),
      };
    }
    return bp;
  });

  return {
    ...patch,
    materials: nextMaterials,
    blueprints: nextBlueprints,
  };
}
